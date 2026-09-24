package config

import (
	"os"
	"path/filepath"
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
