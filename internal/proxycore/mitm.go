package proxycore

import (
	"bufio"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BOPOHA/hamsterd/internal/pki"
	"github.com/elazarl/goproxy"
)

const (
	headerTimeout  = 10 * time.Second
	idleTimeout    = 2 * time.Minute
	maxHeaderBytes = 1 << 20
)

// MITM returns a CONNECT action that terminates the client's TLS session
// with a certificate from issuer and serves the decrypted requests through
// handler. Requests are read by a net/http server, so the header size and
// timeout limits of the outer listener also apply inside the tunnel. Each
// request is sent to the CONNECT host; a different Host header is refused.
func MITM(handler http.Handler, issuer *pki.Issuer, logger *log.Logger) *goproxy.ConnectAction {
	return &goproxy.ConnectAction{
		Action: goproxy.ConnectHijack,
		Hijack: func(request *http.Request, client net.Conn, _ *goproxy.ProxyCtx) {
			serveMITM(handler, issuer, logger, request.URL.Host, client)
		},
	}
}

func serveMITM(handler http.Handler, issuer *pki.Issuer, logger *log.Logger, connectHost string, client net.Conn) {
	hijacked := false
	defer func() {
		if !hijacked {
			_ = client.Close()
		}
	}()
	host, port, err := net.SplitHostPort(connectHost)
	if err != nil {
		host, port = connectHost, "443"
	}
	host = normalizeHostname(host)
	if host == "" {
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Now().Add(headerTimeout))
	reader := bufio.NewReader(client)
	first, err := reader.Peek(1)
	_ = client.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}

	var conn net.Conn = &bufferedConn{Conn: client, reader: reader}
	scheme, defaultPort := "http", "80"
	if first[0] == 0x16 { // TLS handshake record
		scheme, defaultPort = "https", "443"
		conn = tls.Server(conn, &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"},
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return issuer.Certificate(host)
			},
		})
	}
	authority := host
	if port != defaultPort {
		authority = net.JoinHostPort(host, port)
	}

	listener := newSingleConnListener(conn)
	var once sync.Once
	server := &http.Server{
		Handler:           mitmHandler(handler, scheme, authority),
		ReadHeaderTimeout: headerTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          quietErrorLog(logger),
		// HTTP/2 is not offered to intercepted clients.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ConnState: func(_ net.Conn, state http.ConnState) {
			switch state {
			case http.StateHijacked:
				// WebSocket upgrades take ownership of the connection.
				hijacked = true
				once.Do(func() { _ = listener.Close() })
			case http.StateClosed:
				once.Do(func() { _ = listener.Close() })
			}
		},
	}
	_ = server.Serve(listener)
}

// quietErrorLog drops net/http messages caused by clients that close
// speculative or abandoned connections, and keeps actionable ones such as a
// client that does not trust the CA.
func quietErrorLog(logger *log.Logger) *log.Logger {
	return log.New(errorLogFilter{logger: logger}, "", 0)
}

type errorLogFilter struct {
	logger *log.Logger
}

func (filter errorLogFilter) Write(message []byte) (int, error) {
	text := strings.TrimSpace(string(message))
	for _, noise := range []string{": EOF", "connection reset by peer", "broken pipe", "i/o timeout"} {
		if strings.HasSuffix(text, noise) {
			return len(message), nil
		}
	}
	filter.logger.Print(text)
	return len(message), nil
}

func mitmHandler(handler http.Handler, scheme, authority string) http.Handler {
	expected := normalizeHostname(hostOnly(authority))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodConnect {
			http.Error(writer, "CONNECT is not allowed inside an intercepted tunnel", http.StatusMethodNotAllowed)
			return
		}
		if request.Host != "" && normalizeHostname(hostOnly(request.Host)) != expected {
			http.Error(writer, "request host does not match the CONNECT host", http.StatusMisdirectedRequest)
			return
		}
		request.URL.Scheme = scheme
		request.URL.Host = authority
		handler.ServeHTTP(writer, request)
	})
}

func hostOnly(hostPort string) string {
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return host
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostPort, "["), "]")
}

func normalizeHostname(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (conn *bufferedConn) Read(buffer []byte) (int, error) {
	return conn.reader.Read(buffer)
}

// singleConnListener serves exactly one connection and then blocks Accept
// until it is closed.
type singleConnListener struct {
	mu     sync.Mutex
	conn   net.Conn
	addr   net.Addr
	closed chan struct{}
	once   sync.Once
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{conn: conn, addr: conn.LocalAddr(), closed: make(chan struct{})}
}

func (listener *singleConnListener) Accept() (net.Conn, error) {
	listener.mu.Lock()
	conn := listener.conn
	listener.conn = nil
	listener.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *singleConnListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *singleConnListener) Addr() net.Addr {
	return listener.addr
}
