package cache

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestTransportCachesPublicGET(t *testing.T) {
	var calls atomic.Int32
	base := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(request, "cached body", "public, max-age=60"), nil
	})
	transport, err := NewTransport(base, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	first := fetch(t, transport, "")
	if got := first.Header.Get(statusHeader); got != "MISS" {
		t.Fatalf("first cache status = %q", got)
	}
	second := fetch(t, transport, "")
	if got := second.Header.Get(statusHeader); got != "HIT" {
		t.Fatalf("second cache status = %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestTransportBypassesAuthenticatedRequests(t *testing.T) {
	var calls atomic.Int32
	base := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(request, "private body", "public, max-age=60"), nil
	})
	transport, err := NewTransport(base, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		resp := fetch(t, transport, "Bearer secret")
		if got := resp.Header.Get(statusHeader); got != "BYPASS" {
			t.Fatalf("cache status = %q", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
}

func TestTransportDoesNotStorePrivateResponse(t *testing.T) {
	var calls atomic.Int32
	base := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(request, "private body", "private, max-age=60"), nil
	})
	transport, err := NewTransport(base, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		resp := fetch(t, transport, "")
		if got := resp.Header.Get(statusHeader); got != "BYPASS" {
			t.Fatalf("cache status = %q", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Directory: t.TempDir(), MaxBytes: 1 << 20, MaxObjectBytes: 1 << 19, DefaultTTL: time.Minute}
}

func response(request *http.Request, body, cacheControl string) *http.Response {
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Cache-Control": []string{cacheControl}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

func fetch(t *testing.T, transport http.RoundTripper, authorization string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "https://example.test/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	resp, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return resp
}
