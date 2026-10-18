package lemming

import (
	"errors"
	"os"
	"path/filepath"
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
		domain := "app" + strings.Repeat("x", i) + ".example"
		if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{domain}, Include: []string{"/"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.AddRule(config.RedirectRule{Target: "127.0.0.1:5173", Domains: []string{"one-more.example"}, Include: []string{"/"}}); err == nil {
		t.Fatal("rule limit not enforced")
	}
}

func TestNewLoadsAutoRulesAndRejectsConflicts(t *testing.T) {
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
	if _, err := New(configPath, cfg, Info{}); err == nil || !strings.Contains(err.Error(), config.AutoRulesFile) {
		t.Fatalf("conflict error = %v", err)
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
	if err != nil || merged != 1 {
		t.Fatalf("Merge = (%d, %v)", merged, err)
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
