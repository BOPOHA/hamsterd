package main

import (
	"log"
	"net/http"
)

type LemmingTransport struct {
	HostMap map[string]string
}

func (t *LemmingTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	if newHost, ok := t.HostMap[req.Host]; ok {
		log.Printf("Got host %s from redirect map. Redirecting %s to %s.", req.Host, req.URL, newHost)
		req.Host = newHost
		req.URL.Host = newHost
		req.URL.Scheme = "http"
	}
	return http.DefaultTransport.RoundTrip(req)

}
func (t *LemmingTransport) AddRules(rules []LemmingRule) {

	for _, v := range rules {
		for _, host := range v.hosts {
			t.HostMap[host] = v.socket
			t.HostMap[host+":443"] = v.socket
		}
	}

}

func GetNewLemmingTransport(rules []LemmingRule) *LemmingTransport {
	rt := LemmingTransport{HostMap: make(map[string]string)}
	rt.AddRules(rules)
	return &rt
}
