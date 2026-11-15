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
  "time"

  "filetoaster/internal/auth"

  "gopkg.in/yaml.v3"
)

// AllowedPath is a single directory the server may serve. It is rendered on the
// frontend as one folder icon: Name is the label shown on the icon, and Path is
// the directory opened when the icon is clicked.
//
// Exactly one source must identify the directory:
//   - Path: an absolute path to an existing directory.
//   - Env:  the name of an environment variable whose value is a single
//     absolute directory path.
//
// At least one of Path or Env must be set. If both are set, Path takes
// precedence. Name is mandatory.
type AllowedPath struct {
  // Name is the label shown on the folder icon. Mandatory.
  Name string `yaml:"name"`
  // Path is an absolute directory path. Optional; ignored when Env is set and
  // takes precedence over Env when both are provided.
  Path string `yaml:"path"`
  // Env is an environment variable whose value is resolved to an absolute
  // directory path. Optional; used only when Path is empty.
  Env string `yaml:"env"`
}

// Config holds all runtime settings.
type Config struct {
  // Addr is the TCP address the HTTP server binds to.
  Addr string `yaml:"addr"`
  // AllowedPaths are the directories the server may serve. Every file
  // operation must stay within one of these roots. Each entry carries a
  // display Name and the directory itself, supplied as an absolute Path or
  // resolved from an Env variable. See AllowedPath for the rules.
  AllowedPaths []AllowedPath `yaml:"allowed_paths"`
  // MaxParallelTasks bounds the task worker pool. Overflow waits in a queue.
  MaxParallelTasks int `yaml:"max_parallel_tasks"`
  // ShutdownTimeout bounds graceful shutdown, in seconds: how long the pool
  // keeps running while existing operations finish after a shutdown signal
  // before it forcibly cancels them and the server exits. 0 disables the drain
  // and cancels running tasks immediately. Defaults to 30.
  ShutdownTimeout int `yaml:"shutdown_timeout_seconds"`
  // GOMAXPROCS pins runtime.GOMAXPROCS. 0 means use the Go runtime default.
  GOMAXPROCS int `yaml:"gomaxprocs"`
  // TMPDir stages uploads. It is intentionally outside AllowedPaths. It
  // defaults to <home>/.filetoaster/tmp_uploads, which is created on start if
  // absent; a value set in the config or via FILETOASTER_TMP_DIR is never
  // auto-created and must already exist (enforced at startup).
  TMPDir string `yaml:"tmp_dir"`
  // UploadStaleAfter is how many hours a staged upload dir is tolerated before
  // the background sweep removes it (and its in-memory session). Must be at
  // least 1 hour; 0 or negative makes the sweep remove every staged upload at
  // once, which defeats resume.
  UploadStaleAfter int `yaml:"upload_stale_after"`
  // UploadSweepInterval is how often the background sweep runs, in hours.
  UploadSweepInterval int `yaml:"upload_sweep_interval"`
  // UploadChunkSize is the fixed per-file chunk size (in bytes) the server
  // advertises for chunked uploads. The client slices every file with this
  // value and re-sends only missing chunks, so the client and server must agree;
  // the client reads it from GET /api/config. Must be positive.
  UploadChunkSize int64 `yaml:"upload_chunk_size"`
  // UploadMaxConcurrentChunks bounds how many chunks a client uploads at once,
  // across every file and every active session (a global limit, not per file).
  // It caps the number of simultaneous upload requests the browser opens, which
  // avoids resource-exhaustion failures (net::ERR_INSUFFICIENT_RESOURCES) when
  // uploading large files. Must be at least 1.
  UploadMaxConcurrentChunks int `yaml:"upload_max_concurrent_chunks"`
  // SessionSecret signs the session cookie. Required.
  SessionSecret string `yaml:"session_secret"`
  // AdminUser is the admin username accepted at login. Empty falls back to
  // auth.DefaultUser ("admin").
  AdminUser string `yaml:"admin_user"`
  // AdminPasswordHash is an Argon2id hash of the admin password. An empty
  // value means the default admin password (auth.DefaultPassword) is used.
  // FILETOASTER_ADMIN_PASSWORD (env) always takes precedence over this field.
  AdminPasswordHash string `yaml:"admin_password_hash"`

  // Debug enables debug-level logging when true. Defaults to info logging.
  Debug bool `yaml:"debug"`

  // SourcePath records the file this config was loaded from.
  SourcePath string `yaml:"-"`
}

// EnsureTmpDirMode is the permission used when the server creates its built-in
// default upload staging directory: owner read/write/execute only, so a local
// non-owner process cannot list, read, or write staged uploads.
const EnsureTmpDirMode = 0o700

// SecureModes holds the permission bits the server requires for its sensitive
// on-disk state. The config file holds the session secret and admin hash and is
// owner read/write only; group and other must have no access.
const (
  // ConfigFileMode is the required permission for the config file.
  ConfigFileMode = 0o600
  // TmpDirMode is the required permission for the upload staging directory.
  TmpDirMode = 0o700
)

// DefaultConfig returns settings filled with documented defaults.
func DefaultConfig() *Config {
  return &Config{
    Addr:                      ":8080",
    MaxParallelTasks:          4,
    ShutdownTimeout:           30,
    GOMAXPROCS:                0,
    TMPDir:                    defaultTmpDir(),
    UploadStaleAfter:          24,
    UploadSweepInterval:       1,
    UploadChunkSize:           4 << 20, // 4 MiB
    UploadMaxConcurrentChunks: 4,
  }
}

// defaultTmpDir is the built-in upload staging directory: <home>/.filetoaster/tmp_uploads.
// It is only used when the operator does not set tmp_dir in the config or via the
// FILETOASTER_TMP_DIR env var; that default location is auto-created on start if
// absent, unlike a user-provided tmp_dir which must already exist.
func defaultTmpDir() string {
  home, err := os.UserHomeDir()
  if err != nil {
    return filepath.Join(os.TempDir(), "filetoaster-uploads")
  }
  return filepath.Join(home, ".filetoaster", "tmp_uploads")
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
// secret, a default served root at <home>/filetoaster-data (created if needed),
// and the default admin credentials (admin / hashed "admin"). It returns the
// path written.
func WriteDefaultConfig(path string) (string, error) {
  home, err := os.UserHomeDir()
  if err != nil {
    return "", fmt.Errorf("locate home directory: %w", err)
  }
  dataDir := filepath.Join(home, "filetoaster-data")
  if err := os.MkdirAll(dataDir, 0o755); err != nil {
    return "", fmt.Errorf("create allowed path %q: %w", dataDir, err)
  }
  tmpDir := defaultTmpDir()
  secret, err := generateSecret()
  if err != nil {
    return "", fmt.Errorf("generate session secret: %w", err)
  }
  adminHash, err := auth.HashPassword(auth.DefaultPassword)
  if err != nil {
    return "", fmt.Errorf("hash default admin password: %w", err)
  }
  content := fmt.Sprintf(`# FileToaster configuration (auto-created on first run).
# The server reads this file once at startup. Edit as needed and restart.
addr: ":8080"
# Directories the server serves. Each entry has a display name and either an
# absolute path or an env var name (resolved to a single absolute directory).
allowed_paths:
    - name: Files
      path: %s
max_parallel_tasks: 4
# Graceful shutdown: seconds to let running operations finish after a shutdown
# signal before forcibly cancelling them and exiting the server (0 = immediate).
shutdown_timeout_seconds: 30
# Set to true to enable debug logging (defaults to info).
debug: false
tmp_dir: %s
# How many hours a staged upload is tolerated before the background sweep
# removes its staging dir and session. Must be at least 1 hour; 0 or negative
# makes the sweep remove every staged upload at once (defeating resume).
# Default 24.
upload_stale_after: 24
# How often the background upload sweep runs, in hours. Default 1.
upload_sweep_interval: 1
# Fixed size, in bytes, of each per-file upload chunk. The client slices files
# with this value and re-sends only missing chunks, so the client and server
# must agree (the client reads it from GET /api/config). Default 4 MiB.
upload_chunk_size: 4194304
# Maximum number of upload chunks sent at once, across every file and session
# (a global limit, not per file). Caps simultaneous upload requests in the
# browser to avoid resource-exhaustion failures when uploading large files.
# Default 4.
upload_max_concurrent_chunks: 4
# Session secret signs the login cookie. A random value was generated here;
# you can also override it via the FILETOASTER_SESSION_SECRET env var.
session_secret: %s
# Admin credentials. The admin password below is the hashed default ("admin");
# override it at runtime with the FILETOASTER_ADMIN_PASSWORD env var (preferred)
# or by replacing admin_password_hash here and restarting the server.
admin_user: admin
admin_password_hash: %s
`, dataDir, tmpDir, secret, adminHash)

  if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
    return "", fmt.Errorf("create config dir %q: %w", filepath.Dir(path), err)
  }
  if err := os.WriteFile(path, []byte(content), ConfigFileMode); err != nil {
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
  // The config file holds the session secret and admin hash. Refuse to start
  // when it is readable or writable by anyone but the owner, before doing any
  // further work, so a loosened config file is caught first.
  if err := requireOwnerOnly(path, ConfigFileMode, "config file"); err != nil {
    return nil, err
  }
  cfg, err := parseConfig(path)
  if err != nil {
    return nil, err
  }
  cfg.applyEnvOverrides()
  // Validate before resolving env vars so a broken env reference is reported
  // against the config as written (e.g. "env var X is not set") rather than
  // after the Env field has been consumed and cleared.
  if err := cfg.Validate(); err != nil {
    return nil, fmt.Errorf("invalid configuration in %q: %w", path, err)
  }
  cfg.resolveAllowedPaths()
  // The built-in tmp_dir default may be created on start; a user-specified
  // tmp_dir was already required to exist by Validate, so only ensure the
  // default location exists here.
  if err := ensureTmpDir(cfg); err != nil {
    return nil, fmt.Errorf("setup tmp_dir %q: %w", cfg.TMPDir, err)
  }
  // The upload staging dir must also be private to the owner: staged uploads
  // may contain file bytes, so reject a tmp_dir that group or other can access.
  if err := requireOwnerOnly(cfg.TMPDir, TmpDirMode, "tmp_dir"); err != nil {
    return nil, err
  }
  return cfg, nil
}

// LoadForEdit parses and validates the config at path without applying env
// overrides. Callers that persist the config back to disk (e.g. rotating the
// admin password) use it so the file's own values are kept instead of leaking
// env-set values onto disk.
func LoadForEdit(path string) (*Config, error) {
  cfg, err := parseConfig(path)
  if err != nil {
    return nil, err
  }
  if err := cfg.Validate(); err != nil {
    return nil, fmt.Errorf("invalid configuration in %q: %w", path, err)
  }
  return cfg, nil
}

// parseConfig reads and YAML-decodes the config at path with documented defaults
// filled in. It does not apply env overrides or validate.
func parseConfig(path string) (*Config, error) {
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
  return cfg, nil
}

// ChangeAdminPassword persists newHash as the admin password hash in the config
// file at path, leaving every other setting untouched. The caller is expected to
// have already hashed newPassword (see auth.HashPassword). If the
// FILETOASTER_ADMIN_PASSWORD env var is set at runtime, the resolver ignores this
// persisted hash in favour of the env var — so the change has no login effect
// until the env var is cleared.
func ChangeAdminPassword(path, newHash string) error {
  cfg, err := LoadForEdit(path)
  if err != nil {
    return err
  }
  cfg.AdminPasswordHash = newHash
  return cfg.Save(path)
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
  if v := os.Getenv("FILETOASTER_SHUTDOWN_TIMEOUT"); v != "" {
    if n, err := strconv.Atoi(v); err == nil {
      c.ShutdownTimeout = n
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
  if v := os.Getenv("FILETOASTER_UPLOAD_STALE_AFTER"); v != "" {
    if n, err := strconv.Atoi(v); err == nil {
      c.UploadStaleAfter = n
    }
  }
  if v := os.Getenv("FILETOASTER_UPLOAD_SWEEP_INTERVAL"); v != "" {
    if n, err := strconv.Atoi(v); err == nil {
      c.UploadSweepInterval = n
    }
  }
  if v := os.Getenv("FILETOASTER_UPLOAD_CHUNK_SIZE"); v != "" {
    if n, err := strconv.ParseInt(v, 10, 64); err == nil {
      c.UploadChunkSize = n
    }
  }
  if v := os.Getenv("FILETOASTER_UPLOAD_MAX_CONCURRENT_CHUNKS"); v != "" {
    if n, err := strconv.Atoi(v); err == nil {
      c.UploadMaxConcurrentChunks = n
    }
  }
  if v := os.Getenv("FILETOASTER_SESSION_SECRET"); v != "" {
    c.SessionSecret = v
  }
  if v := os.Getenv("FILETOASTER_ALLOWED_PATHS"); v != "" {
    raw := splitAndTrim(v)
    aps := make([]AllowedPath, 0, len(raw))
    for _, p := range raw {
      aps = append(aps, AllowedPath{Name: displayName(p), Path: p})
    }
    c.AllowedPaths = aps
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

// resolveAllowedPaths consumes each entry's Env by resolving it to a concrete
// absolute Path (env is used only when Path is empty, so Path takes precedence)
// and clears the Env field so the resolved path is what the server serves. This
// mutates the config, so it runs only in Load, not in LoadForEdit.
func (c *Config) resolveAllowedPaths() {
  for i := range c.AllowedPaths {
    ap := &c.AllowedPaths[i]
    if strings.TrimSpace(ap.Path) == "" && strings.TrimSpace(ap.Env) != "" {
      if resolved := os.Getenv(ap.Env); resolved != "" {
        ap.Path = filepath.Clean(resolved)
      }
    }
    if p := strings.TrimSpace(ap.Path); p != "" {
      ap.Path = filepath.Clean(p)
    }
    ap.Env = ""
  }
}

// displayName returns a reasonable icon label for a raw path taken from the
// FILETOASTER_ALLOWED_PATHS env var. It uses the path's base element, falling
// back to "Files" for roots like "/" or ".".
func displayName(path string) string {
  base := filepath.Base(filepath.Clean(path))
  if base == "." || base == "/" || base == string(filepath.Separator) {
    return "Files"
  }
  return base
}

// tmpDirIsDefault reports whether TMPDir is the built-in default location rather
// than a value the operator chose. Only the default is eligible for on-start
// creation; any other value is operator-provided and must already exist (checked
// by Validate). Comparison is path-cleaned so a redundant "." or trailing
// separator does not make an equal path look custom.
func (c *Config) tmpDirIsDefault() bool {
  return filepath.Clean(c.TMPDir) == filepath.Clean(defaultTmpDir())
}

// ensureTmpDir creates TMPDir when it is the built-in default location. A
// user-provided tmp_dir is left untouched: Validate already rejected a missing
// or non-directory value, so the server fails to start instead of creating an
// arbitrary path. It is created owner-only so staged uploads are private.
func ensureTmpDir(c *Config) error {
  if c.tmpDirIsDefault() {
    return os.MkdirAll(c.TMPDir, EnsureTmpDirMode)
  }
  return nil
}

// requireOwnerOnly verifies that path is private to its owner: group and other
// must have no permissions, and the owner must hold the required perm bits. It
// is used for the config file (session secret + admin hash) and the upload
// staging dir (file bytes), both of which must not be readable or writable by
// another local user. On failure it returns an actionable error that names the
// chmod command to fix the permissions.
func requireOwnerOnly(path string, wantPerm os.FileMode, label string) error {
  info, err := os.Stat(path)
  if err != nil {
    return err
  }
  perm := info.Mode().Perm()
  if perm&0o077 != 0 || perm&wantPerm != wantPerm {
    return fmt.Errorf("%s %q has permissions %o, but only the owner should access it (expected %o). Fix it with: chmod %o %q", label, path, perm, wantPerm, wantPerm, path)
  }
  return nil
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
  for i, ap := range c.AllowedPaths {
    label := fmt.Sprintf("allowed_paths[%d]", i)
    if strings.TrimSpace(ap.Name) == "" {
      errs = append(errs, label+" name is required")
    }
    hasPath := strings.TrimSpace(ap.Path) != ""
    hasEnv := strings.TrimSpace(ap.Env) != ""
    if !hasPath && !hasEnv {
      errs = append(errs, label+" must set one of path or env")
      continue
    }
    var target string
    var source string
    if hasPath {
      target = ap.Path
      source = "path"
    } else {
      target = os.Getenv(ap.Env)
      source = "env var " + ap.Env
    }
    if strings.TrimSpace(target) == "" {
      errs = append(errs, fmt.Sprintf("%s resolves to an empty path (%s is not set or is empty)", label, source))
      continue
    }
    if !filepath.IsAbs(target) {
      errs = append(errs, fmt.Sprintf("%s %q is not an absolute path", label, target))
      continue
    }
    info, err := os.Stat(target)
    switch {
    case err != nil:
      errs = append(errs, fmt.Sprintf("%s %q does not exist or is not accessible: %v", label, target, err))
    case !info.IsDir():
      errs = append(errs, fmt.Sprintf("%s %q is not a directory", label, target))
    }
  }

  if c.MaxParallelTasks < 1 {
    errs = append(errs, "max_parallel_tasks must be at least 1")
  }
  if c.ShutdownTimeout < 0 {
    errs = append(errs, "shutdown_timeout_seconds must be >= 0")
  }
  if c.GOMAXPROCS < 0 {
    errs = append(errs, "gomaxprocs must be >= 0 (0 = Go runtime default)")
  }
  if c.SessionSecret == "" {
    errs = append(errs, "session_secret is required (set FILETOASTER_SESSION_SECRET or config.session_secret)")
  }
  if c.TMPDir == "" {
    errs = append(errs, "tmp_dir must not be empty")
  } else if !c.tmpDirIsDefault() {
    // A user-provided tmp_dir is never auto-created: it must already exist.
    // Only the built-in default location may be missing and get created at
    // startup (see Load).
    info, err := os.Stat(c.TMPDir)
    switch {
    case err != nil:
      errs = append(errs, fmt.Sprintf("tmp_dir %q does not exist or is not accessible: %v", c.TMPDir, err))
    case !info.IsDir():
      errs = append(errs, fmt.Sprintf("tmp_dir %q is not a directory", c.TMPDir))
    }
  }
  if c.UploadStaleAfter < 1 {
    errs = append(errs, "upload_stale_after must be at least 1 hour")
  }
  if c.UploadSweepInterval < 1 {
    errs = append(errs, "upload_sweep_interval must be at least 1 hour")
  }
  if c.UploadChunkSize <= 0 {
    errs = append(errs, "upload_chunk_size must be a positive number of bytes")
  }
  if c.UploadMaxConcurrentChunks < 1 {
    errs = append(errs, "upload_max_concurrent_chunks must be at least 1")
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
  if err := os.WriteFile(tmp, data, ConfigFileMode); err != nil {
    return err
  }
  return os.Rename(tmp, path)
}

// UploadStaleAfterDuration converts the integer-hours UploadStaleAfter into a
// duration (hours * time.Hour).
func (c *Config) UploadStaleAfterDuration() time.Duration {
  return time.Duration(c.UploadStaleAfter) * time.Hour
}

// UploadSweepIntervalDuration converts the integer-hours UploadSweepInterval
// into a duration (hours * time.Hour).
func (c *Config) UploadSweepIntervalDuration() time.Duration {
  return time.Duration(c.UploadSweepInterval) * time.Hour
}
