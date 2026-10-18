package config

import (
	"encoding/json"
	"errors"
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
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if merged, err := MergeAutoRules(configPath, nil); err != nil || merged.Merged != 0 || merged.Backup != "" {
		t.Fatalf("merge without auto rules = (%+v, %v)", merged, err)
	}
	if err := WriteAutoRules(AutoRulesPath(configPath), []RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}); err != nil {
		t.Fatal(err)
	}
	merged, err := MergeAutoRules(configPath, nil)
	if err != nil || merged.Merged != 1 || merged.Backup != configPath+".bak" {
		t.Fatalf("merge = (%+v, %v)", merged, err)
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
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("merge changed the directory permissions: %v %v", info.Mode(), err)
		}
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
	if _, err := MergeAutoRules(configPath, nil); !errors.Is(err, ErrInvalidMerge) {
		t.Fatalf("conflicting merge error = %v", err)
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

func TestMergeAutoRulesKeepsSymlinkAndMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links and modes differ on Windows")
	}
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.json")
	writeTestFile(t, realPath, `{"version":1,"listen":"127.0.0.1:18080"}`)
	if err := os.Chmod(realPath, 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.Symlink("real.json", configPath); err != nil {
		t.Fatal(err)
	}
	if err := WriteAutoRules(AutoRulesPath(configPath), []RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeAutoRules(configPath, nil); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(configPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.json is no longer a symbolic link: %v %v", info.Mode(), err)
	}
	if info, err := os.Stat(realPath); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("merged file mode = %v %v", info.Mode(), err)
	}
	if cfg, _, err := LoadOrCreate(configPath, Lemmingd, ""); err != nil || len(cfg.Rules) != 1 {
		t.Fatalf("merged config = (%+v, %v)", cfg, err)
	}
}

func TestMergeAutoRulesVerify(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	original := `{"version":1,"listen":"127.0.0.1:18080"}`
	writeTestFile(t, configPath, original)
	if err := WriteAutoRules(AutoRulesPath(configPath), []RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("refused")
	if _, err := MergeAutoRules(configPath, func(Config) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("verify error = %v", err)
	}
	if data, _ := os.ReadFile(configPath); string(data) != original {
		t.Fatalf("config changed: %s", data)
	}
}

func TestValidDomain(t *testing.T) {
	for _, domain := range []string{"app.example", "localhost", "my_host.internal", "a-b.example", "192.0.2.1", "::1", "xn--80ak6aa92e.com"} {
		if !validDomain(NormalizeDomain(domain)) {
			t.Errorf("%q rejected", domain)
		}
	}
	for _, domain := range []string{"", "app.example:443", "*.example", "app example", "-app.example", "app..example", "[::1]", "app.example/x", strings.Repeat("a", 64) + ".example"} {
		if validDomain(NormalizeDomain(domain)) {
			t.Errorf("%q accepted", domain)
		}
	}
}
