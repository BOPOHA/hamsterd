package proxycore

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BOPOHA/hamsterd/internal/pki"
	"github.com/elazarl/goproxy"
)

func discardLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestNewUsesVerifiedDirectTransport(t *testing.T) {
	proxy, transport := New(discardLogger(), Options{})
	if transport.Proxy != nil {
		t.Fatal("outbound transport unexpectedly uses environment proxy settings")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("outbound transport does not verify TLS certificates")
	}
	if transport.MaxResponseHeaderBytes != maxResponseHeaderBytes {
		t.Fatalf("response header limit = %d", transport.MaxResponseHeaderBytes)
	}
	if proxy.Tr != transport {
		t.Fatal("proxy does not use the hardened outbound transport")
	}
}

type mitmFixture struct {
	ca       pki.CA
	proxy    *goproxy.ProxyHttpServer
	outbound *http.Transport
	proxyURL *url.URL
	pool     *x509.CertPool
}

func newMITMFixture(t *testing.T, upstream *httptest.Server) *mitmFixture {
	t.Helper()
	dir := t.TempDir()
	ca, _, err := pki.LoadOrCreate(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"), "test")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := pki.NewIssuer(ca.Certificate, 16)
	if err != nil {
		t.Fatal(err)
	}
	proxy, outbound := New(discardLogger(), Options{})
	if upstream != nil && upstream.TLS != nil {
		outbound.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	}
	mitm := MITM(proxy, issuer, discardLogger())
	proxy.OnRequest().HandleConnectFunc(func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return mitm, host
	})
	proxyServer := httptest.NewServer(proxy)
	t.Cleanup(proxyServer.Close)
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.PEM) {
		t.Fatal("failed to add generated CA to test trust store")
	}
	return &mitmFixture{ca: ca, proxy: proxy, outbound: outbound, proxyURL: proxyURL, pool: pool}
}

func (fixture *mitmFixture) client() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(fixture.proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: fixture.pool},
	}}
}

// tunnel opens a CONNECT tunnel through the proxy and returns the raw
// connection after the 200 response.
func (fixture *mitmFixture) tunnel(t *testing.T, target string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", fixture.proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d", response.StatusCode)
	}
	if reader.Buffered() != 0 {
		t.Fatal("unexpected bytes after CONNECT response")
	}
	return conn
}

func TestMITMUsesIssuerCertificates(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "proxied "+request.URL.Path)
	}))
	defer upstream.Close()
	fixture := newMITMFixture(t, upstream)

	response, err := fixture.client().Get(upstream.URL + "/path")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied /path" {
		t.Fatalf("response body = %q", body)
	}
	leaf := response.TLS.PeerCertificates[0]
	if leaf.NotAfter.Sub(leaf.NotBefore) > pki.LeafValidity+2*time.Hour {
		t.Fatalf("leaf validity = %v", leaf.NotAfter.Sub(leaf.NotBefore))
	}
	if response.TLS.NegotiatedProtocol == "h2" {
		t.Fatal("intercepted client negotiated HTTP/2")
	}
}

func TestMITMRejectsMismatchedHostAndNestedConnect(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("request reached upstream")
	}))
	defer upstream.Close()
	fixture := newMITMFixture(t, upstream)
	host := strings.TrimPrefix(upstream.URL, "https://")

	for _, test := range []struct {
		request string
		status  int
	}{
		{request: "GET / HTTP/1.1\r\nHost: other.example\r\n\r\n", status: http.StatusMisdirectedRequest},
		{request: "CONNECT other.example:443 HTTP/1.1\r\nHost: other.example:443\r\n\r\n", status: http.StatusMethodNotAllowed},
	} {
		conn := fixture.tunnel(t, host)
		tlsConn := tls.Client(conn, &tls.Config{RootCAs: fixture.pool, ServerName: "127.0.0.1"})
		if _, err := io.WriteString(tlsConn, test.request); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
		}
	}
}

func TestMITMLimitsRequestHeaders(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request reached upstream")
	}))
	defer upstream.Close()
	fixture := newMITMFixture(t, upstream)
	conn := fixture.tunnel(t, strings.TrimPrefix(upstream.URL, "https://"))
	tlsConn := tls.Client(conn, &tls.Config{RootCAs: fixture.pool, ServerName: "127.0.0.1"})
	request := "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Large: " + strings.Repeat("x", 2*maxHeaderBytes) + "\r\n\r\n"
	go func() { _, _ = io.WriteString(tlsConn, request) }()
	response, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestMITMServesPlainHTTPInsideTunnel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "plain "+request.Host)
	}))
	defer upstream.Close()
	fixture := newMITMFixture(t, upstream)
	host := strings.TrimPrefix(upstream.URL, "http://")
	conn := fixture.tunnel(t, host)
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "plain "+host {
		t.Fatalf("body = %q", body)
	}
}

func TestMITMProxiesWebSocketUpgrade(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Upgrade") != "websocket" {
			http.Error(writer, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		conn, buffered, err := http.NewResponseController(writer).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		line, err := buffered.ReadString('\n')
		if err == nil {
			_, _ = io.WriteString(conn, "echo "+line)
		}
	}))
	defer upstream.Close()
	fixture := newMITMFixture(t, upstream)
	conn := fixture.tunnel(t, strings.TrimPrefix(upstream.URL, "https://"))
	tlsConn := tls.Client(conn, &tls.Config{RootCAs: fixture.pool, ServerName: "127.0.0.1"})
	_, _ = io.WriteString(tlsConn, "GET /ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	reader := bufio.NewReader(tlsConn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d", response.StatusCode)
	}
	_ = tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(tlsConn, "ping\n")
	line, err := reader.ReadString('\n')
	if err != nil || line != "echo ping\n" {
		t.Fatalf("frame = %q, %v", line, err)
	}
}

func TestRemoteClientsCannotReachLoopbackDestinations(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "local service")
	}))
	defer upstream.Close()
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")

	tests := []struct {
		name    string
		options Options
		status  int
	}{
		{name: "loopback clients", options: Options{}, status: http.StatusOK},
		{name: "remote clients", options: Options{RemoteClients: true}, status: http.StatusForbidden},
		{name: "trusted target", options: Options{RemoteClients: true, TrustedTargets: []string{upstreamHost}}, status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxy, _ := New(discardLogger(), test.options)
			proxyServer := httptest.NewServer(proxy)
			defer proxyServer.Close()
			proxyURL, _ := url.Parse(proxyServer.URL)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
			response, err := client.Get(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, body = %q", response.StatusCode, body)
			}
			if test.status != http.StatusOK && strings.Contains(string(body), "127.0.0.1") {
				t.Fatalf("error body leaks the destination: %q", body)
			}
		})
	}
}

func TestRemoteClientsConnectPolicy(t *testing.T) {
	proxy, _ := New(discardLogger(), Options{RemoteClients: true, ConnectPorts: []int{443, 8443}})
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()
	connect := func(target string) int {
		conn, err := net.Dial("tcp", strings.TrimPrefix(proxyServer.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if status := connect("192.0.2.1:22"); status != http.StatusForbidden {
		t.Fatalf("CONNECT to port 22 status = %d", status)
	}
	if status := connect("127.0.0.1:443"); status != http.StatusForbidden {
		t.Fatalf("CONNECT to loopback status = %d", status)
	}
	if status := connect("169.254.169.254:8443"); status != http.StatusForbidden {
		t.Fatalf("CONNECT to metadata status = %d", status)
	}
}

func TestUpstreamErrorsAreNotEchoed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddress := listener.Addr().String()
	listener.Close()
	var logs strings.Builder
	proxy, _ := New(log.New(&logs, "", 0), Options{})
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()
	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get("http://" + closedAddress + "/secret?token=x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || string(body) != "upstream request failed\n" {
		t.Fatalf("response = %d %q", response.StatusCode, body)
	}
	if strings.Contains(logs.String(), "token=x") {
		t.Fatalf("log contains the query string: %q", logs.String())
	}
}

func TestQuietErrorLogFiltersDisconnects(t *testing.T) {
	var output strings.Builder
	logger := quietErrorLog(log.New(&output, "", 0))
	logger.Printf("http: TLS handshake error from 127.0.0.1:1: EOF")
	logger.Printf("http: TLS handshake error from 127.0.0.1:2: read tcp: connection reset by peer")
	logger.Printf("http: TLS handshake error from 127.0.0.1:3: remote error: tls: unknown certificate authority")
	if got := output.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "unknown certificate authority") {
		t.Fatalf("log = %q", got)
	}
}

func TestWriteConnectErrorAnswersOnlyDialFailures(t *testing.T) {
	ctx := &goproxy.ProxyCtx{Proxy: goproxy.NewProxyHttpServer()}
	ctx.Proxy.Logger = discardLogger()
	var output strings.Builder
	writeConnectError(&output, ctx, &net.OpError{Op: "read", Err: errors.New("reset")})
	if output.Len() != 0 {
		t.Fatalf("established tunnel received %q", output.String())
	}
	writeConnectError(&output, ctx, &net.OpError{Op: "dial", Err: errors.New("refused")})
	if !strings.HasPrefix(output.String(), "HTTP/1.1 502 ") || strings.Contains(output.String(), "refused") {
		t.Fatalf("dial failure response = %q", output.String())
	}
	output.Reset()
	writeConnectError(&output, ctx, ErrDestinationDenied)
	if !strings.HasPrefix(output.String(), "HTTP/1.1 403 ") {
		t.Fatalf("denied response = %q", output.String())
	}
}

func TestRemoveConnectionHeaders(t *testing.T) {
	header := http.Header{
		"Connection":    {"keep-alive, X-Hop, Upgrade"},
		"X-Hop":         {"secret"},
		"Keep-Alive":    {"timeout=5"},
		"Upgrade":       {"websocket"},
		"X-End-To-End":  {"kept"},
		"Authorization": {"kept"},
	}
	removeConnectionHeaders(header)
	for _, name := range []string{"X-Hop", "Keep-Alive"} {
		if header.Get(name) != "" {
			t.Fatalf("%s was forwarded", name)
		}
	}
	for _, name := range []string{"Upgrade", "X-End-To-End", "Authorization", "Connection"} {
		if header.Get(name) == "" {
			t.Fatalf("%s was removed", name)
		}
	}
}

func TestDeniedDestination(t *testing.T) {
	for address, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "0.0.0.0": true, "0.1.2.3": true, "169.254.169.254": true,
		"fe80::1": true, "fd00:ec2::254": true, "100.100.100.200": true, "224.0.0.1": true,
		"255.255.255.255": true, "::ffff:127.0.0.1": true,
		"10.0.0.5": false, "192.168.1.10": false, "93.184.216.34": false, "2001:db8::1": false,
	} {
		if got := deniedDestination(net.ParseIP(address)); got != want {
			t.Fatalf("deniedDestination(%s) = %v, want %v", address, got, want)
		}
	}
}

func TestRemoteClientsAreRefusedBeforeHandlers(t *testing.T) {
	proxy, _ := New(discardLogger(), Options{RemoteClients: true, TrustedTargets: []string{"localhost:5173"}, TrustedHosts: []string{"App.Localhost."}})
	var reached atomic.Int32
	proxy.OnRequest().DoFunc(func(request *http.Request, _ *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		reached.Add(1)
		// Stands in for a cache hit that would not dial.
		return request, goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusOK, "cached")
	})
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()
	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	for rawURL, want := range map[string]int{
		"http://127.0.0.1:8000/x":    http.StatusForbidden,
		"http://localhost:8000/x":    http.StatusForbidden,
		"http://other.localhost/x":   http.StatusForbidden,
		"http://app.localhost/x":     http.StatusOK,
		"http://[::1]:8000/x":        http.StatusForbidden,
		"http://169.254.169.254/x":   http.StatusForbidden,
		"http://localhost:5173/x":    http.StatusOK,
		"http://downloads.example/x": http.StatusOK,
	} {
		response, err := client.Get(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s status = %d, want %d", rawURL, response.StatusCode, want)
		}
	}
	if reached.Load() != 3 {
		t.Fatalf("later handlers ran %d times, want 3", reached.Load())
	}
}

func TestDestinationGuardRefusesProxySelfAddress(t *testing.T) {
	guard := newDestinationGuard(Options{RemoteClients: true, ListenAddress: "192.0.2.10:8080"})
	if _, err := guard.DialContext(context.Background(), "tcp", "192.0.2.10:8080"); !errors.Is(err, ErrDestinationDenied) {
		t.Fatalf("error = %v", err)
	}
}

func TestTrackedConnIdleTimeout(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	conn := &trackedConn{Conn: server, idle: 50 * time.Millisecond, release: func() {}}
	defer conn.Close()

	// Without enableIdle, no deadline applies.
	readDone := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		readDone <- err
	}()
	select {
	case err := <-readDone:
		t.Fatalf("read returned before idle mode: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	_, _ = client.Write([]byte("x"))
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}

	conn.enableIdle()
	start := time.Now()
	_, err := conn.Read(make([]byte, 1))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("idle read error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("idle timeout took %v", elapsed)
	}

	// An earlier caller deadline still applies in idle mode.
	conn.idle = time.Hour
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := conn.Read(make([]byte, 1)); !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("caller deadline error = %v", err)
	}
}

func TestLimitListenerCapsConnections(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newLimitListener(raw, 1, 0)
	defer listener.Close()
	for range 2 {
		conn, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	}
	first, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Bool
	secondDone := make(chan net.Conn, 1)
	go func() {
		conn, _ := listener.Accept()
		accepted.Store(true)
		secondDone <- conn
	}()
	time.Sleep(50 * time.Millisecond)
	if accepted.Load() {
		t.Fatal("second connection accepted above the limit")
	}
	first.Close()
	first.Close()
	select {
	case conn := <-secondDone:
		if conn == nil {
			t.Fatal("second accept failed")
		}
		conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("slot was not released")
	}
}

func TestServeStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Serve(ctx, "127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), log.New(os.Stderr, "", 0), Options{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServeAfterListenRunsCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	err := ServeAfterListen(ctx, "127.0.0.1:0", http.NotFoundHandler(), discardLogger(), Options{}, func() error {
		called = true
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("ready callback was not called")
	}
}

func TestServeAfterListenReturnsCallbackError(t *testing.T) {
	want := errors.New("client launch failed")
	err := ServeAfterListen(context.Background(), "127.0.0.1:0", http.NotFoundHandler(), discardLogger(), Options{}, func() error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want callback error", err)
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
	err := Serve(context.Background(), "not an address", http.NotFoundHandler(), discardLogger(), Options{})
	if err == nil {
		t.Fatal("expected invalid listen address to fail")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
