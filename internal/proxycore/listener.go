package proxycore

import (
	"net"
	"sync"
	"time"
)

const (
	// maxConnections bounds client connections, including hijacked CONNECT
	// tunnels that net/http no longer tracks.
	maxConnections = 1024
	// remoteTunnelIdleTimeout closes CONNECT tunnels and intercepted
	// sessions without traffic in either direction when remote clients are
	// allowed. Loopback-only proxies keep idle tunnels, such as development
	// server WebSockets, open indefinitely.
	remoteTunnelIdleTimeout = 15 * time.Minute
)

// limitListener caps concurrent connections and wraps them so that an idle
// timeout can be enabled once net/http hands a connection over to the proxy.
type limitListener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
	idle  time.Duration
}

func newLimitListener(listener net.Listener, limit int, idle time.Duration) *limitListener {
	return &limitListener{
		Listener: listener,
		slots:    make(chan struct{}, limit),
		done:     make(chan struct{}),
		idle:     idle,
	}
}

func (listener *limitListener) Accept() (net.Conn, error) {
	select {
	case listener.slots <- struct{}{}:
	case <-listener.done:
		return nil, net.ErrClosed
	}
	conn, err := listener.Listener.Accept()
	if err != nil {
		<-listener.slots
		return nil, err
	}
	return &trackedConn{Conn: conn, idle: listener.idle, release: func() { <-listener.slots }}, nil
}

func (listener *limitListener) Close() error {
	listener.once.Do(func() { close(listener.done) })
	return listener.Listener.Close()
}

// trackedConn releases its listener slot on Close. After enableIdle, every
// read or write extends both deadlines by the idle timeout; deadlines set by
// callers still apply when they are earlier.
type trackedConn struct {
	net.Conn
	idle        time.Duration
	release     func()
	releaseOnce sync.Once

	mu            sync.Mutex
	idleEnabled   bool
	refreshed     time.Time
	readDeadline  time.Time
	writeDeadline time.Time
}

func (conn *trackedConn) Read(buffer []byte) (int, error) {
	conn.touch()
	return conn.Conn.Read(buffer)
}

func (conn *trackedConn) Write(buffer []byte) (int, error) {
	conn.touch()
	return conn.Conn.Write(buffer)
}

func (conn *trackedConn) Close() error {
	conn.releaseOnce.Do(conn.release)
	return conn.Conn.Close()
}

// CloseRead and CloseWrite keep the half-close support that goproxy uses for
// TCP tunnels.
func (conn *trackedConn) CloseRead() error {
	if tcp, ok := conn.Conn.(interface{ CloseRead() error }); ok {
		return tcp.CloseRead()
	}
	return conn.Close()
}

func (conn *trackedConn) CloseWrite() error {
	if tcp, ok := conn.Conn.(interface{ CloseWrite() error }); ok {
		return tcp.CloseWrite()
	}
	return conn.Close()
}

func (conn *trackedConn) SetDeadline(deadline time.Time) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.readDeadline, conn.writeDeadline = deadline, deadline
	return conn.applyLocked()
}

func (conn *trackedConn) SetReadDeadline(deadline time.Time) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.readDeadline = deadline
	return conn.applyLocked()
}

func (conn *trackedConn) SetWriteDeadline(deadline time.Time) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.writeDeadline = deadline
	return conn.applyLocked()
}

func (conn *trackedConn) enableIdle() {
	if conn.idle <= 0 {
		return
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.idleEnabled = true
	_ = conn.applyLocked()
}

func (conn *trackedConn) touch() {
	if conn.idle <= 0 {
		return
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.idleEnabled && time.Since(conn.refreshed) >= time.Second {
		_ = conn.applyLocked()
	}
}

func (conn *trackedConn) applyLocked() error {
	read, write := conn.readDeadline, conn.writeDeadline
	if conn.idleEnabled {
		now := time.Now()
		conn.refreshed = now
		limit := now.Add(conn.idle)
		read, write = earliest(read, limit), earliest(write, limit)
	}
	if err := conn.Conn.SetReadDeadline(read); err != nil {
		return err
	}
	return conn.Conn.SetWriteDeadline(write)
}

func earliest(deadline, limit time.Time) time.Time {
	if deadline.IsZero() || limit.Before(deadline) {
		return limit
	}
	return deadline
}
