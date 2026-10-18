// Package lemming holds lemmingd's routing state and its control API.
package lemming

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BOPOHA/hamsterd/internal/config"
	"github.com/BOPOHA/hamsterd/internal/control"
	"github.com/BOPOHA/hamsterd/internal/proxycore"
	"github.com/BOPOHA/hamsterd/internal/redirect"
)

// Rule sources.
const (
	SourceConfig = "config"
	SourceAuto   = "auto"
)

const recentRequests = 200

// Info describes the running proxy for the status endpoint.
type Info struct {
	Version       string    `json:"version"`
	Listen        string    `json:"listen"`
	ProxyURL      string    `json:"proxy_url"`
	ConfigFile    string    `json:"config_file"`
	AutoRulesFile string    `json:"auto_rules_file"`
	CACertFile    string    `json:"ca_cert_file"`
	CAFingerprint string    `json:"ca_sha256_fingerprint"`
	CANotAfter    time.Time `json:"ca_not_after"`
}

// Service holds the effective routing rules. Rules from config.json are
// read-only; rules added through the control API are stored in
// rules.auto.json and applied without a restart.
type Service struct {
	info         Info
	configPath   string
	autoPath     string
	allowRemote  bool
	remoteClient bool
	trusted      *proxycore.TrustList

	mu     sync.Mutex // serializes rule changes
	base   []config.RedirectRule
	auto   []config.RedirectRule
	router atomic.Pointer[redirect.Router]

	recentMu sync.Mutex
	recent   []RequestView
	next     int
}

// New loads the auto rules next to configPath and builds the router from
// the configuration rules followed by the auto rules.
func New(configPath string, cfg config.Config, info Info) (*Service, error) {
	autoPath := config.AutoRulesPath(configPath)
	auto, err := config.LoadAutoRules(autoPath)
	if err != nil {
		return nil, err
	}
	info.ConfigFile, info.AutoRulesFile = configPath, autoPath
	s := &Service{
		info:         info,
		configPath:   configPath,
		autoPath:     autoPath,
		allowRemote:  cfg.AllowRemoteTargets,
		remoteClient: cfg.AllowRemoteClients,
		trusted:      proxycore.NewTrustList(nil, nil),
		base:         slices.Clone(cfg.Rules),
		auto:         auto,
	}
	if err := s.apply(s.base, s.auto); err != nil {
		if len(auto) > 0 {
			return nil, fmt.Errorf("%s: %w", autoPath, err)
		}
		return nil, err
	}
	return s, nil
}

// Router returns the current router.
func (s *Service) Router() *redirect.Router {
	return s.router.Load()
}

// Trusted returns the trust list that follows the current rules.
func (s *Service) Trusted() *proxycore.TrustList {
	return s.trusted
}

// AutoRuleCount returns the number of rules loaded from rules.auto.json.
func (s *Service) AutoRuleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.auto)
}

func (s *Service) apply(base, auto []config.RedirectRule) error {
	all := append(slices.Clone(base), auto...)
	if err := config.ValidateRules(all, s.allowRemote); err != nil {
		return err
	}
	router, err := redirect.New(all)
	if err != nil {
		return err
	}
	s.router.Store(router)
	s.trusted.Set(router.Targets(), router.Domains())
	return nil
}

// RuleView is a rule as reported by the control API.
type RuleView struct {
	Source  string   `json:"source"`
	Target  string   `json:"target"`
	Domains []string `json:"domains"`
	Include []string `json:"include_paths"`
	Exclude []string `json:"exclude_paths"`
}

func view(source string, rule config.RedirectRule) RuleView {
	return RuleView{
		Source:  source,
		Target:  rule.Target,
		Domains: nonNil(rule.Domains),
		Include: nonNil(rule.Include),
		Exclude: nonNil(rule.Exclude),
	}
}

// Rules returns the configuration rules followed by the auto rules.
func (s *Service) Rules() []RuleView {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make([]RuleView, 0, len(s.base)+len(s.auto))
	for _, rule := range s.base {
		views = append(views, view(SourceConfig, rule))
	}
	for _, rule := range s.auto {
		views = append(views, view(SourceAuto, rule))
	}
	return views
}

// AddRule validates rule against the current rules and remote-target
// setting, stores it in rules.auto.json, and applies it.
func (s *Service) AddRule(rule config.RedirectRule) (RuleView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.auto) >= config.MaxAutoRules {
		return RuleView{}, fmt.Errorf("at most %d rules can be added through the control API", config.MaxAutoRules)
	}
	// Validate the new rule on its own first, so that errors do not refer
	// to a position in the combined rule list.
	if err := config.ValidateRule(rule, s.allowRemote); err != nil {
		return RuleView{}, err
	}
	for _, domain := range rule.Domains {
		if indexOfDomain(s.base, domain) >= 0 || indexOfDomain(s.auto, domain) >= 0 {
			return RuleView{}, fmt.Errorf("domain %q is already routed by another rule", domain)
		}
	}
	auto := append(slices.Clone(s.auto), rule)
	if err := config.ValidateRules(append(slices.Clone(s.base), auto...), s.allowRemote); err != nil {
		return RuleView{}, err
	}
	if err := config.WriteAutoRules(s.autoPath, auto); err != nil {
		return RuleView{}, err
	}
	if err := s.apply(s.base, auto); err != nil {
		return RuleView{}, err
	}
	s.auto = auto
	return view(SourceAuto, rule), nil
}

// ErrConfigRule reports an attempt to remove a rule from config.json.
var ErrConfigRule = errors.New("the rule is defined in config.json; edit that file to change it")

// ErrNoRule reports that no rule routes the domain.
var ErrNoRule = errors.New("no rule routes this domain")

// RemoveRule removes the auto rule that routes domain.
func (s *Service) RemoveRule(domain string) (RuleView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if indexOfDomain(s.base, domain) >= 0 {
		return RuleView{}, ErrConfigRule
	}
	index := indexOfDomain(s.auto, domain)
	if index < 0 {
		return RuleView{}, ErrNoRule
	}
	removed := s.auto[index]
	auto := slices.Delete(slices.Clone(s.auto), index, index+1)
	if err := config.WriteAutoRules(s.autoPath, auto); err != nil {
		return RuleView{}, err
	}
	if err := s.apply(s.base, auto); err != nil {
		return RuleView{}, err
	}
	s.auto = auto
	return view(SourceAuto, removed), nil
}

// Merge moves the auto rules into config.json.
func (s *Service) Merge() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged, err := config.MergeAutoRules(s.configPath)
	if err != nil {
		return 0, err
	}
	s.base = append(s.base, s.auto...)
	s.auto = nil
	return merged, nil
}

func indexOfDomain(rules []config.RedirectRule, domain string) int {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	for i, rule := range rules {
		for _, candidate := range rule.Domains {
			if strings.TrimSuffix(strings.ToLower(strings.TrimSpace(candidate)), ".") == domain {
				return i
			}
		}
	}
	return -1
}

// RouteView explains how lemmingd handles a URL.
type RouteView struct {
	URL         string `json:"url"`
	Intercepted bool   `json:"intercepted"`
	Routed      bool   `json:"routed"`
	Target      string `json:"target,omitempty"`
	TargetURL   string `json:"target_url,omitempty"`
	Explanation string `json:"explanation"`
}

// Route reports where a request for rawURL would go.
func (s *Service) Route(rawURL string) (RouteView, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return RouteView{}, errors.New("url must be an absolute http or https URL")
	}
	result := RouteView{URL: rawURL}
	rule, intercepted, routed := s.Router().Lookup(parsed.Host, parsed.Path)
	result.Intercepted, result.Routed = intercepted, routed
	switch {
	case !intercepted:
		result.Explanation = "no rule lists this host; HTTPS is tunneled unchanged and plain HTTP is forwarded to the real server"
	case !routed:
		result.Explanation = "the host is intercepted, but the path matches no include_paths prefix or matches an exclude_paths prefix, so the real server answers"
	default:
		result.Target = rule.Target
		targetURL := url.URL{Scheme: rule.Scheme, Host: rule.Target, Path: parsed.Path, RawPath: parsed.RawPath, RawQuery: parsed.RawQuery}
		result.TargetURL = targetURL.String()
		result.Explanation = "routed to the target with the original path and query; the Host header stays " + parsed.Host
	}
	return result, nil
}

// RequestView is one recently proxied request. Queries are omitted, as in
// the log.
type RequestView struct {
	Time   time.Time `json:"time"`
	Method string    `json:"method"`
	Host   string    `json:"host"`
	Path   string    `json:"path"`
	Target string    `json:"target,omitempty"`
	Status int       `json:"status"`
}

// Record stores a proxied request in the recent-requests ring.
func (s *Service) Record(request RequestView) {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	if len(s.recent) < recentRequests {
		s.recent = append(s.recent, request)
		return
	}
	s.recent[s.next] = request
	s.next = (s.next + 1) % recentRequests
}

// Recent returns up to limit recent requests, newest first.
func (s *Service) Recent(limit int) []RequestView {
	s.recentMu.Lock()
	defer s.recentMu.Unlock()
	ordered := append(slices.Clone(s.recent[s.next:]), s.recent[:s.next]...)
	slices.Reverse(ordered)
	if limit > 0 && limit < len(ordered) {
		ordered = ordered[:limit]
	}
	return ordered
}

// StatusView is the status endpoint response.
type StatusView struct {
	Info
	AllowRemoteTargets bool `json:"allow_remote_targets"`
	AllowRemoteClients bool `json:"allow_remote_clients"`
	ConfigRules        int  `json:"config_rules"`
	AutoRules          int  `json:"auto_rules"`
}

// Status returns the status endpoint response.
func (s *Service) Status() StatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusView{
		Info:               s.info,
		AllowRemoteTargets: s.allowRemote,
		AllowRemoteClients: s.remoteClient,
		ConfigRules:        len(s.base),
		AutoRules:          len(s.auto),
	}
}

// RuleRequest is the body of POST /v1/rules.
type RuleRequest struct {
	Target  string   `json:"target" jsonschema:"where routed requests go: host:port, http://host:port, or https://host:port, for example 127.0.0.1:5173"`
	Domains []string `json:"domains" jsonschema:"exact browser-visible hostnames to intercept, for example app.example.com"`
	Include []string `json:"include_paths" jsonschema:"path prefixes sent to the target, for example / or /assets/"`
	Exclude []string `json:"exclude_paths,omitempty" jsonschema:"path prefixes that stay on the real server even if included, for example /api/"`
}

// RouteRequest is the body of POST /v1/route.
type RouteRequest struct {
	URL string `json:"url"`
}

// MergeView is the response of POST /v1/merge.
type MergeView struct {
	Merged     int    `json:"merged"`
	ConfigFile string `json:"config_file"`
	Backup     string `json:"backup,omitempty"`
}

// Handler serves the control API.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(writer http.ResponseWriter, _ *http.Request) {
		control.WriteJSON(writer, http.StatusOK, s.Status())
	})
	mux.HandleFunc("GET /v1/rules", func(writer http.ResponseWriter, _ *http.Request) {
		control.WriteJSON(writer, http.StatusOK, s.Rules())
	})
	mux.HandleFunc("POST /v1/rules", func(writer http.ResponseWriter, request *http.Request) {
		var body RuleRequest
		if err := control.DecodeJSON(request, &body); err != nil {
			control.WriteError(writer, http.StatusBadRequest, err)
			return
		}
		added, err := s.AddRule(config.RedirectRule(body))
		if err != nil {
			control.WriteError(writer, http.StatusUnprocessableEntity, err)
			return
		}
		control.WriteJSON(writer, http.StatusCreated, added)
	})
	mux.HandleFunc("DELETE /v1/rules", func(writer http.ResponseWriter, request *http.Request) {
		removed, err := s.RemoveRule(request.URL.Query().Get("domain"))
		switch {
		case errors.Is(err, ErrConfigRule):
			control.WriteError(writer, http.StatusConflict, err)
		case errors.Is(err, ErrNoRule):
			control.WriteError(writer, http.StatusNotFound, err)
		case err != nil:
			control.WriteError(writer, http.StatusInternalServerError, err)
		default:
			control.WriteJSON(writer, http.StatusOK, removed)
		}
	})
	mux.HandleFunc("POST /v1/route", func(writer http.ResponseWriter, request *http.Request) {
		var body RouteRequest
		if err := control.DecodeJSON(request, &body); err != nil {
			control.WriteError(writer, http.StatusBadRequest, err)
			return
		}
		route, err := s.Route(body.URL)
		if err != nil {
			control.WriteError(writer, http.StatusBadRequest, err)
			return
		}
		control.WriteJSON(writer, http.StatusOK, route)
	})
	mux.HandleFunc("GET /v1/requests", func(writer http.ResponseWriter, request *http.Request) {
		limit := 50
		if raw := request.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > recentRequests {
				control.WriteError(writer, http.StatusBadRequest, fmt.Errorf("limit must be between 1 and %d", recentRequests))
				return
			}
			limit = parsed
		}
		control.WriteJSON(writer, http.StatusOK, s.Recent(limit))
	})
	mux.HandleFunc("POST /v1/merge", func(writer http.ResponseWriter, _ *http.Request) {
		merged, err := s.Merge()
		if err != nil {
			control.WriteError(writer, http.StatusUnprocessableEntity, err)
			return
		}
		result := MergeView{Merged: merged, ConfigFile: s.configPath}
		if merged > 0 {
			result.Backup = s.configPath + ".bak"
		}
		control.WriteJSON(writer, http.StatusOK, result)
	})
	return mux
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
