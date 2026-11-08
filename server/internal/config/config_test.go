package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filetoaster/internal/auth"
)

// writeConfig writes raw YAML to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// validYAML returns a YAML document guaranteed to pass Validate, using real
// directories for allowed_paths and tmp_dir.
func validYAML(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tmp := t.TempDir()
	return "addr: \"127.0.0.1:0\"\n" +
		"allowed_paths:\n  - " + root + "\n" +
		"max_parallel_tasks: 2\n" +
		"gomaxprocs: 0\n" +
		"tmp_dir: " + tmp + "\n" +
		"session_secret: supersecret\n"
}

func TestDefaultConfigDefaults(t *testing.T) {
	c := DefaultConfig()
	if c.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", c.Addr)
	}
	if c.MaxParallelTasks != 4 {
		t.Errorf("MaxParallelTasks = %d, want 4", c.MaxParallelTasks)
	}
	if c.GOMAXPROCS != 0 {
		t.Errorf("GOMAXPROCS = %d, want 0", c.GOMAXPROCS)
	}
	if !strings.HasSuffix(c.TMPDir, "filetoaster-uploads") {
		t.Errorf("TMPDir = %q, want suffix filetoaster-uploads", c.TMPDir)
	}
}

func TestValidateAllGood(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = ":8080"
	cfg.AllowedPaths = []string{t.TempDir()}
	cfg.MaxParallelTasks = 4
	cfg.SessionSecret = "x"
	cfg.TMPDir = t.TempDir()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestValidateErrors(t *testing.T) {
	root := t.TempDir()
	cases := map[string]*Config{
		"empty addr":       {Addr: "", AllowedPaths: []string{root}, SessionSecret: "x", TMPDir: t.TempDir()},
		"bad addr":         {Addr: ":::not-an-addr", AllowedPaths: []string{root}, SessionSecret: "x", TMPDir: t.TempDir()},
		"no allowed_paths": {Addr: ":8080", SessionSecret: "x", TMPDir: t.TempDir()},
		"missing secret":   {Addr: ":8080", AllowedPaths: []string{root}, TMPDir: t.TempDir()},
		"bad tmp_dir":      {Addr: ":8080", AllowedPaths: []string{root}, SessionSecret: "x", TMPDir: ""},
		"zero tasks":       {Addr: ":8080", AllowedPaths: []string{root}, SessionSecret: "x", TMPDir: t.TempDir(), MaxParallelTasks: 0},
		"negative gc":      {Addr: ":8080", AllowedPaths: []string{root}, SessionSecret: "x", TMPDir: t.TempDir(), GOMAXPROCS: -1},
		"nonexistent root": {Addr: ":8080", AllowedPaths: []string{filepath.Join(t.TempDir(), "nope")}, SessionSecret: "x", TMPDir: t.TempDir()},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate = nil, want error for %q", name)
			}
		})
	}
}

func TestResolveConfigPath_Explicit(t *testing.T) {
	path := writeConfig(t, validYAML(t))
	got, created, err := ResolveConfigPath(path)
	if err != nil {
		t.Fatalf("ResolveConfigPath = %v", err)
	}
	if got != path || created {
		t.Errorf("ResolveConfigPath = (%q, %v), want (%q, false)", got, created, path)
	}
}

func TestResolveConfigPath_DefaultInCwd(t *testing.T) {
	t.Setenv("FILETOASTER_CONFIG", "")
	t.Setenv("HOME", t.TempDir()) // ensure no ~/.filetoaster fallback
	cwd := t.TempDir()
	t.Chdir(cwd)
	// write config.yaml into cwd
	if err := os.WriteFile(filepath.Join(cwd, "config.yaml"), []byte(validYAML(t)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, created, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath = %v", err)
	}
	if created {
		t.Error("created = false, want true")
	}
	want := filepath.Join(cwd, "config.yaml")
	if got != want {
		t.Errorf("ResolveConfigPath = %q, want %q", got, want)
	}
}

func TestResolveConfigPath_HomeFallback(t *testing.T) {
	t.Setenv("FILETOASTER_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir()) // no config.yaml in cwd

	homeConfig := filepath.Join(home, ".filetoaster", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(homeConfig), 0o755); err != nil {
		t.Fatalf("mkdir home config dir: %v", err)
	}
	writeYAML(t, homeConfig, validYAML(t))

	got, created, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath = %v", err)
	}
	if created {
		t.Error("created = false, want true")
	}
	if got != homeConfig {
		t.Errorf("ResolveConfigPath = %q, want %q", got, homeConfig)
	}
}

func TestResolveConfigPath_EnvOverride(t *testing.T) {
	t.Setenv("FILETOASTER_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	envPath := filepath.Join(t.TempDir(), "env.yaml")
	writeYAML(t, envPath, validYAML(t))

	t.Setenv("FILETOASTER_CONFIG", envPath)
	got, created, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath = %v", err)
	}
	if created {
		t.Error("created = false, want true")
	}
	if got != envPath {
		t.Errorf("ResolveConfigPath = %q, want %q", got, envPath)
	}
}

func TestResolveConfigPath_ExplicitMissing(t *testing.T) {
	_, created, err := ResolveConfigPath(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("ResolveConfigPath = nil, want error")
	}
	if created {
		t.Error("created = true, want false (an explicit missing path must not be created)")
	}
}

func TestResolveConfigPath_AutoCreate(t *testing.T) {
	t.Setenv("FILETOASTER_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	got, created, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath = %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	want := filepath.Join(home, ".filetoaster", "config.yaml")
	if got != want {
		t.Errorf("ResolveConfigPath = %q, want %q", got, want)
	}

	// The auto-created config must be loadable and validated.
	cfg, err := Load(got)
	if err != nil {
		t.Fatalf("Load(auto-created) = %v", err)
	}
	if cfg.SessionSecret == "" {
		t.Error("auto-created session_secret should be generated")
	}
	if cfg.Debug {
		t.Error("auto-created debug should default to false")
	}
	if len(cfg.AllowedPaths) != 1 || cfg.AllowedPaths[0] != filepath.Join(home, "filetoaster-data") {
		t.Errorf("auto-created allowed_paths = %v", cfg.AllowedPaths)
	}
}

func TestWriteDefaultConfig_Loads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".filetoaster", "config.yaml")
	got, err := WriteDefaultConfig(path)
	if err != nil {
		t.Fatalf("WriteDefaultConfig = %v", err)
	}
	if got != path {
		t.Errorf("WriteDefaultConfig = %q, want %q", got, path)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if cfg.SessionSecret == "" {
		t.Error("session_secret should be generated")
	}
	if cfg.Debug {
		t.Error("debug should default to false")
	}
}

func TestDebugEnvOverride(t *testing.T) {
	t.Setenv("FILETOASTER_DEBUG", "true")
	cfg, err := Load(writeConfig(t, "session_secret: x\nallowed_paths:\n  - "+t.TempDir()+"\n"))
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if !cfg.Debug {
		t.Error("Debug should be true when FILETOASTER_DEBUG=true")
	}
}

func TestLoadValid(t *testing.T) {
	path := writeConfig(t, validYAML(t))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if cfg.Addr != "127.0.0.1:0" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.MaxParallelTasks != 2 {
		t.Errorf("MaxParallelTasks = %d, want 2", cfg.MaxParallelTasks)
	}
	if cfg.SourcePath != path {
		t.Errorf("SourcePath = %q, want %q", cfg.SourcePath, path)
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	// Omits addr and max_parallel_tasks; defaults should apply.
	yaml := "allowed_paths:\n  - " + t.TempDir() + "\nsession_secret: x\n"
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want default :8080", cfg.Addr)
	}
	if cfg.MaxParallelTasks != 4 {
		t.Errorf("MaxParallelTasks = %d, want default 4", cfg.MaxParallelTasks)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	_, err := Load(writeConfig(t, "foo: [unclosed\n  bad: : :"))
	if err == nil {
		t.Fatal("Load = nil, want malformed-YAML error")
	}
	if !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("error = %q, want 'not valid YAML'", err.Error())
	}
}

func TestLoadUnknownField(t *testing.T) {
	yaml := "session_secret: x\nallowed_paths:\n  - " + t.TempDir() + "\nbogus_key: 1\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want unknown-field error")
	}
}

func TestLoadMissingAllowedPathDoesNotEx(t *testing.T) {
	// allowed_paths points at a non-existent directory.
	yaml := "session_secret: x\nallowed_paths:\n  - " + filepath.Join(t.TempDir(), "nope") + "\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want 'does not exist'", err.Error())
	}
}

func TestLoadMissingSecret(t *testing.T) {
	yaml := "allowed_paths:\n  - " + t.TempDir() + "\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want missing-secret error")
	}
	if !strings.Contains(err.Error(), "session_secret") {
		t.Errorf("error = %q, want 'session_secret'", err.Error())
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	t.Setenv("FILETOASTER_ADDR", "127.0.0.1:9090")
	t.Setenv("FILETOASTER_MAX_TASKS", "8")
	t.Setenv("FILETOASTER_GOMAXPROCS", "2")
	t.Setenv("FILETOASTER_TMP_DIR", tmp)
	t.Setenv("FILETOASTER_SESSION_SECRET", "envsecret")
	t.Setenv("FILETOASTER_ALLOWED_PATHS", root)
	// Provide only a secret; everything else comes from env.
	yaml := "session_secret: filesecret\n"

	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if cfg.Addr != "127.0.0.1:9090" {
		t.Errorf("Addr override = %q", cfg.Addr)
	}
	if cfg.MaxParallelTasks != 8 {
		t.Errorf("MaxParallelTasks override = %d", cfg.MaxParallelTasks)
	}
	if cfg.GOMAXPROCS != 2 {
		t.Errorf("GOMAXPROCS override = %d", cfg.GOMAXPROCS)
	}
	if cfg.TMPDir != tmp {
		t.Errorf("TMPDir override = %q, want %q", cfg.TMPDir, tmp)
	}
	if cfg.SessionSecret != "envsecret" {
		t.Errorf("SessionSecret override = %q, want envsecret", cfg.SessionSecret)
	}
	if len(cfg.AllowedPaths) != 1 || cfg.AllowedPaths[0] != root {
		t.Errorf("AllowedPaths override = %v", cfg.AllowedPaths)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "saved.yaml")
	cfg := DefaultConfig()
	cfg.Addr = ":8080"
	cfg.AllowedPaths = []string{t.TempDir()}
	cfg.MaxParallelTasks = 3
	cfg.SessionSecret = "roundtrip"
	cfg.TMPDir = t.TempDir()
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save = %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload Load = %v", err)
	}
	if reloaded.MaxParallelTasks != 3 {
		t.Errorf("roundtrip MaxParallelTasks = %d, want 3", reloaded.MaxParallelTasks)
	}
	if reloaded.SessionSecret != "roundtrip" {
		t.Errorf("roundtrip SessionSecret = %q", reloaded.SessionSecret)
	}
}

// writeYAML writes content to an existing path (used when the parent dir is
// created manually, e.g. the HOME fallback test).
func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
}

// TestChangeAdminPasswordOverwritesHashAndPreservesRest verifies that rotating
// the admin password updates only the admin_password_hash field and leaves every
// other setting intact.
func TestChangeAdminPasswordOverwritesHashAndPreservesRest(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeYAML(t, path, "addr: \"127.0.0.1:0\"\n"+
		"allowed_paths:\n  - "+root+"\n"+
		"max_parallel_tasks: 2\n"+
		"gomaxprocs: 0\n"+
		"tmp_dir: "+tmp+"\n"+
		"session_secret: supersecret\n"+
		"admin_password_hash: $argon2id$v=19$old\n")

	newHash, err := auth.HashPassword("brand-new")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := ChangeAdminPassword(path, newHash); err != nil {
		t.Fatalf("ChangeAdminPassword: %v", err)
	}

	reloaded, err := LoadForEdit(path)
	if err != nil {
		t.Fatalf("LoadForEdit after change: %v", err)
	}
	if !auth.VerifyPassword(reloaded.AdminPasswordHash, "brand-new") {
		t.Fatal("persisted hash must verify the new password")
	}
	if auth.VerifyPassword(reloaded.AdminPasswordHash, "old") {
		t.Fatal("the previous hash must no longer be stored")
	}
	// Every other field must survive the rewrite.
	if reloaded.Addr != "127.0.0.1:0" {
		t.Errorf("addr = %q, want 127.0.0.1:0", reloaded.Addr)
	}
	if reloaded.AllowedPaths[0] != root {
		t.Errorf("allowed_paths changed = %q, want %q", reloaded.AllowedPaths[0], root)
	}
	if reloaded.SessionSecret != "supersecret" {
		t.Errorf("session_secret = %q, want supersecret", reloaded.SessionSecret)
	}
	if reloaded.MaxParallelTasks != 2 {
		t.Errorf("max_parallel_tasks = %d, want 2", reloaded.MaxParallelTasks)
	}
}

// TestChangeAdminPasswordUsesFileValuesNotEnv ensures the change does not leak
// env-set values back onto disk: even with FILETOASTER_SESSION_SECRET set, the
// persisted secret must be the file's own value.
func TestChangeAdminPasswordUsesFileValuesNotEnv(t *testing.T) {
	t.Setenv("FILETOASTER_SESSION_SECRET", "env-secret")
	t.Setenv("FILETOASTER_ADDR", "10.0.0.1:9999")

	root := t.TempDir()
	tmp := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeYAML(t, path, "addr: \"127.0.0.1:0\"\n"+
		"allowed_paths:\n  - "+root+"\n"+
		"max_parallel_tasks: 2\n"+
		"tmp_dir: "+tmp+"\n"+
		"session_secret: file-secret\n")

	newHash, _ := auth.HashPassword("new")
	if err := ChangeAdminPassword(path, newHash); err != nil {
		t.Fatalf("ChangeAdminPassword: %v", err)
	}

	// LoadForEdit ignores env overrides, so the file's own values must remain.
	reloaded, err := LoadForEdit(path)
	if err != nil {
		t.Fatalf("LoadForEdit: %v", err)
	}
	if reloaded.SessionSecret != "file-secret" {
		t.Errorf("session_secret leaked env value: got %q, want file-secret", reloaded.SessionSecret)
	}
	if reloaded.Addr != "127.0.0.1:0" {
		t.Errorf("addr leaked env value: got %q, want 127.0.0.1:0", reloaded.Addr)
	}
	// The hash was still applied.
	if !auth.VerifyPassword(reloaded.AdminPasswordHash, "new") {
		t.Fatal("hash should have been persisted")
	}
}
