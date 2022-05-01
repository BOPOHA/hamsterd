package main

import (
	"../internal/sscert"
	"github.com/go-shortcut/httpproxy/v2"
	"log"
	"net/http"
	"strconv"
	"text/template"
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
	log.Printf("Response: [%s] %s %s %v [%v]bytes %s", req.RemoteAddr, req.Method, req.URL.String(), SessionID, resp.ContentLength, cached)

}

var tplIndex = template.Must(template.New("j2Index").Parse(j2Index))

func OnAccept(ctx *httpproxy.Context, w http.ResponseWriter, r *http.Request) bool {
	if r.Method == "GET" && !r.URL.IsAbs() {
		switch r.URL.Path {
		case localRootUrl:
			tplIndex.Execute(w, paramsJ2Index{
				ReqHost: r.Host,
				CaURL:   localCaUrl,
			})
			return true
		case localCaUrl:
			w.Header().Add("Content-Type", "application/x-x509-ca-cert")
			w.Write(sscert.CACert)
			return true
		}

	}
	return false
}
