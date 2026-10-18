package proxycore

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/elazarl/goproxy"
)

const (
	// maxResponseHeaderBytes bounds upstream response headers, which hamsterd
	// may also persist as cache metadata.
	maxResponseHeaderBytes = 256 << 10
	// DefaultConnectPort is the only CONNECT port remote clients may use
	// unless the configuration lists others.
	DefaultConnectPort = 443
)

var Tunnel = &goproxy.ConnectAction{Action: goproxy.ConnectAccept}

// ErrDestinationDenied reports that the destination guard refused a dial.
var ErrDestinationDenied = errors.New("destination is not allowed for remote proxy clients")

// Options describes the client exposure of a proxy. The zero value is the
// loopback-only default, where the proxy serves only the local user and needs
// no destination restrictions.
type Options struct {
	// RemoteClients enables the protections required when other machines
	// can reach the proxy: a destination guard against loopback, link-local,
	// and cloud metadata addresses; a CONNECT port allowlist; idle timeouts
	// on tunnels.
	RemoteClients bool
	// ConnectPorts lists the CONNECT ports allowed with RemoteClients. An
	// empty list allows only DefaultConnectPort.
	ConnectPorts []int
	// Trusted lists destinations that bypass the remote-client checks, such
	// as configured lemmingd routes. It may be updated while serving.
	Trusted *TrustList
	// ListenAddress is the proxy's own address, refused as a destination to
	// prevent request loops.
	ListenAddress string
}

func New(logger *log.Logger, options Options) (*goproxy.ProxyHttpServer, *http.Transport) {
	guard := newDestinationGuard(options)
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            guard.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    10,
		MaxConnsPerHost:        32,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		DisableCompression:     true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr = transport
	proxy.ConnectDial = func(network, address string) (net.Conn, error) {
		return guard.DialContext(context.Background(), network, address)
	}
	proxy.ConnectionErrHandler = writeConnectError
	proxy.Logger = logger
	proxy.Verbose = false
	if options.RemoteClients {
		ports := options.ConnectPorts
		if len(ports) == 0 {
			ports = []int{DefaultConnectPort}
		}
		proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			reason := ""
			switch {
			case !connectPortAllowed(host, ports):
				reason = "port"
			case guard.deniedHost(host):
				reason = "destination"
			default:
				return nil, host
			}
			logger.Printf("session=%d method=CONNECT host=%s action=reject reason=%s", ctx.Session, host, reason)
			ctx.Resp = goproxy.NewResponse(ctx.Req, goproxy.ContentTypeText, http.StatusForbidden, "CONNECT destination is not allowed\n")
			return goproxy.RejectConnect, host
		})
		// Refuse obviously denied hosts before any handler, so that a cached
		// response stored in loopback-only mode is not served to remote
		// clients. Names that resolve to denied addresses are refused at dial
		// time.
		proxy.OnRequest().DoFunc(func(request *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			if !guard.deniedHost(request.URL.Host) {
				return request, nil
			}
			logger.Printf("session=%d method=%s host=%s status=403 reason=destination", ctx.Session, request.Method, request.URL.Hostname())
			return request, goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusForbidden, "destination is not allowed\n")
		})
	}
	proxy.OnRequest().DoFunc(func(request *http.Request, _ *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		removeConnectionHeaders(request.Header)
		return request, nil
	})
	// goproxy otherwise returns upstream error text, including internal
	// addresses, to the client. Log the cause and return a generic status.
	proxy.OnResponse().DoFunc(func(response *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if response != nil || ctx.Req == nil {
			return response
		}
		status, message := http.StatusBadGateway, "upstream request failed\n"
		if errors.Is(ctx.Error, ErrDestinationDenied) {
			status, message = http.StatusForbidden, "destination is not allowed\n"
		}
		logger.Printf("session=%d method=%s host=%s status=%d error=%v",
			ctx.Session, ctx.Req.Method, ctx.Req.URL.Hostname(), status, ctx.Error)
		return goproxy.NewResponse(ctx.Req, goproxy.ContentTypeText, status, message)
	})
	return proxy, transport
}

func Serve(ctx context.Context, address string, handler http.Handler, logger *log.Logger, options Options) error {
	return ServeAfterListen(ctx, address, handler, logger, options, nil)
}

// ServeAfterListen runs onReady after the listener is accepting connections and
// before waiting for shutdown. It is useful for launching a client configured
// to use the newly started proxy.
func ServeAfterListen(ctx context.Context, address string, handler http.Handler, logger *log.Logger, options Options, onReady func() error) error {
	rawListener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	var tunnelIdle time.Duration
	if options.RemoteClients {
		tunnelIdle = remoteTunnelIdleTimeout
	}
	listener := newLimitListener(rawListener, maxConnections, tunnelIdle)
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: headerTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ConnState: func(conn net.Conn, state http.ConnState) {
			// net/http stops managing deadlines after a CONNECT hijack.
			if tracked, ok := conn.(*trackedConn); ok && state == http.StateHijacked {
				tracked.enableIdle()
			}
		},
	}
	result := make(chan error, 1)
	go func() {
		result <- server.Serve(listener)
	}()
	logger.Printf("listening on http://%s", rawListener.Addr())
	if onReady != nil {
		if err := onReady(); err != nil {
			return errors.Join(err, stopServer(server, result))
		}
	}
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return stopServer(server, result)
	}
}

func stopServer(server *http.Server, result <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	serveErr := <-result
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(shutdownErr, serveErr)
}

func RoundTripper(transport http.RoundTripper) goproxy.RoundTripper {
	return goproxy.RoundTripperFunc(func(request *http.Request, _ *goproxy.ProxyCtx) (*http.Response, error) {
		return transport.RoundTrip(request)
	})
}

func connectPortAllowed(hostPort string, ports []int) bool {
	_, rawPort, err := net.SplitHostPort(hostPort)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(rawPort)
	return err == nil && slices.Contains(ports, port)
}

// removeConnectionHeaders drops headers that the client marked as hop-by-hop
// in Connection. goproxy removes Connection itself but not the fields it
// names. WebSocket upgrades keep Connection and Upgrade.
func removeConnectionHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			switch strings.ToLower(name) {
			case "", "close", "keep-alive", "upgrade":
				continue
			}
			header.Del(name)
		}
	}
	header.Del("Keep-Alive")
}

// writeConnectError answers a CONNECT whose destination could not be dialed.
// goproxy also reports copy errors of established tunnels here; those are
// not answered, because the client is already speaking TLS to the target.
func writeConnectError(writer io.Writer, ctx *goproxy.ProxyCtx, err error) {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op != "dial" {
		return
	}
	status := http.StatusBadGateway
	if errors.Is(err, ErrDestinationDenied) {
		status = http.StatusForbidden
	}
	host := ""
	if ctx != nil && ctx.Req != nil {
		host = ctx.Req.URL.Host
	}
	if ctx != nil && ctx.Proxy != nil && ctx.Proxy.Logger != nil {
		ctx.Proxy.Logger.Printf("session=%d method=CONNECT host=%s status=%d error=%v", ctx.Session, host, status, err)
	}
	text := http.StatusText(status)
	_, _ = fmt.Fprintf(writer, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s\n",
		status, text, len(text)+1, text)
}

// TrustList holds destinations exempt from the remote-client checks. It is
// safe for concurrent use and can be replaced atomically.
type TrustList struct {
	sets atomic.Pointer[trustSets]
}

type trustSets struct {
	targets map[string]struct{}
	hosts   map[string]struct{}
}

// NewTrustList returns a list with the given targets and hosts.
func NewTrustList(targets, hosts []string) *TrustList {
	list := &TrustList{}
	list.Set(targets, hosts)
	return list
}

// Set replaces the trusted targets (exact host:port destinations that
// bypass the dial-time guard) and hosts (hostnames whose requests the caller
// routes to trusted targets, exempt from the host check before handlers).
func (list *TrustList) Set(targets, hosts []string) {
	sets := &trustSets{targets: make(map[string]struct{}), hosts: make(map[string]struct{})}
	for _, target := range targets {
		sets.targets[strings.ToLower(target)] = struct{}{}
	}
	for _, host := range hosts {
		sets.hosts[normalizeHostname(host)] = struct{}{}
	}
	list.sets.Store(sets)
}

// HasTarget reports whether hostPort is a trusted target.
func (list *TrustList) HasTarget(hostPort string) bool {
	return list.target(hostPort)
}

func (list *TrustList) target(hostPort string) bool {
	if list == nil {
		return false
	}
	_, ok := list.sets.Load().targets[strings.ToLower(hostPort)]
	return ok
}

func (list *TrustList) host(host string) bool {
	if list == nil {
		return false
	}
	_, ok := list.sets.Load().hosts[host]
	return ok
}

type destinationGuard struct {
	enabled bool
	trusted *TrustList
	dialer  *net.Dialer
	direct  *net.Dialer
}

func newDestinationGuard(options Options) *destinationGuard {
	guard := &destinationGuard{
		enabled: options.RemoteClients,
		trusted: options.Trusted,
		direct:  &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second},
	}
	self := selfAddresses(options.ListenAddress)
	guard.dialer = &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// Control sees the resolved address, so DNS names that resolve or
		// rebind to a forbidden address are refused too.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return ErrDestinationDenied
			}
			ip := net.ParseIP(host)
			if ip == nil || deniedDestination(ip) {
				return ErrDestinationDenied
			}
			if _, loop := self[net.JoinHostPort(ip.String(), port)]; loop {
				return ErrDestinationDenied
			}
			return nil
		},
	}
	return guard
}

// deniedHost reports a host:port or host whose name or literal address is
// refused without DNS resolution. Trusted targets are allowed.
func (guard *destinationGuard) deniedHost(hostPort string) bool {
	if guard.trusted.target(hostPort) {
		return false
	}
	host := hostPort
	if splitHost, _, err := net.SplitHostPort(hostPort); err == nil {
		host = splitHost
	}
	host = normalizeHostname(strings.Trim(host, "[]"))
	if guard.trusted.host(host) {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && deniedDestination(ip)
}

func (guard *destinationGuard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !guard.enabled {
		return guard.direct.DialContext(ctx, network, address)
	}
	if guard.trusted.target(address) {
		return guard.direct.DialContext(ctx, network, address)
	}
	conn, err := guard.dialer.DialContext(ctx, network, address)
	if err != nil && errors.Is(err, ErrDestinationDenied) {
		return nil, ErrDestinationDenied
	}
	return conn, err
}

var metadataAddresses = []net.IP{
	net.ParseIP("fd00:ec2::254"),   // AWS IMDS over IPv6
	net.ParseIP("100.100.100.200"), // Alibaba Cloud metadata
}

// deniedDestination reports addresses that remote clients must not reach
// through the proxy host: its own loopback services, link-local and cloud
// metadata endpoints, and non-unicast addresses. Private networks remain
// reachable so a shared cache can serve internal mirrors.
func deniedDestination(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil && (ip4[0] == 0 || ip4.Equal(net.IPv4bcast)) {
		return true
	}
	for _, metadata := range metadataAddresses {
		if ip.Equal(metadata) {
			return true
		}
	}
	return false
}

// selfAddresses returns the ip:port pairs at which the proxy itself listens.
func selfAddresses(listen string) map[string]struct{} {
	result := make(map[string]struct{})
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return result
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		result[net.JoinHostPort(ip.String(), port)] = struct{}{}
		return result
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return result
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok {
			result[net.JoinHostPort(network.IP.String(), port)] = struct{}{}
		}
	}
	return result
}
