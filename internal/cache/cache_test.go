package cache

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

func TestNewTransportValidation(t *testing.T) {
	valid := testConfig(t)
	tests := []struct {
		name   string
		config Config
	}{
		{name: "empty directory", config: Config{MaxBytes: 2, MaxObjectBytes: 1, DefaultTTL: time.Minute}},
		{name: "empty total size", config: Config{Directory: valid.Directory, MaxObjectBytes: 1, DefaultTTL: time.Minute}},
		{name: "empty object size", config: Config{Directory: valid.Directory, MaxBytes: 2, DefaultTTL: time.Minute}},
		{name: "object exceeds total", config: Config{Directory: valid.Directory, MaxBytes: 1, MaxObjectBytes: 2, DefaultTTL: time.Minute}},
		{name: "empty TTL", config: Config{Directory: valid.Directory, MaxBytes: 2, MaxObjectBytes: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTransport(nil, test.config); err == nil {
				t.Fatal("expected invalid configuration to fail")
			}
		})
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Directory: filepath.Join(file, "cache"), MaxBytes: 2, MaxObjectBytes: 1, DefaultTTL: time.Minute}
	if _, err := NewTransport(nil, config); err == nil || !strings.Contains(err.Error(), "create cache directory") {
		t.Fatalf("directory error = %v", err)
	}
}

func TestCacheableRequestPolicy(t *testing.T) {
	valid, err := http.NewRequest(http.MethodGet, "https://example.test/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cacheableRequest(valid) {
		t.Fatal("plain GET should be cacheable")
	}

	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "method", mutate: func(r *http.Request) { r.Method = http.MethodPost }},
		{name: "nil URL", mutate: func(r *http.Request) { r.URL = nil }},
		{name: "URL credentials", mutate: func(r *http.Request) { r.URL.User = url.User("name") }},
		{name: "authorization", mutate: func(r *http.Request) { r.Header.Set("Authorization", "secret") }},
		{name: "cookie", mutate: func(r *http.Request) { r.Header.Set("Cookie", "session=x") }},
		{name: "range", mutate: func(r *http.Request) { r.Header.Set("Range", "bytes=0-1") }},
		{name: "no cache", mutate: func(r *http.Request) { r.Header.Set("Cache-Control", "no-cache") }},
		{name: "no store", mutate: func(r *http.Request) { r.Header.Set("Cache-Control", "no-store") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid.Clone(valid.Context())
			urlCopy := *valid.URL
			request.URL = &urlCopy
			request.Header = valid.Header.Clone()
			test.mutate(request)
			if cacheableRequest(request) {
				t.Fatal("request unexpectedly cacheable")
			}
		})
	}
}

func TestResponseExpirationPolicy(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := func() *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
	}
	tests := []struct {
		name   string
		mutate func(*http.Response)
		ok     bool
	}{
		{name: "default", mutate: func(*http.Response) {}, ok: true},
		{name: "status", mutate: func(r *http.Response) { r.StatusCode = http.StatusNotFound }},
		{name: "set cookie", mutate: func(r *http.Response) { r.Header.Set("Set-Cookie", "x=y") }},
		{name: "vary", mutate: func(r *http.Response) { r.Header.Set("Vary", "Accept") }},
		{name: "content range", mutate: func(r *http.Response) { r.Header.Set("Content-Range", "bytes 0-1/2") }},
		{name: "no store", mutate: func(r *http.Response) { r.Header.Set("Cache-Control", "no-store") }},
		{name: "no cache", mutate: func(r *http.Response) { r.Header.Set("Cache-Control", "no-cache") }},
		{name: "private", mutate: func(r *http.Response) { r.Header.Set("Cache-Control", "private") }},
		{name: "bad max age", mutate: func(r *http.Response) { r.Header.Set("Cache-Control", "max-age=bad") }},
		{name: "zero max age", mutate: func(r *http.Response) { r.Header.Set("Cache-Control", "max-age=0") }},
		{name: "max age", mutate: func(r *http.Response) { r.Header.Set("Cache-Control", "public, max-age=60") }, ok: true},
		{name: "bad expires", mutate: func(r *http.Response) { r.Header.Set("Expires", "tomorrow") }},
		{name: "past expires", mutate: func(r *http.Response) { r.Header.Set("Expires", now.Add(-time.Minute).Format(http.TimeFormat)) }},
		{name: "future expires", mutate: func(r *http.Response) { r.Header.Set("Expires", now.Add(time.Minute).Format(http.TimeFormat)) }, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := base()
			test.mutate(response)
			expires, ok := responseExpiration(response, now, 30*time.Second)
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v; expiry = %v", ok, test.ok, expires)
			}
			if ok && !expires.After(now) {
				t.Fatalf("expiry = %v, want after %v", expires, now)
			}
		})
	}
}

func TestTransportPropagatesUpstreamError(t *testing.T) {
	want := errors.New("upstream failed")
	transport, err := NewTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	}), testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test/file", nil)
	if _, err := transport.RoundTrip(request); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestTransportBypassesNilAndDeclaredOversizeBodies(t *testing.T) {
	tests := []struct {
		name string
		body io.ReadCloser
		size int64
	}{
		{name: "nil body", body: nil, size: 0},
		{name: "oversize", body: io.NopCloser(strings.NewReader("too large")), size: 9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(t)
			config.MaxObjectBytes = 4
			transport, err := NewTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				response := response(request, "", "public, max-age=60")
				response.Body = test.body
				response.ContentLength = test.size
				return response, nil
			}), config)
			if err != nil {
				t.Fatal(err)
			}
			request, _ := http.NewRequest(http.MethodGet, "https://example.test/file", nil)
			got, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			if got.Header.Get(statusHeader) != "BYPASS" {
				t.Fatalf("cache status = %q", got.Header.Get(statusHeader))
			}
			if got.Body != nil {
				got.Body.Close()
			}
		})
	}
}

func TestTransportDiscardsStreamingOversizeAndIncompleteBodies(t *testing.T) {
	t.Run("streaming oversize", func(t *testing.T) {
		var calls atomic.Int32
		config := testConfig(t)
		config.MaxObjectBytes = 4
		transport, err := NewTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			result := response(request, "too large", "public, max-age=60")
			result.ContentLength = -1
			return result, nil
		}), config)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			got := fetch(t, transport, "")
			if got.Header.Get(statusHeader) != "MISS" {
				t.Fatalf("cache status = %q", got.Header.Get(statusHeader))
			}
		}
		if calls.Load() != 2 {
			t.Fatalf("upstream calls = %d, want 2", calls.Load())
		}
	})

	t.Run("closed early", func(t *testing.T) {
		var calls atomic.Int32
		transport, err := NewTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return response(request, "complete body", "public, max-age=60"), nil
		}), testConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest(http.MethodGet, "https://example.test/file", nil)
		first, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := first.Body.Close(); err != nil {
			t.Fatal(err)
		}
		fetch(t, transport, "")
		if calls.Load() != 2 {
			t.Fatalf("upstream calls = %d, want 2", calls.Load())
		}
	})
}

func TestTransportHandlesCacheStorageFailure(t *testing.T) {
	config := testConfig(t)
	transport, err := NewTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, "body", "public, max-age=60"), nil
	}), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(config.Directory); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://example.test/file", nil)
	got, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Get(statusHeader) != "BYPASS" {
		t.Fatalf("cache status = %q", got.Header.Get(statusHeader))
	}
	got.Body.Close()
}

func TestTransportExpiresAndEvictsEntries(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		var calls atomic.Int32
		config := testConfig(t)
		config.DefaultTTL = time.Nanosecond
		transport, err := NewTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return response(request, "body", ""), nil
		}), config)
		if err != nil {
			t.Fatal(err)
		}
		fetch(t, transport, "")
		fetch(t, transport, "")
		if calls.Load() != 2 {
			t.Fatalf("expired response was reused; calls = %d", calls.Load())
		}
	})

	t.Run("eviction", func(t *testing.T) {
		var calls atomic.Int32
		config := testConfig(t)
		config.MaxBytes = 5
		config.MaxObjectBytes = 5
		transport, err := NewTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return response(request, "four", "public, max-age=60"), nil
		}), config)
		if err != nil {
			t.Fatal(err)
		}
		fetchURL := func(rawURL string) {
			request, _ := http.NewRequest(http.MethodGet, rawURL, nil)
			got, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadAll(got.Body); err != nil {
				t.Fatal(err)
			}
			got.Body.Close()
		}
		fetchURL("https://example.test/one")
		firstMeta, firstBody := transport.store.paths(cacheKey(mustRequest(t, "https://example.test/one")))
		old := time.Unix(1, 0)
		if err := os.Chtimes(firstMeta, old, old); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(firstBody, old, old); err != nil {
			t.Fatal(err)
		}
		fetchURL("https://example.test/two")
		fetchURL("https://example.test/one")
		if calls.Load() != 3 {
			t.Fatalf("upstream calls = %d, want 3 after eviction", calls.Load())
		}
	})
}

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
