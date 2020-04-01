package main

import (
	"github.com/BOPOHA/internal-cache-proxy/internal/sscert"
	"github.com/go-httpproxy/httpproxy"
	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
	"log"
	"net/http"
	"os"
	"strconv"
)

func main() {
	prx, _ := httpproxy.NewProxyCert(sscert.CACert, sscert.CAKey)
	prx.Rt = getTransport()
	prx.OnError = OnError
	prx.OnConnect = OnConnect
	prx.OnResponse = OnResponse

	http.ListenAndServe(":8080", prx)
}

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

func OnError(ctx *httpproxy.Context, where string,
	err *httpproxy.Error, opErr error) {
	// Log errors.
	log.Printf("ERR: %s: %s [%s]", where, err, opErr)
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
