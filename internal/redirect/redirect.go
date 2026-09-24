package redirect

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/BOPOHA/hamsterd/internal/config"
)

type Rule struct {
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
		rule := Rule{Target: source.Target, Include: source.Include, Exclude: source.Exclude}
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
	clone.URL.Scheme = "http"
	clone.URL.Host = rule.Target
	clone.Host = rule.Target
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
