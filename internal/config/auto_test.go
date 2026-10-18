package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAutoRulesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), AutoRulesFile)
	rules, err := LoadAutoRules(path)
	if err != nil || rules != nil {
		t.Fatalf("missing file = (%v, %v)", rules, err)
	}
	want := []RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}
	if err := WriteAutoRules(path, want); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("auto rules mode = %v, %v", info.Mode(), err)
		}
	}
	got, err := LoadAutoRules(path)
	if err != nil || len(got) != 1 || got[0].Target != want[0].Target || got[0].Domains[0] != "app.example" {
		t.Fatalf("loaded = (%+v, %v)", got, err)
	}
	if err := WriteAutoRules(path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty rule list did not remove the file: %v", err)
	}
}

func TestAutoRulesCannotChangeOtherSettings(t *testing.T) {
	for name, content := range map[string]string{
		"listen":            `{"version":1,"listen":"0.0.0.0:18080","rules":[]}`,
		"remote clients":    `{"version":1,"allow_remote_clients":true,"rules":[]}`,
		"remote targets":    `{"version":1,"allow_remote_targets":true,"rules":[]}`,
		"legacy rule field": `{"version":1,"rules":[{"LocalSocket":"127.0.0.1:1","domains":["a"],"include_paths":["/"]}]}`,
		"version":           `{"version":2,"rules":[]}`,
		"trailing data":     `{"version":1,"rules":[]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), AutoRulesFile)
			writeTestFile(t, path, content)
			if _, err := LoadAutoRules(path); err == nil {
				t.Fatal("invalid auto rules file accepted")
			}
		})
	}
}

func TestAutoRulesRejectsTooManyRules(t *testing.T) {
	rules := make([]autoRule, MaxAutoRules+1)
	data, _ := json.Marshal(autoRules{Version: 1, Rules: rules})
	path := filepath.Join(t.TempDir(), AutoRulesFile)
	writeTestFile(t, path, string(data))
	if _, err := LoadAutoRules(path); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRulesRejectsDuplicateDomains(t *testing.T) {
	rules := []RedirectRule{
		{Target: "127.0.0.1:1", Domains: []string{"App.Example"}, Include: []string{"/"}},
		{Target: "127.0.0.1:2", Domains: []string{"app.example."}, Include: []string{"/"}},
	}
	if err := ValidateRules(rules, false); err == nil || !strings.Contains(err.Error(), "already routed") {
		t.Fatalf("error = %v", err)
	}
	if err := ValidateRules(rules[:1], false); err != nil {
		t.Fatal(err)
	}
}

func TestMergeAutoRules(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	original := `{"version":1,"listen":"127.0.0.1:18080","rules":[{"target":"127.0.0.1:3000","domains":["api.example"],"include_paths":["/"]}]}`
	writeTestFile(t, configPath, original)
	if merged, err := MergeAutoRules(configPath); err != nil || merged != 0 {
		t.Fatalf("merge without auto rules = (%d, %v)", merged, err)
	}
	if err := WriteAutoRules(AutoRulesPath(configPath), []RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}); err != nil {
		t.Fatal(err)
	}
	merged, err := MergeAutoRules(configPath)
	if err != nil || merged != 1 {
		t.Fatalf("merge = (%d, %v)", merged, err)
	}
	cfg, _, err := LoadOrCreate(configPath, Lemmingd, "")
	if err != nil || len(cfg.Rules) != 2 || cfg.Rules[1].Domains[0] != "app.example" || cfg.Listen != "127.0.0.1:18080" {
		t.Fatalf("merged config = (%+v, %v)", cfg, err)
	}
	if backup, err := os.ReadFile(configPath + ".bak"); err != nil || string(backup) != original {
		t.Fatalf("backup = (%q, %v)", backup, err)
	}
	if _, err := os.Stat(AutoRulesPath(configPath)); !os.IsNotExist(err) {
		t.Fatalf("auto rules file remains: %v", err)
	}
	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), `"cache"`) {
		t.Fatalf("lemmingd config contains a hamsterd cache block: %s", data)
	}
}

func TestMergeAutoRulesRejectsConflictWithoutChanges(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	original := `{"version":1,"listen":"127.0.0.1:18080","rules":[{"target":"127.0.0.1:3000","domains":["app.example"],"include_paths":["/"]}]}`
	writeTestFile(t, configPath, original)
	autoPath := AutoRulesPath(configPath)
	if err := WriteAutoRules(autoPath, []RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeAutoRules(configPath); err == nil {
		t.Fatal("conflicting merge succeeded")
	}
	if data, _ := os.ReadFile(configPath); string(data) != original {
		t.Fatalf("config changed: %s", data)
	}
	if _, err := os.Stat(autoPath); err != nil {
		t.Fatalf("auto rules removed after failed merge: %v", err)
	}
	if _, err := os.Stat(configPath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("backup written for failed merge: %v", err)
	}
}

func TestControlAPIIsLemmingdOnly(t *testing.T) {
	cfg := Default(Hamsterd, t.TempDir())
	cfg.ControlAPI = true
	if err := cfg.Validate(Hamsterd); err == nil || !strings.Contains(err.Error(), "only by lemmingd") {
		t.Fatalf("error = %v", err)
	}
	lemming := Default(Lemmingd, "")
	if lemming.ControlAPI {
		t.Fatal("control API is enabled by default")
	}
	lemming.ControlAPI = true
	if err := lemming.Validate(Lemmingd); err != nil {
		t.Fatal(err)
	}
}
