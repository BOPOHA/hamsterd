package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunVersionAndFlagErrors(t *testing.T) {
	setUserDirs(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "lemmingd ") {
		t.Fatalf("version output = %q", stdout.String())
	}

	stderr.Reset()
	if code := run([]string{"-not-a-flag"}, &stdout, &stderr); code != 2 {
		t.Fatalf("invalid flag exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("invalid flag stderr = %q", stderr.String())
	}
}

func TestRunDefaultPathError(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("XDG_CONFIG_HOME controls UserConfigDir only on Unix-like systems")
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	var stderr bytes.Buffer
	if code := run(nil, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "path in $XDG_CONFIG_HOME is relative") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCreatesFilesBeforeOccupiedListenerError(t *testing.T) {
	configHome, _ := setUserDirs(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var stderr bytes.Buffer
	code := run([]string{"-listen", listener.Addr().String()}, &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	for _, fragment := range []string{"created configuration", "created a unique local CA", "server error"} {
		if !strings.Contains(stderr.String(), fragment) {
			t.Errorf("stderr does not contain %q: %s", fragment, stderr.String())
		}
	}
	for _, name := range []string{"config.json", "ca.crt", "ca.key"} {
		if _, err := os.Stat(filepath.Join(configHome, "lemmingd", name)); err != nil {
			t.Errorf("expected %s to be created: %v", name, err)
		}
	}
}

func TestRunReportsConfigurationCAAndRouterErrors(t *testing.T) {
	setUserDirs(t)

	t.Run("configuration", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"version":99}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		if code := run([]string{"-config", path}, &bytes.Buffer{}, &stderr); code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "configuration error") {
			t.Fatalf("stderr = %q", stderr.String())
		}
	})

	t.Run("listen override", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := run([]string{"-listen", "public.example:18080"}, &bytes.Buffer{}, &stderr); code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "allow_remote_clients") {
			t.Fatalf("stderr = %q", stderr.String())
		}
	})

	t.Run("CA pair", func(t *testing.T) {
		dir := t.TempDir()
		certPath := filepath.Join(dir, "ca.crt")
		if err := os.WriteFile(certPath, []byte("not a certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		code := run([]string{"-cacert", certPath, "-cakey", filepath.Join(dir, "ca.key")}, &bytes.Buffer{}, &stderr)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "CA error") {
			t.Fatalf("stderr = %q", stderr.String())
		}
	})

	t.Run("duplicate route", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		configJSON := `{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "rules": [
    {"target":"127.0.0.1:3000","domains":["app.example"],"include_paths":["/"]},
    {"target":"127.0.0.1:4000","domains":["APP.EXAMPLE"],"include_paths":["/"]}
  ]
}`
		if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		code := run([]string{
			"-config", configPath,
			"-cacert", filepath.Join(dir, "pki", "ca.crt"),
			"-cakey", filepath.Join(dir, "pki", "ca.key"),
		}, &bytes.Buffer{}, &stderr)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "redirect configuration error") {
			t.Fatalf("stderr = %q", stderr.String())
		}
	})
}

func setUserDirs(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", root)
		return filepath.Join(root, "Library", "Application Support"), filepath.Join(root, "Library", "Caches")
	case "windows":
		configHome := filepath.Join(root, "config")
		cacheHome := filepath.Join(root, "cache")
		t.Setenv("AppData", configHome)
		t.Setenv("LocalAppData", cacheHome)
		return configHome, cacheHome
	default:
		configHome := filepath.Join(root, "config")
		cacheHome := filepath.Join(root, "cache")
		t.Setenv("XDG_CONFIG_HOME", configHome)
		t.Setenv("XDG_CACHE_HOME", cacheHome)
		return configHome, cacheHome
	}
}

func TestInformationHandler(t *testing.T) {
	handler := informationHandler([]byte("test CA"))
	tests := []struct {
		path        string
		contentType string
		body        string
	}{
		{path: "/", contentType: "text/plain; charset=utf-8", body: "lemmingd selective local-development proxy\nCA certificate: /ca.crt\nHealth check: /healthz\n"},
		{path: "/ca.crt", contentType: "application/x-x509-ca-cert", body: "test CA"},
		{path: "/healthz", contentType: "text/plain; charset=utf-8", body: "ok\n"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != test.contentType {
				t.Errorf("content type = %q, want %q", got, test.contentType)
			}
			if got := recorder.Body.String(); got != test.body {
				t.Errorf("body = %q, want %q", got, test.body)
			}
		})
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ca.crt", nil))
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="lemmingd-ca.crt"` {
		t.Fatalf("content disposition = %q", got)
	}
}
