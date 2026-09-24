package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Service string

const (
	Hamsterd Service = "hamsterd"
	Lemmingd Service = "lemmingd"
)

type Paths struct {
	Config string
	CACert string
	CAKey  string
	Cache  string
}

type Cache struct {
	Directory    string `json:"directory,omitempty"`
	MaxSizeMiB   int64  `json:"max_size_mib"`
	MaxObjectMiB int64  `json:"max_object_mib"`
	DefaultTTL   string `json:"default_ttl"`
}

type RedirectRule struct {
	Target  string   `json:"target"`
	Domains []string `json:"domains"`
	Include []string `json:"include_paths"`
	Exclude []string `json:"exclude_paths,omitempty"`
}

func (r *RedirectRule) UnmarshalJSON(data []byte) error {
	type ruleJSON struct {
		Target        string   `json:"target"`
		Domains       []string `json:"domains"`
		Include       []string `json:"include_paths"`
		Exclude       []string `json:"exclude_paths"`
		LegacyTarget  string   `json:"LocalSocket"`
		LegacyDomains []string `json:"Domains"`
		LegacyInclude []string `json:"StartWithWL"`
		LegacyExclude []string `json:"StartWithBL"`
	}
	var raw ruleJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Target = firstNonEmpty(raw.Target, raw.LegacyTarget)
	r.Domains = firstNonNil(raw.Domains, raw.LegacyDomains)
	r.Include = firstNonNil(raw.Include, raw.LegacyInclude)
	r.Exclude = firstNonNil(raw.Exclude, raw.LegacyExclude)
	return nil
}

type Config struct {
	Version            int            `json:"version"`
	Listen             string         `json:"listen"`
	AllowRemoteClients bool           `json:"allow_remote_clients,omitempty"`
	AllowRemoteTargets bool           `json:"allow_remote_targets,omitempty"`
	Cache              Cache          `json:"cache,omitempty"`
	Rules              []RedirectRule `json:"rules,omitempty"`
}

type fileConfig struct {
	Config
	LegacyFormatVersion string          `json:"FormatVersion"`
	LegacySocket        string          `json:"Socket"`
	LegacyCustom        json.RawMessage `json:"CustomConfig"`
}

func DefaultPaths(service Service) (Paths, error) {
	configBase, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("find user config directory: %w", err)
	}
	cacheBase, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("find user cache directory: %w", err)
	}
	configDir := filepath.Join(configBase, string(service))
	return Paths{
		Config: filepath.Join(configDir, "config.json"),
		CACert: filepath.Join(configDir, "ca.crt"),
		CAKey:  filepath.Join(configDir, "ca.key"),
		Cache:  filepath.Join(cacheBase, string(service)),
	}, nil
}

func Default(service Service, cacheDir string) Config {
	c := Config{Version: 1}
	switch service {
	case Hamsterd:
		c.Listen = "127.0.0.1:8080"
		c.Cache = Cache{
			Directory:    cacheDir,
			MaxSizeMiB:   1024,
			MaxObjectMiB: 256,
			DefaultTTL:   "1h",
		}
	case Lemmingd:
		c.Listen = "127.0.0.1:18080"
		c.Rules = []RedirectRule{}
	}
	return c
}

func LoadOrCreate(path string, service Service, cacheDir string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default(service, cacheDir)
		if err := writeConfig(path, cfg); err != nil {
			return Config{}, false, err
		}
		return cfg, true, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read config %q: %w", path, err)
	}
	cfg, err := decode(data)
	if err != nil {
		return Config{}, false, fmt.Errorf("parse config %q: %w", path, err)
	}
	if service == Hamsterd {
		defaults := Default(Hamsterd, cacheDir).Cache
		if cfg.Cache.Directory == "" {
			cfg.Cache.Directory = defaults.Directory
		}
		if cfg.Cache.MaxSizeMiB == 0 {
			cfg.Cache.MaxSizeMiB = defaults.MaxSizeMiB
		}
		if cfg.Cache.MaxObjectMiB == 0 {
			cfg.Cache.MaxObjectMiB = defaults.MaxObjectMiB
		}
		if cfg.Cache.DefaultTTL == "" {
			cfg.Cache.DefaultTTL = defaults.DefaultTTL
		}
	}
	if err := cfg.Validate(service); err != nil {
		return Config{}, false, fmt.Errorf("validate config %q: %w", path, err)
	}
	return cfg, false, nil
}

func decode(data []byte) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var raw fileConfig
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("multiple JSON values")
		}
		return Config{}, err
	}
	cfg := raw.Config
	if cfg.Version == 0 && raw.LegacyFormatVersion != "" {
		cfg.Version = 1
	}
	if cfg.Listen == "" {
		cfg.Listen = raw.LegacySocket
	}
	if !cfg.AllowRemoteClients && strings.HasPrefix(cfg.Listen, ":") {
		cfg.Listen = "127.0.0.1" + cfg.Listen
	}
	if len(cfg.Rules) == 0 && len(raw.LegacyCustom) > 0 && string(raw.LegacyCustom) != "null" && string(raw.LegacyCustom) != "{}" {
		if err := json.Unmarshal(raw.LegacyCustom, &cfg.Rules); err != nil {
			return Config{}, fmt.Errorf("parse legacy CustomConfig: %w", err)
		}
	}
	return cfg, nil
}

func writeConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("secure config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode default config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install default config: %w", err)
	}
	return nil
}

func (c Config) Validate(service Service) error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if err := validateListen(c.Listen, c.AllowRemoteClients); err != nil {
		return err
	}
	switch service {
	case Hamsterd:
		if c.Cache.MaxSizeMiB <= 0 {
			return errors.New("cache.max_size_mib must be positive")
		}
		if c.Cache.MaxObjectMiB <= 0 || c.Cache.MaxObjectMiB > c.Cache.MaxSizeMiB {
			return errors.New("cache.max_object_mib must be positive and no larger than cache.max_size_mib")
		}
		if _, err := time.ParseDuration(c.Cache.DefaultTTL); err != nil {
			return fmt.Errorf("cache.default_ttl: %w", err)
		}
	case Lemmingd:
		for i := range c.Rules {
			if err := validateRule(c.Rules[i], c.AllowRemoteTargets); err != nil {
				return fmt.Errorf("rules[%d]: %w", i, err)
			}
		}
	default:
		return fmt.Errorf("unknown service %q", service)
	}
	return nil
}

func validateListen(address string, allowRemote bool) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen must be host:port: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("listen port %q is invalid", port)
	}
	if !allowRemote && !isLoopback(host) {
		return errors.New("listen is not loopback; set allow_remote_clients only after adding network access controls")
	}
	return nil
}

func validateRule(rule RedirectRule, allowRemote bool) error {
	if len(rule.Domains) == 0 {
		return errors.New("domains must not be empty")
	}
	if len(rule.Include) == 0 {
		return errors.New("include_paths must not be empty")
	}
	host, port, err := net.SplitHostPort(rule.Target)
	if err != nil || port == "" {
		return errors.New("target must be host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("target port is invalid")
	}
	if !allowRemote && !isLoopback(host) {
		return errors.New("target is not loopback; set allow_remote_targets to permit it")
	}
	for _, domain := range rule.Domains {
		if domain == "" || strings.ContainsAny(domain, "/?#") {
			return fmt.Errorf("invalid domain %q", domain)
		}
	}
	for _, prefix := range append(append([]string{}, rule.Include...), rule.Exclude...) {
		if !strings.HasPrefix(prefix, "/") {
			return fmt.Errorf("path prefix %q must start with /", prefix)
		}
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonNil[T any](values ...[]T) []T {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
