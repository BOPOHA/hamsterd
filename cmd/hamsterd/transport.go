package main

import (
	"github.com/go-shortcut/httpcache/httpcache"
	"github.com/go-shortcut/httpcache/pkg/diskcache"
	"github.com/go-shortcut/httpcache/pkg/memorycache"
	"net/http"
	"os"
)

func getTransport() *httpcache.Transport {
	var cache httpcache.Cache

	if home, err := os.UserHomeDir(); err == nil {
		cacheDirPath := home + "/tmp/cache/"
		if err = os.MkdirAll(cacheDirPath, os.ModePerm); err == nil {
			cache = diskcache.New(cacheDirPath)
			println("disk cache created: ", cacheDirPath)
		}
	}
	if cache == nil {
		cache = memorycache.NewMemoryCache()
		println("Memory cache created.")
	}

	return httpcache.NewTransportWithOpts(
		cache,
		func(req *http.Request) string {
			return req.Method + " " + req.URL.String() + " " + req.Header.Get("range")
		},
		func(req *http.Request) bool {
			return req.Method == "GET" || req.Method == "HEAD"
		},
	)

}
