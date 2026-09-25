package redirect

import (
	"net/http"
	"testing"

	"github.com/BOPOHA/hamsterd/internal/config"
)

func TestRouterRewritesIncludedPath(t *testing.T) {
	router, err := New([]config.RedirectRule{{
		Target: "127.0.0.1:8000", Domains: []string{"Static.Example.com"},
		Include: []string{"/static/"}, Exclude: []string{"/static/generated/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !router.Intercepts("static.example.com:443") {
		t.Fatal("configured HTTPS host was not recognized")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://static.example.com/static/app.js", nil)
	rewritten, ok := router.Rewrite(request)
	if !ok {
		t.Fatal("included path was not rewritten")
	}
	if rewritten.URL.String() != "http://127.0.0.1:8000/static/app.js" {
		t.Fatalf("unexpected rewritten URL %q", rewritten.URL)
	}
	if request.URL.Host != "static.example.com" {
		t.Fatal("original request was mutated")
	}
}

func TestRouterExclusionWins(t *testing.T) {
	router, err := New([]config.RedirectRule{{
		Target: "127.0.0.1:8000", Domains: []string{"static.example.com"},
		Include: []string{"/"}, Exclude: []string{"/api/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://static.example.com/api/users", nil)
	if _, ok := router.Rewrite(request); ok {
		t.Fatal("excluded path was rewritten")
	}
}

func TestRouterRejectsDuplicateNormalizedDomain(t *testing.T) {
	_, err := New([]config.RedirectRule{
		{Target: "127.0.0.1:3000", Domains: []string{"App.Example."}, Include: []string{"/"}},
		{Target: "127.0.0.1:4000", Domains: []string{"app.example"}, Include: []string{"/"}},
	})
	if err == nil {
		t.Fatal("expected duplicate domain error")
	}
}

func TestRouterLeavesUnknownAndUnmatchedRequestsUnchanged(t *testing.T) {
	router, err := New([]config.RedirectRule{{
		Target: "127.0.0.1:8000", Domains: []string{"app.example"}, Include: []string{"/static/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{
		"https://unknown.example/static/app.js",
		"https://app.example/api/users",
		"https://app.example/Static/app.js",
	}
	for _, rawURL := range tests {
		request, requestErr := http.NewRequest(http.MethodGet, rawURL, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		got, changed := router.Rewrite(request)
		if changed || got != request {
			t.Errorf("request %q was unexpectedly rewritten", rawURL)
		}
	}
	if router.Intercepts("unknown.example:443") {
		t.Fatal("unknown domain was intercepted")
	}
}

func TestRouterUsesRequestHostFallbackAndPreservesQuery(t *testing.T) {
	router, err := New([]config.RedirectRule{{
		Target: "127.0.0.1:8000", Domains: []string{"app.example"}, Include: []string{"/assets/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "/assets/app.js?v=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "APP.EXAMPLE:443"
	got, changed := router.Rewrite(request)
	if !changed {
		t.Fatal("request host fallback did not match")
	}
	if got.URL.String() != "http://127.0.0.1:8000/assets/app.js?v=2" {
		t.Fatalf("rewritten URL = %q", got.URL)
	}
	if got.Host != "127.0.0.1:8000" {
		t.Fatalf("rewritten Host = %q", got.Host)
	}
}

func TestNormalizeHost(t *testing.T) {
	tests := map[string]string{
		" Example.COM. ":        "example.com",
		"Example.COM:443":       "example.com",
		"[::1]:443":             "::1",
		"unparseable:host:port": "unparseable:host:port",
	}
	for input, want := range tests {
		if got := normalizeHost(input); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", input, got, want)
		}
	}
}
