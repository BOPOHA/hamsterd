package lemming

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/BOPOHA/hamsterd/internal/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxMCPLineBytes bounds one inbound JSON-RPC message from the MCP client.
const maxMCPLineBytes = 1 << 20

const mcpInstructions = `lemmingd is a local HTTPS development proxy. A browser configured to use it
sees a real site, for example https://app.example.com/, while selected paths are
served by local development servers such as 127.0.0.1:5173.

These tools control the lemmingd instance that is already running on this
machine. Rules from config.json are read-only; rules added here are stored in
rules.auto.json and take effect immediately. Use test_route to check where a URL
goes before and after changing rules, and recent_requests to see what the
browser actually requested.`

// NewMCPServer returns an MCP server whose tools call the control API
// published in configDir.
func NewMCPServer(configDir, version string) *mcp.Server {
	client := control.NewClient(configDir, "lemmingd")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "lemmingd",
		Title:   "lemmingd local development proxy",
		Version: version,
	}, &mcp.ServerOptions{Instructions: mcpInstructions})

	call := func(ctx context.Context, method, path string, in, out any) error {
		err := client.Do(ctx, method, path, in, out)
		if errors.Is(err, control.ErrNotRunning) {
			return fmt.Errorf(`lemmingd is not running with its control API. Start lemmingd (or "lemmingd firefox URL") with "control_api": true in %s/config.json`, configDir)
		}
		return err
	}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Title:       "Proxy status",
		Description: "Report the running lemmingd: proxy address to configure in a browser, version, CA certificate file and fingerprint, remote-access settings, and rule counts.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, StatusView, error) {
		var status StatusView
		err := call(ctx, "GET", "/v1/status", nil, &status)
		return nil, status, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_rules",
		Title:       "List routing rules",
		Description: "List the routing rules in order. source is \"config\" for read-only rules from config.json and \"auto\" for rules added with add_rule.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, rulesOutput, error) {
		var rules []RuleView
		err := call(ctx, "GET", "/v1/rules", nil, &rules)
		return nil, rulesOutput{Rules: rules}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_route",
		Title:       "Explain a URL's route",
		Description: "Explain whether lemmingd intercepts a URL and whether it is served by a local target or by the real server, including the exact target URL.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input routeInput) (*mcp.CallToolResult, RouteView, error) {
		var route RouteView
		err := call(ctx, "POST", "/v1/route", RouteRequest(input), &route)
		return nil, route, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:  "recent_requests",
		Title: "Recent proxied requests",
		Description: "List recent requests that lemmingd could see, newest first, with the local target when a rule routed them. " +
			"These are requests to domains listed in a rule and plain HTTP requests; HTTPS to other domains is tunneled unchanged and not visible. Query strings are not recorded.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input recentInput) (*mcp.CallToolResult, requestsOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 50
		}
		var requests []RequestView
		err := call(ctx, "GET", "/v1/requests?limit="+url.QueryEscape(fmt.Sprint(limit)), nil, &requests)
		return nil, requestsOutput{Requests: requests}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:  "add_rule",
		Title: "Add a routing rule",
		Description: "Route paths of real domains to a local target, live and persisted in rules.auto.json. " +
			"Targets are host:port, http://host:port, or https://host:port with no path; the request path is forwarded unchanged. " +
			"Targets must be loopback unless config.json sets allow_remote_targets. A domain can appear in only one rule.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RuleRequest) (*mcp.CallToolResult, RuleView, error) {
		var added RuleView
		err := call(ctx, "POST", "/v1/rules", input, &added)
		return nil, added, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remove_rule",
		Title:       "Remove a routing rule",
		Description: "Remove the rule added with add_rule that routes the given domain. Rules from config.json cannot be removed here.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input removeInput) (*mcp.CallToolResult, RuleView, error) {
		var removed RuleView
		err := call(ctx, "DELETE", "/v1/rules?domain="+url.QueryEscape(input.Domain), nil, &removed)
		return nil, removed, err
	})
	return server
}

// ServeMCP serves the MCP server over standard input and output.
func ServeMCP(ctx context.Context, server *mcp.Server) error {
	return server.Run(ctx, &mcp.StdioTransport{MaxLineLength: maxMCPLineBytes})
}

type rulesOutput struct {
	Rules []RuleView `json:"rules"`
}

type requestsOutput struct {
	Requests []RequestView `json:"requests"`
}

type routeInput struct {
	URL string `json:"url" jsonschema:"absolute http or https URL as the browser would request it"`
}

type recentInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum number of requests to return, 1-200; default 50"`
}

type removeInput struct {
	Domain string `json:"domain" jsonschema:"a domain listed in the auto rule to remove"`
}
