package main

import (
	"flag"
	"github.com/BOPOHA/hamsterd/internal/proxyconfig"
	"github.com/go-shortcut/httpproxy/v2"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

const configDir = ".hamsterd"

var (
	config proxyconfig.ProxyServiceConfig
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

func main() {
	flag.Parse()
	if err := config.Validate(); err != nil {
		log.Fatalf("config validation failed: %s", err.Error())
	}
	if err := config.InitConfigs(); err != nil {
		log.Fatalf("config init failed: %s", err.Error())
	}
	prx, err := httpproxy.NewProxyCert(config.GetCaCert(), config.GetCaKey())
	if err != nil {
		log.Fatalln(err)
	}
	prx.Rt = getTransport()
	prx.OnError = OnError
	prx.OnConnect = OnConnect
	prx.OnResponse = OnResponse
	prx.OnAccept = OnAccept

	server := &http.Server{
		Addr:           config.GetUnitConfig().Socket,
		Handler:        prx,
		MaxHeaderBytes: 1 << 23, // 8 MB
	}
	err = server.ListenAndServe()
	if err != nil {
		log.Fatalln(err)
	}
}
