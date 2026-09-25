package proxycore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
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

func TestRoundTripperDelegates(t *testing.T) {
	var calls atomic.Int32
	wantResponse := &http.Response{StatusCode: http.StatusNoContent}
	wantError := errors.New("round trip error")
	adapter := RoundTripper(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.String() != "https://example.test/path" {
			t.Fatalf("URL = %q", request.URL)
		}
		return wantResponse, wantError
	}))
	request, _ := http.NewRequest(http.MethodGet, "https://example.test/path", nil)
	gotResponse, gotError := adapter.RoundTrip(request, &goproxy.ProxyCtx{})
	if gotResponse != wantResponse || !errors.Is(gotError, wantError) || calls.Load() != 1 {
		t.Fatalf("result = (%p, %v), calls = %d", gotResponse, gotError, calls.Load())
	}
}

func TestServeRejectsInvalidAddress(t *testing.T) {
	err := Serve(context.Background(), "not an address", http.NotFoundHandler(), log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatal("expected invalid listen address to fail")
	}
}

func TestCertificateCacheReusesAndEvicts(t *testing.T) {
	cache := newCertificateCache(1)
	first := &tls.Certificate{}
	second := &tls.Certificate{}
	var calls atomic.Int32
	generateFirst := func() (*tls.Certificate, error) {
		calls.Add(1)
		return first, nil
	}

	got, err := cache.Fetch("one.example", generateFirst)
	if err != nil || got != first {
		t.Fatalf("first fetch = (%p, %v)", got, err)
	}
	got, err = cache.Fetch("one.example", func() (*tls.Certificate, error) {
		t.Fatal("cached certificate regenerated")
		return nil, nil
	})
	if err != nil || got != first {
		t.Fatalf("cached fetch = (%p, %v)", got, err)
	}
	got, err = cache.Fetch("two.example", func() (*tls.Certificate, error) {
		calls.Add(1)
		return second, nil
	})
	if err != nil || got != second {
		t.Fatalf("second fetch = (%p, %v)", got, err)
	}
	got, err = cache.Fetch("one.example", generateFirst)
	if err != nil || got != first || calls.Load() != 3 {
		t.Fatalf("evicted fetch = (%p, %v), calls = %d", got, err, calls.Load())
	}
}

func TestCertificateCacheDoesNotStoreGenerationError(t *testing.T) {
	cache := newCertificateCache(1)
	want := errors.New("generation failed")
	if got, err := cache.Fetch("bad.example", func() (*tls.Certificate, error) {
		return nil, want
	}); got != nil || !errors.Is(err, want) {
		t.Fatalf("fetch = (%p, %v)", got, err)
	}
	certificate := &tls.Certificate{}
	if got, err := cache.Fetch("bad.example", func() (*tls.Certificate, error) {
		return certificate, nil
	}); got != certificate || err != nil {
		t.Fatalf("retry = (%p, %v)", got, err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
