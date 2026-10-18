package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func startTestServer(t *testing.T) (*Server, Endpoint, string) {
	t.Helper()
	dir := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ping", func(writer http.ResponseWriter, _ *http.Request) {
		WriteJSON(writer, http.StatusOK, map[string]string{"pong": "yes"})
	})
	mux.HandleFunc("POST /v1/echo", func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		if err := DecodeJSON(request, &body); err != nil {
			WriteError(writer, http.StatusBadRequest, err)
			return
		}
		WriteJSON(writer, http.StatusOK, body)
	})
	server, err := Start(dir, "lemmingd", mux, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	endpoint, err := readEndpoint(server.Path())
	if err != nil {
		t.Fatal(err)
	}
	return server, endpoint, dir
}

func TestEndpointFileIsPrivate(t *testing.T) {
	server, endpoint, _ := startTestServer(t)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(server.Path())
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("endpoint file mode = %v, %v", info.Mode(), err)
		}
	}
	if !strings.HasPrefix(endpoint.URL, "http://127.0.0.1:") || len(endpoint.Token) != 64 || endpoint.PID != os.Getpid() {
		t.Fatalf("endpoint = %+v", endpoint)
	}
}

func TestAuthorization(t *testing.T) {
	_, endpoint, _ := startTestServer(t)
	host := strings.TrimPrefix(endpoint.URL, "http://")
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{name: "valid", mutate: func(*http.Request) {}, status: http.StatusOK},
		{name: "no token", mutate: func(r *http.Request) { r.Header.Del("Authorization") }, status: http.StatusUnauthorized},
		{name: "wrong token", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("0", 64)) }, status: http.StatusUnauthorized},
		{name: "rebinding host", mutate: func(r *http.Request) { r.Host = "attacker.example:" + strings.Split(host, ":")[1] }, status: http.StatusForbidden},
		{name: "localhost name", mutate: func(r *http.Request) { r.Host = "localhost:" + strings.Split(host, ":")[1] }, status: http.StatusForbidden},
		{name: "browser origin", mutate: func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, status: http.StatusForbidden},
		{name: "browser fetch metadata", mutate: func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, endpoint.URL+"/v1/ping", nil)
			request.Header.Set("Authorization", "Bearer "+endpoint.Token)
			test.mutate(request)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}

func TestClientCallsAndReportsErrors(t *testing.T) {
	_, _, dir := startTestServer(t)
	client := NewClient(dir, "lemmingd")
	var pong map[string]string
	if err := client.Do(context.Background(), "GET", "/v1/ping", nil, &pong); err != nil || pong["pong"] != "yes" {
		t.Fatalf("ping = (%v, %v)", pong, err)
	}
	var echo map[string]string
	if err := client.Do(context.Background(), "POST", "/v1/echo", map[string]string{"a": "b"}, &echo); err != nil || echo["a"] != "b" {
		t.Fatalf("echo = (%v, %v)", echo, err)
	}
	if err := client.Do(context.Background(), "POST", "/v1/echo", []int{1}, nil); err == nil || !strings.Contains(err.Error(), "invalid request body") {
		t.Fatalf("API error = %v", err)
	}
	if err := client.Do(context.Background(), "POST", "/v1/echo", map[string]string{"big": strings.Repeat("x", maxRequestBytes)}, nil); err == nil {
		t.Fatal("oversized body accepted")
	}
	if err := NewClient(dir, "hamsterd").Do(context.Background(), "GET", "/v1/ping", nil, nil); err == nil {
		t.Fatal("client accepted another service's endpoint")
	}
}

func TestClientNotRunning(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir, "lemmingd")
	if err := client.Do(context.Background(), "GET", "/v1/ping", nil, nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("missing endpoint error = %v", err)
	}
	server, err := Start(dir, "lemmingd", http.NotFoundHandler(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := os.ReadFile(server.Path())
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(server.Path()); !os.IsNotExist(err) {
		t.Fatalf("endpoint file remains after Close: %v", err)
	}
	// A stale file from a crashed process points at a closed port.
	if err := os.WriteFile(server.Path(), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Do(context.Background(), "GET", "/v1/ping", nil, nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stale endpoint error = %v", err)
	}
}

func TestClientRefusesUnsafeEndpointFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	data, _ := json.Marshal(Endpoint{Version: 1, Service: "lemmingd", URL: "http://attacker.example:80", Token: "t"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewClient(dir, "lemmingd").Do(context.Background(), "GET", "/v1/ping", nil, nil); err == nil || !strings.Contains(err.Error(), "local") {
		t.Fatalf("non-loopback endpoint error = %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewClient(dir, "lemmingd").Do(context.Background(), "GET", "/v1/ping", nil, nil); err == nil || !strings.Contains(err.Error(), "accessible by other users") {
		t.Fatalf("readable endpoint error = %v", err)
	}
}

func TestCloseKeepsReplacedEndpointFile(t *testing.T) {
	server, _, _ := startTestServer(t)
	replacement := []byte(`{"version":1,"service":"lemmingd","url":"http://127.0.0.1:1","token":"other","pid":1}`)
	if err := os.WriteFile(server.Path(), replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(server.Path()); err != nil || string(data) != string(replacement) {
		t.Fatalf("another instance's endpoint file was removed: %v", err)
	}
}
