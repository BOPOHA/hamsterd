package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// AutoRulesFile is the name of the file, next to config.json, that holds
// lemmingd rules added through the control API. Like PostgreSQL's
// postgresql.auto.conf, it keeps machine-made changes out of the
// human-edited configuration. It can contain only rules, so it cannot change
// listen addresses or remote-access settings.
const AutoRulesFile = "rules.auto.json"

// MaxAutoRules bounds the number of rules the control API may add.
const MaxAutoRules = 100

type autoRules struct {
	Version int        `json:"version"`
	Rules   []autoRule `json:"rules"`
}

// autoRule mirrors RedirectRule without its legacy field names.
type autoRule struct {
	Target  string   `json:"target"`
	Domains []string `json:"domains"`
	Include []string `json:"include_paths"`
	Exclude []string `json:"exclude_paths,omitempty"`
}

// AutoRulesPath returns the auto rules file for a configuration file.
func AutoRulesPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), AutoRulesFile)
}

// LoadAutoRules reads the auto rules file. A missing file has no rules.
func LoadAutoRules(path string) ([]RedirectRule, error) {
	data, err := readPrivate(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file autoRules
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %q: unexpected data after the rules document", path)
	}
	if file.Version != 1 {
		return nil, fmt.Errorf("parse %q: unsupported version %d", path, file.Version)
	}
	if len(file.Rules) > MaxAutoRules {
		return nil, fmt.Errorf("parse %q: more than %d rules", path, MaxAutoRules)
	}
	rules := make([]RedirectRule, 0, len(file.Rules))
	for _, rule := range file.Rules {
		rules = append(rules, RedirectRule(rule))
	}
	return rules, nil
}

// WriteAutoRules atomically replaces the auto rules file. An empty list
// removes the file.
func WriteAutoRules(path string, rules []RedirectRule) error {
	if len(rules) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %q: %w", path, err)
		}
		return nil
	}
	file := autoRules{Version: 1, Rules: make([]autoRule, 0, len(rules))}
	for _, rule := range rules {
		file.Rules = append(file.Rules, autoRule(rule))
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auto rules: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

// ValidateRules checks rules against a configuration's remote-target setting
// and rejects a domain that appears in more than one rule.
func ValidateRules(rules []RedirectRule, allowRemoteTargets bool) error {
	seen := make(map[string]struct{})
	for i, rule := range rules {
		if err := ValidateRule(rule, allowRemoteTargets); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
		for _, domain := range rule.Domains {
			key := NormalizeDomain(domain)
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("rules[%d]: domain %q is already routed by another rule", i, domain)
			}
			seen[key] = struct{}{}
		}
	}
	return nil
}

// ErrInvalidMerge reports that merging would produce an invalid
// configuration. Neither file is changed.
var ErrInvalidMerge = errors.New("merged configuration is invalid")

// MergeResult describes a completed merge.
type MergeResult struct {
	// Merged is the number of rules moved into config.json.
	Merged int
	// Backup is the copy of the previous config.json, empty when nothing
	// was merged.
	Backup string
}

// MergeAutoRules appends the auto rules to config.json and removes the auto
// rules file. The previous config.json is kept as config.json.bak, replacing
// an older backup. The merged file is rewritten in canonical form, keeps its
// permissions, and replaces the target of a symbolic link rather than the
// link. If verify is not nil, it is called with the configuration read from
// disk and can refuse the merge. Callers must ensure that no running
// lemmingd is managing the auto rules file, or ask it to merge instead.
func MergeAutoRules(configPath string, verify func(onDisk Config) error) (MergeResult, error) {
	autoPath := AutoRulesPath(configPath)
	auto, err := LoadAutoRules(autoPath)
	if err != nil {
		return MergeResult{}, err
	}
	if len(auto) == 0 {
		return MergeResult{}, nil
	}
	target, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		return MergeResult{}, fmt.Errorf("read config %q: %w", configPath, err)
	}
	data, err := readPrivate(target)
	if err != nil {
		return MergeResult{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return MergeResult{}, fmt.Errorf("inspect config %q: %w", configPath, err)
	}
	cfg, err := decode(data)
	if err != nil {
		return MergeResult{}, fmt.Errorf("parse config %q: %w", configPath, err)
	}
	if verify != nil {
		if err := verify(cfg); err != nil {
			return MergeResult{}, err
		}
	}
	cfg.Rules = append(cfg.Rules, auto...)
	if err := cfg.Validate(Lemmingd); err != nil {
		return MergeResult{}, fmt.Errorf("%w: %w", ErrInvalidMerge, err)
	}
	if err := ValidateRules(cfg.Rules, cfg.AllowRemoteTargets); err != nil {
		return MergeResult{}, fmt.Errorf("%w: %w", ErrInvalidMerge, err)
	}
	merged, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return MergeResult{}, fmt.Errorf("encode merged config: %w", err)
	}
	backup := configPath + ".bak"
	if err := writeFileAtomic(backup, data, 0o600); err != nil {
		return MergeResult{}, fmt.Errorf("back up config: %w", err)
	}
	// Unlike writeConfig for a new file, do not change the permissions of
	// the directory, which -config may point to.
	if err := writeFileAtomic(target, append(merged, '\n'), info.Mode().Perm()&^0o022); err != nil {
		return MergeResult{}, err
	}
	if err := WriteAutoRules(autoPath, nil); err != nil {
		return MergeResult{}, err
	}
	return MergeResult{Merged: len(auto), Backup: backup}, nil
}

// writeFileAtomic atomically writes a file with mode perm in an existing
// directory.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary file for %q: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install %q: %w", path, err)
	}
	return nil
}
