package redirect

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/BOPOHA/hamsterd/internal/config"
)

type Rule struct {
	Scheme  string
	Target  string
	Include []string
	Exclude []string
}

type Router struct {
	hosts map[string]Rule
}

func New(rules []config.RedirectRule) (*Router, error) {
	router := &Router{hosts: make(map[string]Rule)}
	for _, source := range rules {
		target, err := config.ParseTarget(source.Target)
		if err != nil {
			return nil, err
		}
		rule := Rule{Scheme: target.Scheme, Target: target.Address, Include: source.Include, Exclude: source.Exclude}
		for _, domain := range source.Domains {
			host := normalizeHost(domain)
			if _, exists := router.hosts[host]; exists {
				return nil, fmt.Errorf("duplicate redirect domain %q", domain)
			}
			router.hosts[host] = rule
		}
	}
	return router, nil
}

// Domains returns the configured browser-visible domains.
func (r *Router) Domains() []string {
	domains := make([]string, 0, len(r.hosts))
	for host := range r.hosts {
		domains = append(domains, host)
	}
	slices.Sort(domains)
	return domains
}

// Targets returns the configured target addresses.
func (r *Router) Targets() []string {
	var targets []string
	for _, rule := range r.hosts {
		if !slices.Contains(targets, rule.Target) {
			targets = append(targets, rule.Target)
		}
	}
	slices.Sort(targets)
	return targets
}

// PlaintextRemoteTargets returns non-loopback targets reached over HTTP.
func (r *Router) PlaintextRemoteTargets() []string {
	var targets []string
	for _, rule := range r.hosts {
		target := config.Target{Scheme: rule.Scheme, Address: rule.Target}
		if rule.Scheme == "http" && !target.Loopback() && !slices.Contains(targets, rule.Target) {
			targets = append(targets, rule.Target)
		}
	}
	slices.Sort(targets)
	return targets
}

// Lookup reports how a request for hostPort and path is handled: whether
// the host is intercepted, and whether the path is routed to rule's target.
func (r *Router) Lookup(hostPort, path string) (rule Rule, intercepted, routed bool) {
	rule, intercepted = r.hosts[normalizeHost(hostPort)]
	if !intercepted {
		return Rule{}, false, false
	}
	return rule, true, matches(path, rule)
}

func (r *Router) Intercepts(hostPort string) bool {
	_, ok := r.hosts[normalizeHost(hostPort)]
	return ok
}

func (r *Router) Rewrite(request *http.Request) (*http.Request, bool) {
	rule, ok := r.hosts[normalizeHost(request.URL.Host)]
	if !ok {
		rule, ok = r.hosts[normalizeHost(request.Host)]
	}
	if !ok || !matches(request.URL.Path, rule) {
		return request, false
	}
	clone := request.Clone(request.Context())
	urlCopy := *request.URL
	clone.URL = &urlCopy
	clone.URL.Scheme = rule.Scheme
	clone.URL.Host = rule.Target
	// URL.Host controls where the transport connects. Host controls the HTTP
	// Host header, which must remain the browser-visible domain so a local
	// target can select the intended virtual host.
	if clone.Host == "" {
		clone.Host = request.URL.Host
	}
	return clone, true
}

func matches(path string, rule Rule) bool {
	for _, prefix := range rule.Exclude {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	for _, prefix := range rule.Include {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func normalizeHost(hostPort string) string {
	hostPort = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostPort)), ".")
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return strings.TrimSuffix(strings.ToLower(host), ".")
	}
	return hostPort
}
