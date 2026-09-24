package proxycore

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
)

var Tunnel = &goproxy.ConnectAction{Action: goproxy.ConnectAccept}

func New(logger *log.Logger) (*goproxy.ProxyHttpServer, *http.Transport) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		MaxConnsPerHost:       32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr = transport
	proxy.ConnectDial = dialer.Dial
	proxy.Logger = logger
	proxy.Verbose = false
	proxy.CertStore = newCertificateCache(1024)
	return proxy, transport
}

func MITM(ca *tls.Certificate) *goproxy.ConnectAction {
	return &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(ca),
	}
}

func RoundTripper(transport http.RoundTripper) goproxy.RoundTripper {
	return goproxy.RoundTripperFunc(func(request *http.Request, _ *goproxy.ProxyCtx) (*http.Response, error) {
		return transport.RoundTrip(request)
	})
}

func Serve(ctx context.Context, address string, handler http.Handler, logger *log.Logger) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	result := make(chan error, 1)
	go func() {
		result <- server.Serve(listener)
	}()
	logger.Printf("listening on http://%s", listener.Addr())
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type certificateCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]*tls.Certificate
	order   []string
}

func newCertificateCache(maxEntries int) *certificateCache {
	return &certificateCache{max: maxEntries, entries: make(map[string]*tls.Certificate)}
}

func (cache *certificateCache) Fetch(hostname string, generate func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if certificate := cache.entries[hostname]; certificate != nil {
		return certificate, nil
	}
	certificate, err := generate()
	if err != nil {
		return nil, err
	}
	if len(cache.order) >= cache.max {
		delete(cache.entries, cache.order[0])
		cache.order = cache.order[1:]
	}
	cache.entries[hostname] = certificate
	cache.order = append(cache.order, hostname)
	return certificate, nil
}
