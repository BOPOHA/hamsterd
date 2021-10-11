package main

import (
	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
	"os"
)

func getTransport() *httpcache.Transport {
	var cache httpcache.Cache

	cache = httpcache.NewMemoryCache()

	if home, err := os.UserHomeDir(); err == nil {
		cacheDirPath := home + "/tmp/cache/"
		if err = os.MkdirAll(cacheDirPath, os.ModePerm); err == nil {
			cache = diskcache.New(cacheDirPath)
			println("disk cache created: ", cacheDirPath)
		}
	}

	return httpcache.NewTransport(cache)
}
