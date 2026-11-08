// Package config loads FileToaster's runtime settings from a YAML file with
// environment-variable overrides, then validates them. Config is loaded once at
// startup and is not mutated at runtime; any on-disk change requires a restart.
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime settings.
type Config struct {
	// Addr is the TCP address the HTTP server binds to.
	Addr string `yaml:"addr"`
	// AllowedPaths are the directories the server may serve. Every file
	// operation must stay within one of these roots.
	AllowedPaths []string `yaml:"allowed_paths"`
	// MaxParallelTasks bounds the task worker pool. Overflow waits in a queue.
	MaxParallelTasks int `yaml:"max_parallel_tasks"`
	// GOMAXPROCS pins runtime.GOMAXPROCS. 0 means use the Go runtime default.
	GOMAXPROCS int `yaml:"gomaxprocs"`
	// TMPDir stages uploads. It is intentionally outside AllowedPaths.
	TMPDir string `yaml:"tmp_dir"`
	// SessionSecret signs the session cookie. Required.
	SessionSecret string `yaml:"session_secret"`
	// AdminPasswordHash is an optional pre-hashed Argon2id admin password.
	AdminPasswordHash string `yaml:"admin_password_hash"`

	// Debug enables debug-level logging when true. Defaults to info logging.
	Debug bool `yaml:"debug"`

	// SourcePath records the file this config was loaded from.
	SourcePath string `yaml:"-"`
}

// DefaultConfig returns settings filled with documented defaults.
func DefaultConfig() *Config {
	return &Config{
		Addr:             ":8080",
		MaxParallelTasks: 4,
		GOMAXPROCS:       0,
		TMPDir:           filepath.Join(os.TempDir(), "filetoaster-uploads"),
	}
}

// ResolveConfigPath returns the config file to use and whether it was created.
// Precedence when no explicit path is given:
//  1. FILETOASTER_CONFIG environment variable
//  2. ./config.yaml in the current working directory
//  3. ~/.filetoaster/config.yaml in the home directory
//
// When none of those exist, a default config is written to
// ~/.filetoaster/config.yaml and (thatPath, true, nil) is returned. When an
// explicit path is given it must already exist; a missing explicit path is an
// error and nothing is created.
func ResolveConfigPath(explicit string) (string, bool, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit, false, nil
		}
		return "", false, fmt.Errorf("--config-file %q does not exist", explicit)
	}

	if env := os.Getenv("FILETOASTER_CONFIG"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, false, nil
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("could not determine current working directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(wd, "config.yaml")); err == nil {
		return filepath.Join(wd, "config.yaml"), false, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("could not locate home directory; set FILETOASTER_CONFIG or pass --config-file: %w", err)
	}
	homeCfg := filepath.Join(home, ".filetoaster", "config.yaml")
	if _, err := os.Stat(homeCfg); err == nil {
		return homeCfg, false, nil
	}

	// Nothing found: bootstrap a default config in the home directory.
	if _, err := WriteDefaultConfig(homeCfg); err != nil {
		return "", false, fmt.Errorf("no config file found and could not create one at %q: %w", homeCfg, err)
	}
	return homeCfg, true, nil
}

// WriteDefaultConfig writes a default config to path with a generated session
// secret and a default served root at <home>/filetoaster-data (created if
// needed). It returns the path written.
func WriteDefaultConfig(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	dataDir := filepath.Join(home, "filetoaster-data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("create allowed path %q: %w", dataDir, err)
	}
	tmpDir := filepath.Join(os.TempDir(), "filetoaster-uploads")
	secret, err := generateSecret()
	if err != nil {
		return "", fmt.Errorf("generate session secret: %w", err)
	}
	content := fmt.Sprintf(`# FileToaster configuration (auto-created on first run).
# The server reads this file once at startup. Edit as needed and restart.
addr: ":8080"
# Directories the server serves. Add or change these to your data folders.
allowed_paths:
  - %s
max_parallel_tasks: 4
# Set to true to enable debug logging (defaults to info).
debug: false
tmp_dir: %s
# Session secret signs the login cookie. A random value was generated here;
# you can also override it via the FILETOASTER_SESSION_SECRET env var.
session_secret: %s
admin_password_hash: ""
`, dataDir, tmpDir, secret)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create config dir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// generateSecret returns a random 32-byte hex string for the session secret.
func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Load reads, parses, applies env overrides, and validates the config at path.
// It returns a human-readable error if the file is missing, malformed, or
// semantically invalid.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config file %q is not readable: %w", path, err)
	}

	cfg := DefaultConfig()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject unknown keys to catch typos early

	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty file: keep defaults and apply env overrides below.
		} else {
			return nil, fmt.Errorf("config file %q is not valid YAML or has the wrong structure: %w", path, err)
		}
	}

	cfg.SourcePath = path
	cfg.applyEnvOverrides()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration in %q: %w", path, err)
	}
	return cfg, nil
}

// applyEnvOverrides lets environment variables override file settings.
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("FILETOASTER_ADDR"); v != "" {
		c.Addr = v
	}
	if v := os.Getenv("FILETOASTER_MAX_TASKS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxParallelTasks = n
		}
	}
	if v := os.Getenv("FILETOASTER_GOMAXPROCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.GOMAXPROCS = n
		}
	}
	if v := os.Getenv("FILETOASTER_TMP_DIR"); v != "" {
		c.TMPDir = v
	}
	if v := os.Getenv("FILETOASTER_SESSION_SECRET"); v != "" {
		c.SessionSecret = v
	}
	if v := os.Getenv("FILETOASTER_ALLOWED_PATHS"); v != "" {
		c.AllowedPaths = splitAndTrim(v)
	}
	if v := os.Getenv("FILETOASTER_DEBUG"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Debug = b
		}
	}
}

// splitAndTrim splits a comma/space-separated env value into non-empty parts.
func splitAndTrim(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Validate checks that all required settings are present and sane. The returned
// error lists every problem found, joined with "; ".
func (c *Config) Validate() error {
	var errs []string

	if c.Addr == "" {
		errs = append(errs, "addr must not be empty")
	} else if _, err := net.ResolveTCPAddr("tcp", c.Addr); err != nil {
		errs = append(errs, fmt.Sprintf("addr %q is not a valid TCP address: %v", c.Addr, err))
	}

	if len(c.AllowedPaths) == 0 {
		errs = append(errs, "allowed_paths must contain at least one directory")
	}
	for _, p := range c.AllowedPaths {
		if p == "" {
			errs = append(errs, "allowed_paths contains an empty entry")
			continue
		}
		info, err := os.Stat(p)
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("allowed_paths entry %q does not exist or is not accessible: %v", p, err))
		case !info.IsDir():
			errs = append(errs, fmt.Sprintf("allowed_paths entry %q is not a directory", p))
		}
	}

	if c.MaxParallelTasks < 1 {
		errs = append(errs, "max_parallel_tasks must be at least 1")
	}
	if c.GOMAXPROCS < 0 {
		errs = append(errs, "gomaxprocs must be >= 0 (0 = Go runtime default)")
	}
	if c.SessionSecret == "" {
		errs = append(errs, "session_secret is required (set FILETOASTER_SESSION_SECRET or config.session_secret)")
	}
	if c.TMPDir == "" {
		errs = append(errs, "tmp_dir must not be empty")
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Save writes the config to path atomically with 0600 permissions, since it may
// contain the session secret.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
