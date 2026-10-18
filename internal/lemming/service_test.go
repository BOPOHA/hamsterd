package lemming

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BOPOHA/hamsterd/internal/config"
)

func newTestService(t *testing.T, cfg config.Config) (*Service, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if cfg.Version == 0 {
		cfg.Version, cfg.Listen = 1, "127.0.0.1:18080"
	}
	service, err := New(configPath, cfg, Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return service, configPath
}

var configRule = config.RedirectRule{Target: "127.0.0.1:3000", Domains: []string{"api.example"}, Include: []string{"/"}}

func TestAddAndRemoveRuleApplyLiveAndPersist(t *testing.T) {
	service, configPath := newTestService(t, config.Config{Version: 1, Listen: "127.0.0.1:18080", Rules: []config.RedirectRule{configRule}})
	if service.Router().Intercepts("app.example:443") {
		t.Fatal("unconfigured domain intercepted")
	}
	added, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}})
	if err != nil || added.Source != SourceAuto {
		t.Fatalf("AddRule = (%+v, %v)", added, err)
	}
	if !service.Router().Intercepts("app.example:443") || !service.trusted.HasTarget("127.0.0.1:5173") {
		t.Fatal("added rule is not live")
	}
	saved, err := config.LoadAutoRules(config.AutoRulesPath(configPath))
	if err != nil || len(saved) != 1 || saved[0].Target != "127.0.0.1:5173" {
		t.Fatalf("persisted rules = (%+v, %v)", saved, err)
	}
	rules := service.Rules()
	if len(rules) != 2 || rules[0].Source != SourceConfig || rules[1].Source != SourceAuto {
		t.Fatalf("rules = %+v", rules)
	}

	if _, err := service.RemoveRule("api.example"); !errors.Is(err, ErrConfigRule) {
		t.Fatalf("removing a config rule: %v", err)
	}
	if _, err := service.RemoveRule("missing.example"); !errors.Is(err, ErrNoRule) {
		t.Fatalf("removing a missing rule: %v", err)
	}
	if _, err := service.RemoveRule("APP.example."); err != nil {
		t.Fatal(err)
	}
	if service.Router().Intercepts("app.example:443") || service.trusted.HasTarget("127.0.0.1:5173") {
		t.Fatal("removed rule is still live")
	}
	if _, err := os.Stat(config.AutoRulesPath(configPath)); !os.IsNotExist(err) {
		t.Fatalf("auto rules file remains: %v", err)
	}
}

func TestAddRuleEnforcesConfiguration(t *testing.T) {
	service, configPath := newTestService(t, config.Config{Version: 1, Listen: "127.0.0.1:18080", Rules: []config.RedirectRule{configRule}})
	for name, rule := range map[string]config.RedirectRule{
		"remote target":    {Target: "192.0.2.10:443", Domains: []string{"app.example"}, Include: []string{"/"}},
		"duplicate domain": {Target: "127.0.0.1:5173", Domains: []string{"API.example"}, Include: []string{"/"}},
		"target path":      {Target: "https://127.0.0.1:5173/app", Domains: []string{"app.example"}, Include: []string{"/"}},
		"no domains":       {Target: "127.0.0.1:5173", Include: []string{"/"}},
		"relative path":    {Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"assets/"}},
	} {
		if _, err := service.AddRule(rule); err == nil {
			t.Fatalf("%s: rule accepted", name)
		}
	}
	if _, err := os.Stat(config.AutoRulesPath(configPath)); !os.IsNotExist(err) {
		t.Fatalf("rejected rules were written: %v", err)
	}

	remote, _ := newTestService(t, config.Config{Version: 1, Listen: "127.0.0.1:18080", AllowRemoteTargets: true})
	if _, err := remote.AddRule(config.RedirectRule{Target: "staging.example:443", Domains: []string{"app.example"}, Include: []string{"/"}}); err != nil {
		t.Fatalf("remote target rejected with allow_remote_targets: %v", err)
	}
}

func TestAddRuleLimit(t *testing.T) {
	service, _ := newTestService(t, config.Config{})
	for i := range config.MaxAutoRules {
		domain := "app" + strconv.Itoa(i) + ".example"
		if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{domain}, Include: []string{"/"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{"one-more.example"}, Include: []string{"/"}}); err == nil {
		t.Fatal("rule limit not enforced")
	}
}

func TestNewLoadsAutoRulesAndSkipsConflicts(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.WriteAutoRules(config.AutoRulesPath(configPath), []config.RedirectRule{{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Version: 1, Listen: "127.0.0.1:18080", Rules: []config.RedirectRule{configRule}}
	service, err := New(configPath, cfg, Info{})
	if err != nil || service.AutoRuleCount() != 1 || !service.Router().Intercepts("app.example") {
		t.Fatalf("New = (%v, %v)", service, err)
	}
	cfg.Rules = append(cfg.Rules, config.RedirectRule{Target: "127.0.0.1:4000", Domains: []string{"app.example"}, Include: []string{"/"}})
	service, err = New(configPath, cfg, Info{})
	if err != nil {
		t.Fatalf("invalid auto rules prevented startup: %v", err)
	}
	if err := service.AutoRulesError(); err == nil || !strings.Contains(err.Error(), config.AutoRulesFile) {
		t.Fatalf("AutoRulesError = %v", err)
	}
	if service.AutoRuleCount() != 0 || len(service.Rules()) != 2 || service.Status().AutoRulesError == "" {
		t.Fatalf("service with skipped auto rules = %+v", service.Status())
	}
	before, _ := os.ReadFile(config.AutoRulesPath(configPath))
	if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5174", Domains: []string{"new.example"}, Include: []string{"/"}}); err == nil {
		t.Fatal("rule added while rules.auto.json is skipped")
	}
	if _, err := service.RemoveRule("app.example"); err == nil {
		t.Fatal("rule removed while rules.auto.json is skipped")
	}
	if _, err := service.Merge(); err == nil {
		t.Fatal("merged while rules.auto.json is skipped")
	}
	if after, _ := os.ReadFile(config.AutoRulesPath(configPath)); string(after) != string(before) {
		t.Fatalf("skipped rules.auto.json changed:\n%s", after)
	}
}

func TestNewSkipsUnreadableAutoRules(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config.AutoRulesPath(configPath), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(configPath, config.Config{Version: 1, Listen: "127.0.0.1:18080", Rules: []config.RedirectRule{configRule}}, Info{})
	if err != nil || service.AutoRulesError() == nil || !service.Router().Intercepts("api.example") {
		t.Fatalf("New = (%v, %v)", service, err)
	}
}

func TestRejectedRuleIsNotSaved(t *testing.T) {
	service, configPath := newTestService(t, config.Config{})
	if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"App.Example.", "app.example:443", "*.example", "app example"} {
		if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5174", Domains: []string{domain}, Include: []string{"/"}}); err == nil {
			t.Fatalf("domain %q accepted", domain)
		}
	}
	saved, err := config.LoadAutoRules(config.AutoRulesPath(configPath))
	if err != nil || len(saved) != 1 {
		t.Fatalf("saved rules = (%+v, %v)", saved, err)
	}
	restarted, err := New(configPath, config.Config{Version: 1, Listen: "127.0.0.1:18080"}, Info{})
	if err != nil || restarted.AutoRulesError() != nil || restarted.AutoRuleCount() != 1 {
		t.Fatalf("restart = (%v, %v)", restarted, err)
	}
}

func TestRouteExplainsDecisions(t *testing.T) {
	service, _ := newTestService(t, config.Config{Version: 1, Listen: "127.0.0.1:18080", Rules: []config.RedirectRule{
		{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}, Exclude: []string{"/api/"}},
	}})
	routed, err := service.Route("https://app.example/assets/app.js?v=2")
	if err != nil || !routed.Routed || routed.TargetURL != "http://127.0.0.1:5173/assets/app.js?v=2" {
		t.Fatalf("routed = (%+v, %v)", routed, err)
	}
	excluded, _ := service.Route("https://app.example/api/users")
	if !excluded.Intercepted || excluded.Routed || excluded.Target != "" {
		t.Fatalf("excluded = %+v", excluded)
	}
	root, _ := service.Route("https://app.example")
	if !root.Routed || root.TargetURL != "http://127.0.0.1:5173/" {
		t.Fatalf("URL without a path = %+v", root)
	}
	other, _ := service.Route("https://other.example/")
	if other.Intercepted || other.Routed {
		t.Fatalf("other = %+v", other)
	}
	for _, bad := range []string{"app.example/", "ftp://app.example/", "https:///path"} {
		if _, err := service.Route(bad); err == nil {
			t.Fatalf("Route(%q) accepted", bad)
		}
	}
}

func TestRecentRequestsRing(t *testing.T) {
	service, _ := newTestService(t, config.Config{})
	for i := range recentRequests + 5 {
		service.Record(RequestView{Path: "/" + strings.Repeat("x", i%3), Status: i})
	}
	recent := service.Recent(0)
	if len(recent) != recentRequests || recent[0].Status != recentRequests+4 || recent[len(recent)-1].Status != 5 {
		t.Fatalf("recent = %d items, first %d, last %d", len(recent), recent[0].Status, recent[len(recent)-1].Status)
	}
	if got := service.Recent(3); len(got) != 3 || got[0].Status != recentRequests+4 {
		t.Fatalf("limited = %+v", got)
	}
}

func TestMergeMovesAutoRulesIntoConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":1,"listen":"127.0.0.1:18080"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadOrCreate(configPath, config.Lemmingd, "")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(configPath, cfg, Info{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}); err != nil {
		t.Fatal(err)
	}
	merged, err := service.Merge()
	if err != nil || merged.Merged != 1 || merged.Backup != configPath+".bak" {
		t.Fatalf("Merge = (%+v, %v)", merged, err)
	}
	rules := service.Rules()
	if len(rules) != 1 || rules[0].Source != SourceConfig || !service.Router().Intercepts("app.example") {
		t.Fatalf("rules after merge = %+v", rules)
	}
	reloaded, _, err := config.LoadOrCreate(configPath, config.Lemmingd, "")
	if err != nil || len(reloaded.Rules) != 1 {
		t.Fatalf("config after merge = (%+v, %v)", reloaded, err)
	}
}

func TestMergeRefusesConfigChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":1,"listen":"127.0.0.1:18080"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadOrCreate(configPath, config.Lemmingd, "")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(configPath, cfg, Info{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{"app.example"}, Include: []string{"/"}}); err != nil {
		t.Fatal(err)
	}
	edited := `{"version":1,"listen":"127.0.0.1:18080","rules":[{"target":"127.0.0.1:3000","domains":["api.example"],"include_paths":["/"]}]}`
	if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Merge(); err == nil || !strings.Contains(err.Error(), "restart lemmingd") {
		t.Fatalf("merge error = %v", err)
	}
	if data, _ := os.ReadFile(configPath); string(data) != edited {
		t.Fatalf("config changed: %s", data)
	}
	if service.AutoRuleCount() != 1 {
		t.Fatal("auto rules dropped after a refused merge")
	}
}

func TestHandlerStatusCodes(t *testing.T) {
	service, _ := newTestService(t, config.Config{Version: 1, Listen: "127.0.0.1:18080", Rules: []config.RedirectRule{configRule}})
	handler := service.Handler()
	for _, test := range []struct {
		method, target, body string
		status               int
	}{
		{"DELETE", "/v1/rules", "", http.StatusBadRequest},
		{"DELETE", "/v1/rules?domain=api.example", "", http.StatusConflict},
		{"DELETE", "/v1/rules?domain=none.example", "", http.StatusNotFound},
		{"POST", "/v1/rules", `{"target":"127.0.0.1:5173","domains":["api.example"],"include_paths":["/"]}`, http.StatusUnprocessableEntity},
		{"POST", "/v1/rules", `{"target":"127.0.0.1:5173","domains":["app.example"],"include_paths":["/"]}`, http.StatusCreated},
		{"POST", "/v1/merge", "", http.StatusInternalServerError}, // config.json does not exist
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, strings.NewReader(test.body)))
		if recorder.Code != test.status {
			t.Fatalf("%s %s = %d %s, want %d", test.method, test.target, recorder.Code, recorder.Body, test.status)
		}
	}
}
