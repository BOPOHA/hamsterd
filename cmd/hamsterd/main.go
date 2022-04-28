package main

import (
	"github.com/BOPOHA/hamsterd/internal/sscert"
	"github.com/go-shortcut/httpproxy/v2"
	"log"
	"net/http"
)

func main() {
	prx, err := httpproxy.NewProxyCert(sscert.CACert, sscert.CAKey)
	if err != nil {
		log.Fatalln(err)
	}
	prx.Rt = getTransport()
	prx.OnError = OnError
	prx.OnConnect = OnConnect
	prx.OnResponse = OnResponse
	prx.OnAccept = OnAccept

	server := &http.Server{
		Addr:           ":8080",
		Handler:        prx,
		MaxHeaderBytes: 1 << 23, // 8 MB
	}
	err = server.ListenAndServe()
	if err != nil {
		log.Fatalln(err)
	}
}
