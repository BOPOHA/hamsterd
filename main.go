package main

import (
	"github.com/gregjones/httpcache"
	"log"
	"net/http"
	"strconv"
)
import "github.com/go-httpproxy/httpproxy"

func main() {
	prx, _ := httpproxy.NewProxyCert(CACert, CAKey)
	prx.Rt = httpcache.NewMemoryCacheTransport()
	prx.OnError = OnError
	prx.OnConnect = OnConnect
	prx.OnResponse = OnResponse

	http.ListenAndServe(":8080", prx)
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
