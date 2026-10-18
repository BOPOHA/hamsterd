package lemming

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BOPOHA/hamsterd/internal/config"
	"github.com/BOPOHA/hamsterd/internal/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectMCP(t *testing.T, configDir string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := NewMCPServer(configDir, "test")
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments any) (*mcp.CallToolResult, string) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return result, text.String()
}

func TestMCPToolsControlRunningService(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	service, err := New(configPath, config.Config{Version: 1, Listen: "127.0.0.1:18080", Rules: []config.RedirectRule{configRule}}, Info{Version: "test", ProxyURL: "http://127.0.0.1:18080"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := control.Start(dir, "lemmingd", service.Handler(), log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	session := connectMCP(t, dir)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "add_rule,list_rules,recent_requests,remove_rule,status,test_route" {
		t.Fatalf("tools = %v", names)
	}

	result, text := callTool(t, session, "status", map[string]any{})
	if result.IsError || !strings.Contains(text, `"proxy_url":"http://127.0.0.1:18080"`) {
		t.Fatalf("status = %s", text)
	}
	result, text = callTool(t, session, "add_rule", map[string]any{
		"target": "127.0.0.1:5173", "domains": []string{"app.example"}, "include_paths": []string{"/"}, "exclude_paths": []string{"/api/"},
	})
	if result.IsError {
		t.Fatalf("add_rule = %s", text)
	}
	result, text = callTool(t, session, "test_route", map[string]any{"url": "https://app.example/index.html"})
	var route RouteView
	if result.IsError || json.Unmarshal([]byte(text), &route) != nil || !route.Routed || route.Target != "127.0.0.1:5173" {
		t.Fatalf("test_route = %s", text)
	}
	result, text = callTool(t, session, "add_rule", map[string]any{
		"target": "192.0.2.1:443", "domains": []string{"remote.example"}, "include_paths": []string{"/"},
	})
	if !result.IsError || !strings.Contains(text, "allow_remote_targets") || strings.Contains(text, "rules[") {
		t.Fatalf("remote add_rule = %v %s", result.IsError, text)
	}
	result, text = callTool(t, session, "remove_rule", map[string]any{"domain": "api.example"})
	if !result.IsError || !strings.Contains(text, "config.json") {
		t.Fatalf("remove config rule = %v %s", result.IsError, text)
	}
	result, text = callTool(t, session, "list_rules", map[string]any{})
	if result.IsError || strings.Count(text, `"source"`) != 2 {
		t.Fatalf("list_rules = %s", text)
	}
	service.Record(RequestView{Method: "GET", Host: "app.example", Path: "/", Target: "127.0.0.1:5173", Status: 200})
	result, text = callTool(t, session, "recent_requests", map[string]any{"limit": 5})
	if result.IsError || !strings.Contains(text, `"target":"127.0.0.1:5173"`) {
		t.Fatalf("recent_requests = %s", text)
	}
	result, text = callTool(t, session, "remove_rule", map[string]any{"domain": "app.example"})
	if result.IsError {
		t.Fatalf("remove_rule = %s", text)
	}
}

func TestMCPReportsNotRunning(t *testing.T) {
	session := connectMCP(t, t.TempDir())
	result, text := callTool(t, session, "status", map[string]any{})
	if !result.IsError || !strings.Contains(text, `"control_api": true`) {
		t.Fatalf("status without lemmingd = %v %s", result.IsError, text)
	}
}
