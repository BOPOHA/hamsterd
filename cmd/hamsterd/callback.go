package main

import (
	"fmt"
	"github.com/BOPOHA/hamsterd/internal/sscert"
	"github.com/go-httpproxy/httpproxy"
	"log"
	"net/http"
	"strconv"
)

func OnError(ctx *httpproxy.Context, where string,
	err *httpproxy.Error, opErr error) {
	log.Printf("ERR: %s: '%s' %s [%s]", where, ctx.ConnectHost, err, opErr)
}

func OnConnect(ctx *httpproxy.Context, host string) (
	ConnectAction httpproxy.ConnectAction, newHost string) {
	return httpproxy.ConnectMitm, host
}

func OnResponse(ctx *httpproxy.Context, req *http.Request, resp *http.Response) {
	SessionID := strconv.FormatInt(ctx.Prx.SessionNo, 10)
	resp.Header.Set("x-session-no", SessionID)
	cached := "direct"
	if len(resp.Header.Get("X-From-Cache")) > 0 {
		cached = "cached"
	}
	log.Printf("Responce: %s %s %v %s", req.Method, req.URL.String(), SessionID, cached)

}

func OnAccept(ctx *httpproxy.Context, w http.ResponseWriter, r *http.Request) bool {
	if r.Method == "GET" && !r.URL.IsAbs() {
		switch r.URL.Path {
		case localRootUrl:
			fmt.Fprintf(w,
				"#!/bin/bash +x\n"+
					"if [ -d /etc/pki/ca-trust/source/anchors/ ]; then\n"+
					"curl -s %s%s -o /etc/pki/ca-trust/source/anchors/proxy.dev.crt\n"+
					"update-ca-trust\n"+
					"grep -q ^proxy= /etc/dnf/dnf.conf || echo proxy=http://%s >> /etc/dnf/dnf.conf\n"+
					"fi\n"+
					"echo Done\n"+
					"\n\n",
				r.Host, localCaUrl, r.Host)
			return true
		case localCaUrl:
			w.Header().Add("Content-Type", "application/x-x509-ca-cert")
			w.Write(sscert.CACert)
			return true
		}

	}
	return false
}
