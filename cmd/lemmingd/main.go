package main

import (
	"fmt"
	"github.com/BOPOHA/hamsterd/internal/sscert"
	"github.com/go-shortcut/httpproxy/v2"
	"log"
	"net/http"
)

type LemmingRule struct {
	socket                string
	hosts                 []string
	doProxyPathStartsWith []string
	noProxyPathStartsWith []string
}

var redirects = []LemmingRule{
	{
		socket: "127.0.0.1:8000",
		hosts: []string{
			"static.happify.com",
			"static.as.happify.com",
			"static.eu.happify.com",
			"static.b2b.happify.com",
			"static.stage.b2b.happify.com",
			"static-stage.happify.com",
			"static-qa01.happify.com",
			"static-qa02.happify.com",
			"static-qa03.happify.com",
			"static-qa04.happify.com",
			"static-qa05.happify.com",
			"static-qa06.happify.com",
			"static-qa07.happify.com",
			"static-qa08.happify.com",
			"static-qa09.happify.com",
			"static-qa10.happify.com",
			"static-qa11.happify.com",
			"static-qa12.happify.com",
			"static-qa13.happify.com",
			"static-qa14.happify.com",
			"static-qa15.happify.com",
			"static-qa16.happify.com",
			"static.happify.localhost",
			"static.happify.local",
		},
		doProxyPathStartsWith: []string{
			"/static/",
		},
	},
	{
		socket: "127.0.0.1:8000",
		hosts: []string{
			"ensemble-stage.happifyhealth.com",
			"ensemble.happifyhealth.com",
			"india.happify.com",
		},
		doProxyPathStartsWith: []string{
			"/static/",
		},
		noProxyPathStartsWith: []string{
			"/static/gen/",
		},
	},
	{
		socket: "127.0.0.1:8001",
		hosts: []string{
			"dev-connect.happify.com",
			"stage-connect.happify.com",
			"prod-connect.happify.com",
			"confidenavigator.com",
			"stage.confidenavigator.com",
		},
		doProxyPathStartsWith: []string{
			"/",
		},
		noProxyPathStartsWith: []string{
			"/api/",
			"/env.js",
		},
	},
}

func main() {

	prx, err := httpproxy.NewProxyCert(sscert.CACert, sscert.CAKey)
	if err != nil {
		log.Fatal(err)
	}
	prx.Rt = GetNewLemmingTransport(redirects)
	prx.OnError = OnError
	prx.OnConnect = OnConnect
	fmt.Println(string(sscert.CACert))
	server := &http.Server{
		Addr:           ":18080",
		Handler:        prx,
		MaxHeaderBytes: 1 << 23, // 8 MB
	}
	server.ListenAndServe()
}

func OnError(ctx *httpproxy.Context, where string,
	err *httpproxy.Error, opErr error) {
	log.Printf("ERR: %s: '%s' %s [%s]", where, ctx.ConnectHost, err, opErr)
}

func OnConnect(ctx *httpproxy.Context, host string) (
	ConnectAction httpproxy.ConnectAction, newHost string) {
	switch rt := (ctx.Prx.Rt).(type) {
	case *LemmingTransport:
		if _, ok := rt.HostMap[host]; ok {
			return httpproxy.ConnectMitm, host
		}
	}
	return httpproxy.ConnectProxy, host
}
