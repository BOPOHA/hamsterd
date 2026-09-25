package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateSecureDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.json")
	cfg, created, err := LoadOrCreate(path, Hamsterd, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected the config to be created")
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("unexpected listen address %q", cfg.Listen)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
}

func TestLegacyLemmingConfig(t *testing.T) {
	data := []byte(`{
  "FormatVersion": "1.0",
  "Socket": "127.0.0.1:18080",
  "CustomConfig": [{
    "LocalSocket": "127.0.0.1:8000",
    "Domains": ["static.example.com"],
    "StartWithWL": ["/static/"],
    "StartWithBL": ["/static/generated/"]
  }]
}`)
	cfg, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(Lemmingd); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:18080" {
		t.Fatalf("legacy wildcard address was not secured: %q", cfg.Listen)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Target != "127.0.0.1:8000" {
		t.Fatalf("legacy rules not converted: %+v", cfg.Rules)
	}
}

func TestRemoteListenRequiresOptIn(t *testing.T) {
	cfg := Default(Hamsterd, t.TempDir())
	cfg.Listen = ":8080"
	if err := cfg.Validate(Hamsterd); err == nil {
		t.Fatal("expected non-loopback listen address to be rejected")
	}
	cfg.AllowRemoteClients = true
	if err := cfg.Validate(Hamsterd); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPathsAndDefaults(t *testing.T) {
	root := t.TempDir()
	var configHome, cacheHome string
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", root)
		configHome = filepath.Join(root, "Library", "Application Support")
		cacheHome = filepath.Join(root, "Library", "Caches")
	case "windows":
		configHome = filepath.Join(root, "config")
		cacheHome = filepath.Join(root, "cache")
		t.Setenv("AppData", configHome)
		t.Setenv("LocalAppData", cacheHome)
	default:
		configHome = filepath.Join(root, "config")
		cacheHome = filepath.Join(root, "cache")
		t.Setenv("XDG_CONFIG_HOME", configHome)
		t.Setenv("XDG_CACHE_HOME", cacheHome)
	}

	paths, err := DefaultPaths(Lemmingd)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != filepath.Join(configHome, "lemmingd", "config.json") ||
		paths.CACert != filepath.Join(configHome, "lemmingd", "ca.crt") ||
		paths.CAKey != filepath.Join(configHome, "lemmingd", "ca.key") ||
		paths.Cache != filepath.Join(cacheHome, "lemmingd") {
		t.Fatalf("unexpected paths: %+v", paths)
	}

	lemming := Default(Lemmingd, "unused")
	if lemming.Listen != "127.0.0.1:18080" || lemming.Rules == nil {
		t.Fatalf("unexpected lemmingd defaults: %+v", lemming)
	}
	if unknown := Default(Service("unknown"), "unused"); unknown.Version != 1 || unknown.Listen != "" {
		t.Fatalf("unexpected unknown-service defaults: %+v", unknown)
	}
}

func TestDefaultPathsErrors(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("XDG path validation is specific to Unix-like systems")
	}
	t.Run("config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "relative")
		if _, err := DefaultPaths(Hamsterd); err == nil || !strings.Contains(err.Error(), "find user config directory") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("cache", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("XDG_CACHE_HOME", "relative")
		if _, err := DefaultPaths(Hamsterd); err == nil || !strings.Contains(err.Error(), "find user cache directory") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestLoadExistingConfigFillsHamsterDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{
  "version": 1,
  "listen": "127.0.0.1:8080",
  "cache": {}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, created, err := LoadOrCreate(path, Hamsterd, filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing configuration reported as newly created")
	}
	if cfg.Cache.Directory != filepath.Join(dir, "cache") || cfg.Cache.MaxSizeMiB != 1024 ||
		cfg.Cache.MaxObjectMiB != 256 || cfg.Cache.DefaultTTL != "1h" {
		t.Fatalf("cache defaults not populated: %+v", cfg.Cache)
	}
}

func TestLoadOrCreateErrors(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		if _, _, err := LoadOrCreate(t.TempDir(), Hamsterd, t.TempDir()); err == nil || !strings.Contains(err.Error(), "read config") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("parse", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"unknown":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadOrCreate(path, Hamsterd, t.TempDir()); err == nil || !strings.Contains(err.Error(), "parse config") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"version":2,"listen":"127.0.0.1:8080"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadOrCreate(path, Hamsterd, t.TempDir()); err == nil || !strings.Contains(err.Error(), "validate config") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("non-directory parent", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadOrCreate(filepath.Join(parent, "config.json"), Hamsterd, t.TempDir()); err == nil || !strings.Contains(err.Error(), "read config") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestDecodeRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "syntax", data: `{`, want: "unexpected"},
		{name: "unknown field", data: `{"version":1,"surprise":true}`, want: "unknown field"},
		{name: "multiple values", data: `{"version":1} {"version":1}`, want: "multiple JSON values"},
		{name: "trailing syntax", data: `{"version":1} {`, want: "unexpected"},
		{name: "invalid legacy routes", data: `{"FormatVersion":"1","Socket":"127.0.0.1:1","CustomConfig":"bad"}`, want: "parse legacy CustomConfig"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decode([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsInvalidConfigurations(t *testing.T) {
	validCache := Cache{Directory: t.TempDir(), MaxSizeMiB: 2, MaxObjectMiB: 1, DefaultTTL: "1h"}
	tests := []struct {
		name    string
		cfg     Config
		service Service
		want    string
	}{
		{name: "version", cfg: Config{Version: 2}, service: Hamsterd, want: "unsupported config version"},
		{name: "listen format", cfg: Config{Version: 1, Listen: "bad"}, service: Hamsterd, want: "listen must be host:port"},
		{name: "listen port text", cfg: Config{Version: 1, Listen: "127.0.0.1:http"}, service: Hamsterd, want: "listen port"},
		{name: "listen port zero", cfg: Config{Version: 1, Listen: "127.0.0.1:0"}, service: Hamsterd, want: "listen port"},
		{name: "remote listen", cfg: Config{Version: 1, Listen: "192.0.2.1:8080"}, service: Hamsterd, want: "allow_remote_clients"},
		{name: "cache size", cfg: Config{Version: 1, Listen: "localhost:8080", Cache: Cache{}}, service: Hamsterd, want: "max_size_mib"},
		{name: "object size zero", cfg: Config{Version: 1, Listen: "localhost:8080", Cache: Cache{MaxSizeMiB: 2}}, service: Hamsterd, want: "max_object_mib"},
		{name: "object too large", cfg: Config{Version: 1, Listen: "localhost:8080", Cache: Cache{MaxSizeMiB: 1, MaxObjectMiB: 2}}, service: Hamsterd, want: "max_object_mib"},
		{name: "TTL", cfg: Config{Version: 1, Listen: "localhost:8080", Cache: Cache{MaxSizeMiB: 2, MaxObjectMiB: 1, DefaultTTL: "later"}}, service: Hamsterd, want: "default_ttl"},
		{name: "unknown service", cfg: Config{Version: 1, Listen: "localhost:8080", Cache: validCache}, service: Service("other"), want: "unknown service"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.cfg.Validate(test.service)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	remote := Config{Version: 1, Listen: "192.0.2.1:8080", AllowRemoteClients: true, Cache: validCache}
	if err := remote.Validate(Hamsterd); err != nil {
		t.Fatalf("explicit remote listener rejected: %v", err)
	}
	ipv6 := Config{Version: 1, Listen: "[::1]:8080", Cache: validCache}
	if err := ipv6.Validate(Hamsterd); err != nil {
		t.Fatalf("IPv6 loopback rejected: %v", err)
	}
}

func TestValidateLemmingRules(t *testing.T) {
	base := RedirectRule{Target: "127.0.0.1:3000", Domains: []string{"app.example"}, Include: []string{"/"}}
	tests := []struct {
		name        string
		rule        RedirectRule
		allowRemote bool
		want        string
	}{
		{name: "domains", rule: RedirectRule{Target: base.Target, Include: base.Include}, want: "domains"},
		{name: "includes", rule: RedirectRule{Target: base.Target, Domains: base.Domains}, want: "include_paths"},
		{name: "target format", rule: RedirectRule{Target: "localhost", Domains: base.Domains, Include: base.Include}, want: "target must be host:port"},
		{name: "target port", rule: RedirectRule{Target: "localhost:nope", Domains: base.Domains, Include: base.Include}, want: "target port"},
		{name: "remote target", rule: RedirectRule{Target: "192.0.2.1:3000", Domains: base.Domains, Include: base.Include}, want: "allow_remote_targets"},
		{name: "empty domain", rule: RedirectRule{Target: base.Target, Domains: []string{""}, Include: base.Include}, want: "invalid domain"},
		{name: "domain path", rule: RedirectRule{Target: base.Target, Domains: []string{"app.example/path"}, Include: base.Include}, want: "invalid domain"},
		{name: "include prefix", rule: RedirectRule{Target: base.Target, Domains: base.Domains, Include: []string{"api"}}, want: "must start with /"},
		{name: "exclude prefix", rule: RedirectRule{Target: base.Target, Domains: base.Domains, Include: base.Include, Exclude: []string{"api"}}, want: "must start with /"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{Version: 1, Listen: "127.0.0.1:18080", AllowRemoteTargets: test.allowRemote, Rules: []RedirectRule{test.rule}}
			err := cfg.Validate(Lemmingd)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	cfg := Config{
		Version:            1,
		Listen:             "localhost:18080",
		AllowRemoteTargets: true,
		Rules: []RedirectRule{{
			Target: "192.0.2.1:3000", Domains: []string{"app.example"}, Include: []string{"/"},
		}},
	}
	if err := cfg.Validate(Lemmingd); err != nil {
		t.Fatalf("explicit remote target rejected: %v", err)
	}
}

func TestRedirectRuleRejectsMalformedJSON(t *testing.T) {
	var rule RedirectRule
	if err := rule.UnmarshalJSON([]byte(`{"target":`)); err == nil {
		t.Fatal("expected malformed rule to fail")
	}
}
