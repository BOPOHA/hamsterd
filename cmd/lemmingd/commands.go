package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/BOPOHA/hamsterd/internal/buildinfo"
	"github.com/BOPOHA/hamsterd/internal/config"
	"github.com/BOPOHA/hamsterd/internal/control"
	"github.com/BOPOHA/hamsterd/internal/lemming"
)

const configUsage = `usage: lemmingd config [-config PATH] COMMAND

Commands:
  show    print the effective configuration: config.json plus rules.auto.json
  merge   move the rules from rules.auto.json into config.json
          (config.json is kept as config.json.bak)
`

// runMCP serves the MCP protocol on standard input and output. It controls
// the lemmingd instance that publishes its control API next to the
// configuration, so it never starts a proxy itself.
func runMCP(args []string, stderr io.Writer) int {
	paths, err := config.DefaultPaths(config.Lemmingd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	flags := flag.NewFlagSet("lemmingd mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", paths.Config, "configuration file of the lemmingd to control")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: lemmingd mcp [-config PATH]")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := lemming.NewMCPServer(filepath.Dir(*configPath), buildinfo.Version)
	if err := lemming.ServeMCP(ctx, server); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "lemmingd mcp: %v\n", err)
		return 1
	}
	return 0
}

func runConfig(args []string, stdout, stderr io.Writer) int {
	paths, err := config.DefaultPaths(config.Lemmingd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	flags := flag.NewFlagSet("lemmingd config", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, configUsage) }
	configPath := flags.String("config", paths.Config, "configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	switch flags.Arg(0) {
	case "show":
		return showConfig(*configPath, stdout, stderr)
	case "merge":
		return mergeConfig(*configPath, stdout, stderr)
	default:
		flags.Usage()
		return 2
	}
}

type effectiveConfig struct {
	ConfigFile    string        `json:"config_file"`
	AutoRulesFile string        `json:"auto_rules_file"`
	RuleSources   []string      `json:"rule_sources"`
	Effective     config.Config `json:"effective"`
}

func showConfig(configPath string, stdout, stderr io.Writer) int {
	if _, err := os.Stat(configPath); err != nil {
		fmt.Fprintf(stderr, "lemmingd config: %v\n", err)
		return 1
	}
	cfg, _, err := config.LoadOrCreate(configPath, config.Lemmingd, "")
	if err != nil {
		fmt.Fprintf(stderr, "lemmingd config: %v\n", err)
		return 1
	}
	autoPath := config.AutoRulesPath(configPath)
	auto, err := config.LoadAutoRules(autoPath)
	if err != nil {
		fmt.Fprintf(stderr, "lemmingd config: %v\n", err)
		return 1
	}
	result := effectiveConfig{ConfigFile: configPath, AutoRulesFile: autoPath, Effective: cfg}
	for range cfg.Rules {
		result.RuleSources = append(result.RuleSources, lemming.SourceConfig)
	}
	for range auto {
		result.RuleSources = append(result.RuleSources, lemming.SourceAuto)
	}
	if result.RuleSources == nil {
		result.RuleSources = []string{}
	}
	result.Effective.Rules = append(result.Effective.Rules, auto...)
	if err := config.ValidateRules(result.Effective.Rules, cfg.AllowRemoteTargets); err != nil {
		fmt.Fprintf(stderr, "lemmingd config: warning: the effective rules are invalid: %v\n", err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "lemmingd config: %v\n", err)
		return 1
	}
	return 0
}

// mergeConfig asks a running lemmingd to merge, so that its in-memory rules
// stay consistent, and edits the files directly when none is running. A
// lemmingd without the control API never writes rules.auto.json, so editing
// the files under it is safe.
func mergeConfig(configPath string, stdout, stderr io.Writer) int {
	client := control.NewClient(filepath.Dir(configPath), "lemmingd")
	var result lemming.MergeView
	err := client.Do(context.Background(), "POST", "/v1/merge", nil, &result)
	if errors.Is(err, control.ErrNotRunning) {
		result.ConfigFile = configPath
		result.Merged, err = config.MergeAutoRules(configPath)
		if result.Merged > 0 {
			result.Backup = configPath + ".bak"
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "lemmingd config merge: %v\n", err)
		return 1
	}
	if result.Merged == 0 {
		fmt.Fprintf(stdout, "no rules in %s; nothing to merge\n", config.AutoRulesPath(configPath))
		return 0
	}
	fmt.Fprintf(stdout, "merged %d rules into %s; previous version saved as %s\n", result.Merged, result.ConfigFile, result.Backup)
	return 0
}
