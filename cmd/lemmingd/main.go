package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BOPOHA/hamsterd/internal/buildinfo"
	"github.com/BOPOHA/hamsterd/internal/config"
	"github.com/BOPOHA/hamsterd/internal/control"
	"github.com/BOPOHA/hamsterd/internal/firefox"
	"github.com/BOPOHA/hamsterd/internal/lemming"
	"github.com/BOPOHA/hamsterd/internal/pki"
	"github.com/BOPOHA/hamsterd/internal/proxycore"
	"github.com/elazarl/goproxy"
)

var launchFirefox = firefox.Launch

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "mcp":
			return runMCP(args[1:], stderr)
		case "config":
			return runConfig(args[1:], stdout, stderr)
		}
	}
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
	service, err := lemming.New(*configPath, cfg, lemming.Info{
		Version:       buildinfo.Version,
		Listen:        cfg.Listen,
		ProxyURL:      "http://" + clientAddress(cfg.Listen),
		CACertFile:    *caCertPath,
		CAFingerprint: fingerprint(ca.Certificate.Leaf.Raw),
		CANotAfter:    ca.Certificate.Leaf.NotAfter,
	})
	if err != nil {
		logger.Printf("redirect configuration error: %v", err)
		return 1
	}

	options := proxycore.Options{
		RemoteClients: cfg.AllowRemoteClients,
		ConnectPorts:  cfg.ConnectPorts,
		Trusted:       service.Trusted(),
		ListenAddress: cfg.Listen,
	}
	proxy, _ := proxycore.New(logger, options)
	issuer, err := pki.NewIssuer(ca.Certificate, 1024)
	if err != nil {
		logger.Printf("CA error: %v", err)
		return 1
	}
	mitm := proxycore.MITM(proxy, issuer, logger)
	proxy.OnRequest().HandleConnectFunc(func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if service.Router().Intercepts(host) {
			return mitm, host
		}
		return proxycore.Tunnel, host
	})
	proxy.OnRequest().DoFunc(func(request *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		ctx.UserData = requestRecord{method: request.Method, host: request.URL.Hostname(), path: request.URL.EscapedPath()}
		rewritten, changed := service.Router().Rewrite(request)
		if changed {
			logger.Printf("session=%d route host=%s path=%s target=%s",
				ctx.Session, request.URL.Hostname(), request.URL.EscapedPath(), rewritten.URL.Host)
			ctx.UserData = requestRecord{method: request.Method, host: request.URL.Hostname(), path: request.URL.EscapedPath(), target: rewritten.URL.Host}
		}
		return rewritten, nil
	})
	proxy.OnResponse().DoFunc(func(response *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if record, ok := ctx.UserData.(requestRecord); ok && response != nil {
			service.Record(lemming.RequestView{
				Time: time.Now().UTC(), Method: record.method, Host: record.host, Path: record.path,
				Target: record.target, Status: response.StatusCode,
			})
		}
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
	if ca.ExpiresSoon(time.Now()) {
		logger.Printf("warning: CA expires at %s; before then, remove it from client trust stores, delete %s and %s, restart, and trust the new CA",
			ca.Certificate.Leaf.NotAfter.Format(time.RFC3339), *caCertPath, *caKeyPath)
	}
	if count := service.AutoRuleCount(); count > 0 {
		logger.Printf("loaded %d rules from %s; run \"lemmingd config merge\" to move them into %s", count, config.AutoRulesPath(*configPath), *configPath)
	}
	for _, target := range service.Router().PlaintextRemoteTargets() {
		logger.Printf("warning: target %s is remote plain HTTP; intercepted HTTPS requests, cookies, and credentials cross the network unencrypted", target)
	}
	if cfg.AllowRemoteClients {
		logger.Printf("remote clients allowed without authentication; restrict access with firewall rules")
	}
	if cfg.ControlAPI {
		server, err := control.Start(filepath.Dir(*configPath), "lemmingd", service.Handler(), logger)
		if err != nil {
			logger.Printf("control API error: %v", err)
			return 1
		}
		defer server.Close()
		logger.Printf("control API enabled for lemmingd mcp; endpoint and token in %s", server.Path())
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
	if err := proxycore.ServeAfterListen(ctx, cfg.Listen, proxy, logger, options, onReady); err != nil {
		logger.Printf("server error: %v", err)
		return 1
	}
	return 0
}

type requestRecord struct {
	method, host, path, target string
}

// clientAddress turns a wildcard listen address into one that local clients
// can connect to.
func clientAddress(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
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
