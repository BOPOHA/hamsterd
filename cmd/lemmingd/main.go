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
			"static.example1.com",
			"static.as.example1.com",
			"static.eu.example1.com",
			"static.b2b.example1.com",
			"static.stage.b2b.example1.com",
			"static-stage.example1.com",
			"static-qa01.example1.com",
			"static-qa02.example1.com",
			"static-qa03.example1.com",
			"static-qa04.example1.com",
			"static-qa05.example1.com",
			"static-qa06.example1.com",
			"static-qa07.example1.com",
			"static-qa08.example1.com",
			"static-qa09.example1.com",
			"static-qa10.example1.com",
			"static-qa11.example1.com",
			"static-qa12.example1.com",
			"static-qa13.example1.com",
			"static-qa14.example1.com",
			"static-qa15.example1.com",
			"static-qa16.example1.com",
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
			"india.example1.com",
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
			"dev-connect.example1.com",
			"stage-connect.example1.com",
			"prod-connect.example1.com",
			"example2.com",
			"stage.example2.com",
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
