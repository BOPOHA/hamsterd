package main

import (
	"encoding/json"
	"flag"
	"github.com/BOPOHA/hamsterd/internal/proxyconfig"
	"github.com/go-shortcut/httpproxy/v2"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

const configDir = ".lemmingd"

var (
	config = proxyconfig.ProxyServiceConfig{DefConfigFN: "config.lemmingd.json"}
)

func init() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	defaultConfigJson := filepath.Join(homeDir, configDir, "config.json")
	defaultCaCrt := filepath.Join(homeDir, configDir, "ca.crt")
	defaultCaKey := filepath.Join(homeDir, configDir, "ca.key")
	flag.StringVar(&config.PathConfig, "config", defaultConfigJson, "config path. default "+defaultConfigJson)
	flag.StringVar(&config.PathCaCert, "cacert", defaultCaCrt, "CA crt path. default "+defaultCaCrt)
	flag.StringVar(&config.PathCaKey, "cakey", defaultCaKey, "CA key path. default "+defaultCaKey)
}

type LemmingRule struct {
	LocalSocket           string   `json:"LocalSocket"`
	Domains               []string `json:"Domains"`
	DoProxyPathStartsWith []string `json:"StartWithWL"`
	NoProxyPathStartsWith []string `json:"StartWithBL"`
}

var redirects []LemmingRule

func main() {

	flag.Parse()
	if err := config.Validate(); err != nil {
		log.Fatalf("config validation failed: %s", err.Error())
	}
	if err := config.InitConfigs(); err != nil {
		log.Fatalf("config init failed: %s", err.Error())
	}
	// reading custom config
	err := json.Unmarshal(config.GetUnitConfig().CustomConfig, &redirects)
	if err != nil {
		log.Fatalln("Failed to unmarshal CustomConfig")
	}
	//log.Println(redirects)

	prx, err := httpproxy.NewProxyCert(config.GetCaCert(), config.GetCaKey())
	if err != nil {
		log.Fatalln(err)
	}
	prx.MitmChunked = false
	prx.Rt = GetNewLemmingTransport(redirects)
	prx.OnError = OnError
	prx.OnConnect = OnConnect
	server := &http.Server{
		Addr:           config.GetUnitConfig().Socket,
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
