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
