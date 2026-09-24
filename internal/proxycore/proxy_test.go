package proxycore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/BOPOHA/hamsterd/internal/pki"
	"github.com/elazarl/goproxy"
)

func TestNewUsesVerifiedDirectTransport(t *testing.T) {
	proxy, transport := New(log.New(io.Discard, "", 0))
	if transport.Proxy != nil {
		t.Fatal("outbound transport unexpectedly uses environment proxy settings")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("outbound transport does not verify TLS certificates")
	}
	if proxy.Tr != transport {
		t.Fatal("proxy does not use the hardened outbound transport")
	}
}

func TestMITMUsesProvidedCA(t *testing.T) {
	dir := t.TempDir()
	ca, _, err := pki.LoadOrCreate(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"), "test")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "proxied")
	}))
	defer upstream.Close()

	proxy, outbound := New(log.New(io.Discard, "", 0))
	upstreamTransport := upstream.Client().Transport.(*http.Transport)
	outbound.TLSClientConfig = upstreamTransport.TLSClientConfig.Clone()
	mitm := MITM(&ca.Certificate)
	proxy.OnRequest().HandleConnectFunc(func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return mitm, host
	})
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.PEM) {
		t.Fatal("failed to add generated CA to test trust store")
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
	response, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied" {
		t.Fatalf("response body = %q", body)
	}
}

func TestServeStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Serve(ctx, "127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), log.New(os.Stderr, "", 0))
	if err != nil {
		t.Fatal(err)
	}
}
