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
	"path/filepath"
	"syscall"

	"github.com/BOPOHA/hamsterd/internal/buildinfo"
	"github.com/BOPOHA/hamsterd/internal/config"
	"github.com/BOPOHA/hamsterd/internal/firefox"
	"github.com/BOPOHA/hamsterd/internal/pki"
	"github.com/BOPOHA/hamsterd/internal/proxycore"
	"github.com/BOPOHA/hamsterd/internal/redirect"
	"github.com/elazarl/goproxy"
)

var launchFirefox = firefox.Launch

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	firefoxMode := len(args) > 0 && args[0] == "firefox"
	if firefoxMode {
		args = args[1:]
	}
	paths, err := config.DefaultPaths(config.Lemmingd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	flags := flag.NewFlagSet("lemmingd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", paths.Config, "configuration file")
	caCertPath := flags.String("cacert", paths.CACert, "CA certificate file")
	caKeyPath := flags.String("cakey", paths.CAKey, "CA private-key file")
	listen := flags.String("listen", "", "override the configured listen address")
	firefoxBinary := flags.String("firefox-bin", "", "Firefox executable for the firefox command")
	nssCertutil := flags.String("nss-certutil", "", "NSS certutil executable for the firefox command")
	showVersion := flags.Bool("version", false, "print version information")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "lemmingd %s\n", buildinfo.Version)
		return 0
	}
	if firefoxMode && flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: lemmingd firefox [options] URL")
		return 2
	}

	logger := log.New(stderr, "lemmingd: ", log.LstdFlags|log.LUTC)
	cfg, configCreated, err := config.LoadOrCreate(*configPath, config.Lemmingd, "")
	if err != nil {
		logger.Printf("configuration error: %v", err)
		return 1
	}
	if *listen != "" {
		cfg.Listen = *listen
		if err := cfg.Validate(config.Lemmingd); err != nil {
			logger.Printf("configuration error: %v", err)
			return 1
		}
	}
	ca, caCreated, err := pki.LoadOrCreate(*caCertPath, *caKeyPath, "lemmingd")
	if err != nil {
		logger.Printf("CA error: %v", err)
		return 1
	}
	router, err := redirect.New(cfg.Rules)
	if err != nil {
		logger.Printf("redirect configuration error: %v", err)
		return 1
	}

	proxy, _ := proxycore.New(logger)
	mitm := proxycore.MITM(&ca.Certificate)
	proxy.OnRequest().HandleConnectFunc(func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if router.Intercepts(host) {
			return mitm, host
		}
		return proxycore.Tunnel, host
	})
	proxy.OnRequest().DoFunc(func(request *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		rewritten, changed := router.Rewrite(request)
		if changed {
			logger.Printf("session=%d route host=%s path=%s target=%s",
				ctx.Session, request.URL.Hostname(), request.URL.EscapedPath(), rewritten.URL.Host)
		}
		return rewritten, nil
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
	var onReady func() error
	if firefoxMode {
		profile := filepath.Join(filepath.Dir(*configPath), "firefox-profile")
		browserURL := flags.Arg(0)
		onReady = func() error {
			if err := launchFirefox(firefox.Config{
				Profile:       profile,
				CACertificate: *caCertPath,
				ProxyAddress:  cfg.Listen,
				URL:           browserURL,
				Binary:        *firefoxBinary,
				NSSCertutil:   *nssCertutil,
			}); err != nil {
				return fmt.Errorf("prepare Firefox: %w", err)
			}
			logger.Printf("started Firefox with isolated lemmingd profile %s", profile)
			return nil
		}
	}
	if err := proxycore.ServeAfterListen(ctx, cfg.Listen, proxy, logger, onReady); err != nil {
		logger.Printf("server error: %v", err)
		return 1
	}
	return 0
}

func informationHandler(caPEM []byte) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(writer, "lemmingd selective local-development proxy\nCA certificate: /ca.crt\nHealth check: /healthz\n")
	})
	mux.HandleFunc("GET /ca.crt", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/x-x509-ca-cert")
		writer.Header().Set("Content-Disposition", `attachment; filename="lemmingd-ca.crt"`)
		_, _ = writer.Write(caPEM)
	})
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(writer, "ok\n")
	})
	return mux
}
