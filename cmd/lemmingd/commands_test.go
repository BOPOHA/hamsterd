package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BOPOHA/hamsterd/internal/config"
	"github.com/BOPOHA/hamsterd/internal/control"
	"github.com/BOPOHA/hamsterd/internal/firefox"
	"github.com/BOPOHA/hamsterd/internal/lemming"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigShowAndMergeOffline(t *testing.T) {
	configPath := writeConfig(t, `{"version":1,"listen":"127.0.0.1:18080","rules":[{"target":"127.0.0.1:3000","domains":["api.example"],"include_paths":["/"]}]}`)
	if err := config.WriteAutoRules(config.AutoRulesPath(configPath), []config.RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"config", "-config", configPath, "show"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show exit code = %d, stderr = %s", code, stderr.String())
	}
	var shown struct {
		RuleSources []string      `json:"rule_sources"`
		Effective   config.Config `json:"effective"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &shown); err != nil {
		t.Fatalf("show output is not JSON: %v\n%s", err, stdout.String())
	}
	if strings.Join(shown.RuleSources, ",") != "config,auto" || len(shown.Effective.Rules) != 2 {
		t.Fatalf("show = %+v", shown)
	}

	stdout.Reset()
	if code := run([]string{"config", "-config", configPath, "merge"}, &stdout, &stderr); code != 0 {
		t.Fatalf("merge exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "merged 1 rule ") || !strings.Contains(stdout.String(), "config.json.bak") {
		t.Fatalf("merge output = %q", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"config", "-config", configPath, "merge"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "nothing to merge") {
		t.Fatalf("second merge = %d %q", code, stdout.String())
	}
}

func TestConfigAndMCPUsageErrors(t *testing.T) {
	setUserDirs(t)
	for _, args := range [][]string{{"config"}, {"config", "bogus"}, {"config", "show", "extra"}, {"mcp", "extra"}, {"mcp", "-bogus"}} {
		var stderr bytes.Buffer
		if code := run(args, &bytes.Buffer{}, &stderr); code != 2 {
			t.Fatalf("%v exit code = %d, stderr = %s", args, code, stderr.String())
		}
	}
	var stderr bytes.Buffer
	if code := run([]string{"config", "-config", filepath.Join(t.TempDir(), "missing.json"), "show"}, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("show of a missing config exit code = %d", code)
	}
}

func TestRunWithoutControlAPIPublishesNoEndpoint(t *testing.T) {
	configHome, _ := setUserDirs(t)
	address := freeAddress(t)
	oldLaunch := launchFirefox
	t.Cleanup(func() { launchFirefox = oldLaunch })
	stop := errors.New("stop")
	launchFirefox = func(firefox.Config) error {
		if _, err := os.Stat(filepath.Join(configHome, "lemmingd", control.FileName)); !os.IsNotExist(err) {
			t.Errorf("control endpoint published by default: %v", err)
		}
		return stop
	}
	var stderr bytes.Buffer
	if code := run([]string{"firefox", "-listen", address, "https://example.test/"}, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunServesControlAPIAndMergesThroughIt(t *testing.T) {
	configHome, _ := setUserDirs(t)
	dir := filepath.Join(configHome, "lemmingd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	address := freeAddress(t)
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":1,"listen":"`+address+`","control_api":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	oldLaunch := launchFirefox
	t.Cleanup(func() { launchFirefox = oldLaunch })
	stop := errors.New("checks finished")
	launchFirefox = func(firefox.Config) error {
		ctx := context.Background()
		client := control.NewClient(dir, "lemmingd")
		var status lemming.StatusView
		if err := client.Do(ctx, "GET", "/v1/status", nil, &status); err != nil {
			t.Errorf("status: %v", err)
			return stop
		}
		if status.ProxyURL != "http://"+address || status.CAFingerprint == "" {
			t.Errorf("status = %+v", status)
		}
		rule := lemming.RuleRequest{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}
		if err := client.Do(ctx, "POST", "/v1/rules", rule, nil); err != nil {
			t.Errorf("add rule: %v", err)
			return stop
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"config", "-config", configPath, "merge"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "merged 1 rule ") {
			t.Errorf("merge through the running instance = %d %q %q", code, stdout.String(), stderr.String())
		}
		var rules []lemming.RuleView
		if err := client.Do(ctx, "GET", "/v1/rules", nil, &rules); err != nil || len(rules) != 1 || rules[0].Source != lemming.SourceConfig {
			t.Errorf("rules after merge = (%+v, %v)", rules, err)
		}
		return stop
	}
	var stderr bytes.Buffer
	if code := run([]string{"firefox", "https://example.test/"}, &bytes.Buffer{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "control API enabled") {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, control.FileName)); !os.IsNotExist(err) {
		t.Fatalf("control endpoint file remains after exit: %v", err)
	}
	cfg, _, err := config.LoadOrCreate(configPath, config.Lemmingd, "")
	if err != nil || len(cfg.Rules) != 1 || !cfg.ControlAPI {
		t.Fatalf("config after merge = (%+v, %v)", cfg, err)
	}
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
