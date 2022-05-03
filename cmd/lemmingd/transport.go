package main

import (
	"log"
	"net/http"
	"strings"
)

type LemmingTransport struct {
	HostMap map[string]LemmingHostRule
}
type LemmingHostRule struct {
	Socket            string
	StartsWithFilter  []string
	StartsWithExclude []string
}

func (lhr *LemmingHostRule) CheckPathStartsWith(urlPath string) bool {
	for _, path := range lhr.StartsWithExclude {
		if strings.HasPrefix(urlPath, path) {
			log.Printf("DEBUG: matched rule: no_proxy '%s' %s", urlPath, lhr.StartsWithExclude)
			return false
		}
	}
	for _, path := range lhr.StartsWithFilter {
		if strings.HasPrefix(urlPath, path) {
			log.Printf("DEBUG: matched rule: '%s' %s", urlPath, lhr.StartsWithFilter)
			return true
		}
	}
	return false
}

func (t *LemmingTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	if newHost, ok := t.HostMap[req.Host]; ok {

		if newHost.CheckPathStartsWith(req.URL.Path) == true {
			log.Printf("Got host %s from redirect map. Redirecting %s to %v.", req.Host, req.URL, newHost.Socket)
			req.Host = newHost.Socket
			req.URL.Host = newHost.Socket
			req.URL.Scheme = "http"
		}

	}
	return http.DefaultTransport.RoundTrip(req)

}
func (t *LemmingTransport) AddRules(rules []LemmingRule) {

	for _, v := range rules {
		for _, host := range v.Domains {
			t.HostMap[host] = LemmingHostRule{v.LocalSocket, v.DoProxyPathStartsWith, v.NoProxyPathStartsWith}
			t.HostMap[host+":443"] = t.HostMap[host]

		}
	}

}

func GetNewLemmingTransport(rules []LemmingRule) *LemmingTransport {
	rt := LemmingTransport{
		HostMap: make(map[string]LemmingHostRule),
	}
	rt.AddRules(rules)
	return &rt
}
