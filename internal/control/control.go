// Package control implements a local control API for a running proxy.
//
// The server listens on a random loopback port and publishes its URL and a
// random bearer token in a file inside the private configuration directory,
// like Jupyter runtime files or Chrome's DevToolsActivePort. Whoever can read
// that directory can control the proxy; nobody else can, including browsers
// and proxy clients on the network.
package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// FileName is the endpoint file written next to the configuration.
const FileName = "control.json"

const (
	maxRequestBytes  = 64 << 10
	maxResponseBytes = 1 << 20
)

// ErrNotRunning reports that no control API answers at the published
// endpoint, or that no endpoint is published.
var ErrNotRunning = errors.New("not running with the control API")

// Endpoint is the content of the endpoint file.
type Endpoint struct {
	Version int    `json:"version"`
	Service string `json:"service"`
	URL     string `json:"url"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
}

// Server is a running control API.
type Server struct {
	server   *http.Server
	path     string
	token    string
	finished chan struct{}
}

// Start serves handler on a random loopback port and writes the endpoint
// file into dir, which must already exist and be private to the user.
func Start(dir, service string, handler http.Handler, logger *log.Logger) (*Server, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate control token: %w", err)
	}
	token := hex.EncodeToString(secret)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for the control API: %w", err)
	}
	address := listener.Addr().String()
	endpoint := Endpoint{Version: 1, Service: service, URL: "http://" + address, Token: token, PID: os.Getpid()}
	path := filepath.Join(dir, FileName)
	if err := writeEndpoint(path, endpoint); err != nil {
		listener.Close()
		return nil, err
	}
	s := &Server{
		server: &http.Server{
			Handler:           authorize(handler, address, token),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       time.Minute,
			MaxHeaderBytes:    16 << 10,
			ErrorLog:          logger,
		},
		path:     path,
		token:    token,
		finished: make(chan struct{}),
	}
	go func() {
		defer close(s.finished)
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("control API stopped: %v", err)
		}
	}()
	return s, nil
}

// Path returns the endpoint file path.
func (s *Server) Path() string {
	return s.path
}

// Close stops the server and removes the endpoint file if it still belongs
// to this server.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.server.Shutdown(ctx)
	<-s.finished
	if current, readErr := readEndpoint(s.path); readErr == nil && current.Token == s.token {
		if removeErr := os.Remove(s.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}
	return err
}

// authorize admits only requests that carry the token, address the
// listener by its literal loopback address, and do not come from a browser.
// The Host check defeats DNS rebinding; refusing Origin and Sec-Fetch-*
// headers stops cross-site requests from web pages.
func authorize(next http.Handler, address, token string) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Host != address:
			WriteError(writer, http.StatusForbidden, errors.New("unexpected Host header"))
			return
		case request.Header.Get("Origin") != "" || request.Header.Get("Sec-Fetch-Site") != "" || request.Header.Get("Sec-Fetch-Mode") != "":
			WriteError(writer, http.StatusForbidden, errors.New("browser requests are not accepted"))
			return
		case subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), expected) != 1:
			WriteError(writer, http.StatusUnauthorized, errors.New("missing or invalid token"))
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
		next.ServeHTTP(writer, request)
	})
}

// WriteJSON writes a JSON response.
func WriteJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

// WriteError writes {"error": "..."} with status.
func WriteError(writer http.ResponseWriter, status int, err error) {
	WriteJSON(writer, status, map[string]string{"error": err.Error()})
}

// DecodeJSON strictly decodes a request body.
func DecodeJSON(request *http.Request, value any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid request body: unexpected data after the JSON value")
	}
	return nil
}

// Client calls a control API published in a directory.
type Client struct {
	dir     string
	service string
	http    *http.Client
}

// NewClient returns a client for the endpoint file in dir. The file is read
// on every call, so the client keeps working across proxy restarts.
func NewClient(dir, service string) *Client {
	return &Client{
		dir:     dir,
		service: service,
		http:    &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}},
	}
}

// Do sends a JSON request and decodes a JSON response into out, which may be
// nil. API errors are returned with the server's message.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	endpoint, err := readEndpoint(filepath.Join(c.dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotRunning
	}
	if err != nil {
		return err
	}
	if endpoint.Service != c.service || !strings.HasPrefix(endpoint.URL, "http://127.0.0.1:") {
		return fmt.Errorf("%s does not describe a local %s control API", FileName, c.service)
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.URL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+endpoint.Token)
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return ErrNotRunning
		}
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		var apiError struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &apiError) == nil && apiError.Error != "" {
			return errors.New(apiError.Error)
		}
		return fmt.Errorf("control API returned %s", response.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func readEndpoint(path string) (Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, err
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
			return Endpoint{}, fmt.Errorf("%s is accessible by other users (mode %04o)", path, info.Mode().Perm())
		}
	}
	var endpoint Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if endpoint.Version != 1 {
		return Endpoint{}, fmt.Errorf("parse %s: unsupported version %d", path, endpoint.Version)
	}
	return endpoint, nil
}

func writeEndpoint(path string, endpoint Endpoint) error {
	data, err := json.MarshalIndent(endpoint, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+FileName+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create control endpoint file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure control endpoint file: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write control endpoint file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write control endpoint file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install control endpoint file: %w", err)
	}
	return nil
}
