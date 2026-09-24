package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BOPOHA/hamsterd/internal/buildinfo"
	cachetransport "github.com/BOPOHA/hamsterd/internal/cache"
	"github.com/BOPOHA/hamsterd/internal/config"
	"github.com/BOPOHA/hamsterd/internal/pki"
	"github.com/BOPOHA/hamsterd/internal/proxycore"
	"github.com/elazarl/goproxy"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	paths, err := config.DefaultPaths(config.Hamsterd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	flags := flag.NewFlagSet("hamsterd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", paths.Config, "configuration file")
	caCertPath := flags.String("cacert", paths.CACert, "CA certificate file")
	caKeyPath := flags.String("cakey", paths.CAKey, "CA private-key file")
	listen := flags.String("listen", "", "override the configured listen address")
	showVersion := flags.Bool("version", false, "print version information")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "hamsterd %s\n", buildinfo.Version)
		return 0
	}

	logger := log.New(stderr, "hamsterd: ", log.LstdFlags|log.LUTC)
	cfg, configCreated, err := config.LoadOrCreate(*configPath, config.Hamsterd, paths.Cache)
	if err != nil {
		logger.Printf("configuration error: %v", err)
		return 1
	}
	if *listen != "" {
		cfg.Listen = *listen
		if err := cfg.Validate(config.Hamsterd); err != nil {
			logger.Printf("configuration error: %v", err)
			return 1
		}
	}
	ca, caCreated, err := pki.LoadOrCreate(*caCertPath, *caKeyPath, "hamsterd")
	if err != nil {
		logger.Printf("CA error: %v", err)
		return 1
	}
	ttl, err := time.ParseDuration(cfg.Cache.DefaultTTL)
	if err != nil {
		logger.Printf("cache TTL error: %v", err)
		return 1
	}

	proxy, outbound := proxycore.New(logger)
	cache, err := cachetransport.NewTransport(outbound, cachetransport.Config{
		Directory:      cfg.Cache.Directory,
		MaxBytes:       cfg.Cache.MaxSizeMiB * 1024 * 1024,
		MaxObjectBytes: cfg.Cache.MaxObjectMiB * 1024 * 1024,
		DefaultTTL:     ttl,
	})
	if err != nil {
		logger.Printf("cache error: %v", err)
		return 1
	}
	mitm := proxycore.MITM(&ca.Certificate)
	proxy.OnRequest().HandleConnectFunc(func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return mitm, host
	})
	proxy.OnRequest().DoFunc(func(request *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		ctx.RoundTripper = proxycore.RoundTripper(cache)
		return request, nil
	})
	proxy.OnResponse().DoFunc(func(response *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if response == nil || ctx.Req == nil {
			return response
		}
		response.Header.Set("X-Hamsterd-Session", fmt.Sprint(ctx.Session))
		logger.Printf("session=%d method=%s host=%s path=%s status=%d cache=%s",
			ctx.Session, ctx.Req.Method, ctx.Req.URL.Hostname(), ctx.Req.URL.EscapedPath(),
			response.StatusCode, response.Header.Get("X-Hamsterd-Cache"))
		return response
	})
	proxy.NonproxyHandler = informationHandler(ca.PEM)

	if configCreated {
		logger.Printf("created configuration %s", *configPath)
	}
	if caCreated {
		logger.Printf("created a unique local CA; certificate: %s; private key: %s", *caCertPath, *caKeyPath)
		logger.Printf("trust the certificate only on clients that intentionally use this proxy")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := proxycore.Serve(ctx, cfg.Listen, proxy, logger); err != nil {
		logger.Printf("server error: %v", err)
		return 1
	}
	return 0
}

func informationHandler(caPEM []byte) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(writer, "hamsterd caching proxy\nCA certificate: /ca.crt\nHealth check: /healthz\n")
	})
	mux.HandleFunc("GET /ca.crt", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/x-x509-ca-cert")
		writer.Header().Set("Content-Disposition", `attachment; filename="hamsterd-ca.crt"`)
		_, _ = writer.Write(caPEM)
	})
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(writer, "ok\n")
	})
	return mux
}
