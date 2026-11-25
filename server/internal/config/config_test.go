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

// secureTmpDir returns a temp directory with 0700 permissions, the mode the
// server requires for the upload staging dir. t.TempDir() may be created with
// 0755 depending on the platform umask, so we set 0700 explicitly to model a
// correctly-permissioned tmp_dir.
func secureTmpDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(t.TempDir(), "tmp")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatalf("chmod temp dir: %v", err)
	}
	return d
}

// validYAML returns a YAML document guaranteed to pass Validate, using real
// directories for allowed_paths and tmp_dir.
func validYAML(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tmp := secureTmpDir(t)
	return "addr: \"127.0.0.1:0\"\n" +
		"admin_user: testuser\n" +
		"allowed_paths:\n  - name: Files\n    path: " + root + "\n" +
		"max_parallel_tasks: 2\n" +
		"gomaxprocs: 0\n" +
		"tmp_dir: " + tmp + "\n" +
		"session_secret: supersecret\n"
}

// allowedOne returns a single AllowedPath rooted at p.
func allowedOne(p string) []AllowedPath {
	return []AllowedPath{{Name: "root", Path: p}}
}

func TestDefaultConfigDefaults(t *testing.T) {
	c := DefaultConfig()
	if c.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", c.Addr)
	}
	if c.MaxParallelTasks != 4 {
		t.Errorf("MaxParallelTasks = %d, want 4", c.MaxParallelTasks)
	}
	if c.ShutdownTimeout != 30 {
		t.Errorf("ShutdownTimeout = %d, want default 30", c.ShutdownTimeout)
	}
	if c.GOMAXPROCS != 0 {
		t.Errorf("GOMAXPROCS = %d, want 0", c.GOMAXPROCS)
	}
	if !strings.HasSuffix(c.TMPDir, filepath.Join(".filetoaster", "tmp_uploads")) {
		t.Errorf("TMPDir = %q, want suffix %q", c.TMPDir, filepath.Join(".filetoaster", "tmp_uploads"))
	}
}

func TestValidateAllGood(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = ":8080"
	cfg.AdminUser = "testuser"
	cfg.AllowedPaths = []AllowedPath{{Name: "root", Path: t.TempDir()}}
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
		"empty addr":          {Addr: "", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: t.TempDir(), AdminUser: "testuser"},
		"bad addr":            {Addr: ":::not-an-addr", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: t.TempDir(), AdminUser: "testuser"},
		"no allowed_paths":    {Addr: ":8080", SessionSecret: "x", TMPDir: t.TempDir(), AdminUser: "testuser"},
		"missing secret":      {Addr: ":8080", AllowedPaths: allowedOne(root), TMPDir: t.TempDir(), AdminUser: "testuser"},
		"bad tmp_dir":         {Addr: ":8080", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: "", AdminUser: "testuser"},
		"nonexistent tmp_dir": {Addr: ":8080", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: filepath.Join(t.TempDir(), "nope"), AdminUser: "testuser"},
		"tmp_dir is a file": func() *Config {
			f := filepath.Join(t.TempDir(), "afile")
			os.WriteFile(f, []byte("x"), 0o600)
			return &Config{Addr: ":8080", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: f, AdminUser: "testuser"}
		}(),
		"zero tasks":           {Addr: ":8080", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: t.TempDir(), MaxParallelTasks: 0, AdminUser: "testuser"},
		"negative shutdown":    {Addr: ":8080", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: t.TempDir(), ShutdownTimeout: -1, AdminUser: "testuser"},
		"negative gc":          {Addr: ":8080", AllowedPaths: allowedOne(root), SessionSecret: "x", TMPDir: t.TempDir(), GOMAXPROCS: -1, AdminUser: "testuser"},
		"nonexistent root":     {Addr: ":8080", AllowedPaths: allowedOne(filepath.Join(t.TempDir(), "nope")), SessionSecret: "x", TMPDir: t.TempDir(), AdminUser: "testuser"},
		"missing name":         {Addr: ":8080", AllowedPaths: []AllowedPath{{Path: root}}, SessionSecret: "x", TMPDir: t.TempDir(), AdminUser: "testuser"},
		"neither path nor env": {Addr: ":8080", AllowedPaths: []AllowedPath{{Name: "x"}}, SessionSecret: "x", TMPDir: t.TempDir(), AdminUser: "testuser"},
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
	t.Setenv("FILETOASTER_TMP_DIR", secureTmpDir(t))
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
	if len(cfg.AllowedPaths) != 1 || cfg.AllowedPaths[0].Name != "Home" || cfg.AllowedPaths[0].Path != home {
		t.Errorf("auto-created allowed_paths = %v", cfg.AllowedPaths)
	}
}

func TestWriteDefaultConfig_Loads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FILETOASTER_TMP_DIR", secureTmpDir(t))
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
	tmp := secureTmpDir(t)
	cfg, err := Load(writeConfig(t, "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: "+t.TempDir()+"\ntmp_dir: "+tmp+"\n"))
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
	yaml := "allowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\nsession_secret: x\nadmin_user: testuser\ntmp_dir: " + secureTmpDir(t) + "\n"
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
	yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\nbogus_key: 1\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want unknown-field error")
	}
}

func TestLoadMissingAllowedPathDoesNotEx(t *testing.T) {
	// allowed_paths points at a non-existent directory.
	yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + filepath.Join(t.TempDir(), "nope") + "\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want 'does not exist'", err.Error())
	}
}

func TestLoadMissingSecret(t *testing.T) {
	yaml := "allowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want missing-secret error")
	}
	if !strings.Contains(err.Error(), "session_secret") {
		t.Errorf("error = %q, want 'session_secret'", err.Error())
	}
}

func TestLoadMissingTmpDir(t *testing.T) {
	// A non-existent tmp_dir must be rejected at startup, before the server
	// binds or auto-creates anything.
	yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\n" +
		"tmp_dir: " + filepath.Join(t.TempDir(), "nope") + "\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want validation error for missing tmp_dir")
	}
	if !strings.Contains(err.Error(), "tmp_dir") || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to mention tmp_dir not existing", err.Error())
	}
}

// TestValidateTmpDirMustBeDirectory verifies a tmp_dir that exists but is a file
// (not a directory) is rejected at startup.
func TestValidateTmpDirMustBeDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	cfg := &Config{
		Addr:          ":8080",
		AllowedPaths:  allowedOne(root),
		SessionSecret: "x",
		TMPDir:        file,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate = nil, want error for tmp_dir that is not a directory")
	}
	if !strings.Contains(err.Error(), "tmp_dir") || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("error = %q, want it to mention tmp_dir is not a directory", err.Error())
	}
}

func TestLoadCreatesDefaultTmpDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FILETOASTER_TMP_DIR", "")

	tmpDir := filepath.Join(home, ".filetoaster", "tmp_uploads")
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("precondition: default tmp_dir should not exist yet: %v", err)
	}

	yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\n"
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load = %v, want nil", err)
	}
	if !cfg.tmpDirIsDefault() {
		t.Fatalf("tmp_dir should be the default %q", tmpDir)
	}
	info, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("default tmp_dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("default tmp_dir %q is not a directory", tmpDir)
	}
}

// TestLoadRejectsMissingCustomTmpDir verifies a non-default tmp_dir is not created
// and fails startup when missing.
func TestLoadRejectsMissingCustomTmpDir(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "nope")

	yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\n" +
		"tmp_dir: " + tmpDir + "\n"
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("Load = nil, want validation error for missing custom tmp_dir")
	}
	if !strings.Contains(err.Error(), "tmp_dir") || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to mention tmp_dir not existing", err.Error())
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("custom tmp_dir must not be auto-created; stat err = %v", err)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	root := t.TempDir()
	tmp := secureTmpDir(t)
	t.Setenv("FILETOASTER_ADDR", "127.0.0.1:9090")
	t.Setenv("FILETOASTER_MAX_TASKS", "8")
	t.Setenv("FILETOASTER_GOMAXPROCS", "2")
	t.Setenv("FILETOASTER_TMP_DIR", tmp)
	t.Setenv("FILETOASTER_SESSION_SECRET", "envsecret")
	t.Setenv("FILETOASTER_ADMIN_USER", "envuser")
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
	if len(cfg.AllowedPaths) != 1 || cfg.AllowedPaths[0].Path != root {
		t.Errorf("AllowedPaths override = %v", cfg.AllowedPaths)
	}
}

func TestShutdownTimeoutDefault(t *testing.T) {
	// No YAML and no env: the documented default applies.
	yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\ntmp_dir: " + secureTmpDir(t) + "\n"
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if cfg.ShutdownTimeout != 30 {
		t.Errorf("ShutdownTimeout = %d, want default 30", cfg.ShutdownTimeout)
	}
}

func TestShutdownTimeoutFromFileAndEnv(t *testing.T) {
	t.Run("from file", func(t *testing.T) {
		yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\n" +
			"shutdown_timeout_seconds: 5\n" +
			"tmp_dir: " + secureTmpDir(t) + "\n"
		cfg, err := Load(writeConfig(t, yaml))
		if err != nil {
			t.Fatalf("Load = %v", err)
		}
		if cfg.ShutdownTimeout != 5 {
			t.Errorf("ShutdownTimeout = %d, want 5", cfg.ShutdownTimeout)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		t.Setenv("FILETOASTER_SHUTDOWN_TIMEOUT", "120")
		yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\n" +
			"shutdown_timeout_seconds: 5\n" +
			"tmp_dir: " + secureTmpDir(t) + "\n"
		cfg, err := Load(writeConfig(t, yaml))
		if err != nil {
			t.Fatalf("Load = %v", err)
		}
		if cfg.ShutdownTimeout != 120 {
			t.Errorf("ShutdownTimeout = %d, want 120 (env over file)", cfg.ShutdownTimeout)
		}
	})

	t.Run("non-integer env ignored", func(t *testing.T) {
		t.Setenv("FILETOASTER_SHUTDOWN_TIMEOUT", "not-a-number")
		yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\ntmp_dir: " + secureTmpDir(t) + "\n"
		cfg, err := Load(writeConfig(t, yaml))
		if err != nil {
			t.Fatalf("Load = %v", err)
		}
		if cfg.ShutdownTimeout != 30 {
			t.Errorf("ShutdownTimeout = %d, want default 30 when env is invalid", cfg.ShutdownTimeout)
		}
	})
}

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "saved.yaml")
	cfg := DefaultConfig()
	cfg.Addr = ":8080"
	cfg.AdminUser = "testuser"
	cfg.AllowedPaths = []AllowedPath{{Name: "root", Path: t.TempDir()}}
	cfg.MaxParallelTasks = 3
	cfg.SessionSecret = "roundtrip"
	cfg.TMPDir = secureTmpDir(t)
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
		"admin_user: testuser\n"+
		"allowed_paths:\n  - name: Files\n    path: "+root+"\n"+
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
	if reloaded.AllowedPaths[0].Path != root {
		t.Errorf("allowed_paths changed = %q, want %q", reloaded.AllowedPaths[0].Path, root)
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
		"admin_user: testuser\n"+
		"allowed_paths:\n  - name: Files\n    path: "+root+"\n"+
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

// TestChangeAdminPasswordPreservesCommentsAndFormatting verifies that only the
// admin_password_hash field is updated and all comments, formatting, and other
// fields are preserved exactly as they were.
func TestChangeAdminPasswordPreservesCommentsAndFormatting(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.yaml")

	// Write a config with comments and specific formatting
	originalContent := `# FileToaster configuration
# This is a comment at the top
addr: "127.0.0.1:0"  # inline comment
admin_user: testuser  # user comment
allowed_paths:
  - name: Files      # name comment
    path: ` + root + `  # path comment
max_parallel_tasks: 2  # tasks comment
gomaxprocs: 0
tmp_dir: ` + tmp + `
session_secret: supersecret  # secret comment
admin_password_hash: $argon2id$v=19$old  # hash comment
`
	writeYAML(t, path, originalContent)

	newHash, err := auth.HashPassword("brand-new")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := ChangeAdminPassword(path, newHash); err != nil {
		t.Fatalf("ChangeAdminPassword: %v", err)
	}

	// Read the file back and verify structure
	newContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	newContentStr := string(newContent)

	// Verify the hash was updated
	if !strings.Contains(newContentStr, newHash) {
		t.Errorf("new hash not found in file: %s", newContentStr)
	}
	if strings.Contains(newContentStr, "$argon2id$v=19$old") {
		t.Errorf("old hash still present in file: %s", newContentStr)
	}

	// Verify comments are preserved
	if !strings.Contains(newContentStr, "# FileToaster configuration") {
		t.Errorf("top comment lost: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "# This is a comment at the top") {
		t.Errorf("second comment lost: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "# inline comment") {
		t.Errorf("inline comment on addr lost: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "# name comment") {
		t.Errorf("name comment lost: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "# path comment") {
		t.Errorf("path comment lost: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "# tasks comment") {
		t.Errorf("tasks comment lost: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "# secret comment") {
		t.Errorf("secret comment lost: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "# hash comment") {
		t.Errorf("hash comment lost: %s", newContentStr)
	}

	// Verify other fields are unchanged
	if !strings.Contains(newContentStr, `addr: "127.0.0.1:0"`) {
		t.Errorf("addr field changed: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "max_parallel_tasks: 2") {
		t.Errorf("max_parallel_tasks field changed: %s", newContentStr)
	}
	if !strings.Contains(newContentStr, "session_secret: supersecret") {
		t.Errorf("session_secret field changed: %s", newContentStr)
	}

	// Verify the file can still be loaded and validated
	reloaded, err := LoadForEdit(path)
	if err != nil {
		t.Fatalf("LoadForEdit after change: %v", err)
	}
	if !auth.VerifyPassword(reloaded.AdminPasswordHash, "brand-new") {
		t.Fatal("persisted hash must verify the new password")
	}
}

// TestWriteDefaultConfigCreatesOwnerOnlyConfigFile verifies an auto-created
// config file is written with 0600 so the session secret and admin hash are
// private to the owner, not readable by other local users.
func TestWriteDefaultConfigCreatesOwnerOnlyConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := WriteDefaultConfig(path); err != nil {
		t.Fatalf("WriteDefaultConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != ConfigFileMode {
		t.Errorf("config file mode = %o, want %o", got, ConfigFileMode)
	}
}

// TestSaveWritesOwnerOnlyConfigFile verifies a rotated/saved config is written
// with 0600 (the Save writes atomically via a temp file).
func TestSaveWritesOwnerOnlyConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "saved.yaml")
	cfg := DefaultConfig()
	cfg.Addr = ":8080"
	cfg.AllowedPaths = []AllowedPath{{Name: "root", Path: t.TempDir()}}
	cfg.SessionSecret = "keep-me-secret"
	cfg.TMPDir = secureTmpDir(t)
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != ConfigFileMode {
		t.Errorf("saved config file mode = %o, want %o", got, ConfigFileMode)
	}
}

// TestLoadRejectsInsecureConfigFile verifies startup fails when the config file
// is readable or writable by anyone but the owner, and that the error tells the
// operator how to fix it.
func TestLoadRejectsInsecureConfigFile(t *testing.T) {
	root := t.TempDir()
	path := writeConfig(t, "session_secret: x\nallowed_paths:\n  - name: Files\n    path: "+root+"\ntmp_dir: "+secureTmpDir(t)+"\n")
	for _, mode := range []os.FileMode{0o644, 0o646, 0o666, 0o640} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("chmod %o: %v", mode, err)
		}
		_, err := Load(path)
		if err == nil {
			t.Fatalf("Load = nil, want error for config file with %o", mode)
		}
		msg := err.Error()
		if !strings.Contains(msg, "config file") || !strings.Contains(msg, "chmod 600") {
			t.Errorf("error = %q, want it to name 'config file' and the 'chmod 600' fix", msg)
		}
	}
	if err := os.Chmod(path, ConfigFileMode); err != nil {
		t.Fatalf("restore config perms: %v", err)
	}
}

// TestLoadRejectsConfigFileWithoutOwnerPermissions verifies startup fails when
// the config file is locked down so far that even the owner cannot read it.
func TestLoadRejectsConfigFileWithoutOwnerPermissions(t *testing.T) {
	root := t.TempDir()
	path := writeConfig(t, "session_secret: x\nallowed_paths:\n  - name: Files\n    path: "+root+"\ntmp_dir: "+secureTmpDir(t)+"\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(path, ConfigFileMode) }()
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load = nil, want error for config file with no owner permissions")
	}
	if !strings.Contains(err.Error(), "config file") {
		t.Errorf("error = %q, want it to name 'config file'", err.Error())
	}
}

// TestLoadRejectsInsecureTmpDir verifies startup fails when the configured
// tmp_dir is accessible to group or other, and that the error tells the
// operator how to fix it.
func TestLoadRejectsInsecureTmpDir(t *testing.T) {
	root := t.TempDir()
	tmp := secureTmpDir(t)
	for _, mode := range []os.FileMode{0o755, 0o757, 0o777, 0o711} {
		if err := os.Chmod(tmp, mode); err != nil {
			t.Fatalf("chmod %o: %v", mode, err)
		}
		yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + root + "\ntmp_dir: " + tmp + "\n"
		_, err := Load(writeConfig(t, yaml))
		if err == nil {
			t.Fatalf("Load = nil, want error for tmp_dir with %o", mode)
		}
		msg := err.Error()
		if !strings.Contains(msg, "tmp_dir") || !strings.Contains(msg, "chmod 700") {
			t.Errorf("error = %q, want it to name 'tmp_dir' and the 'chmod 700' fix", msg)
		}
	}
	if err := os.Chmod(tmp, TmpDirMode); err != nil {
		t.Fatalf("restore tmp_dir perms: %v", err)
	}
}

// TestLoadCreatesDefaultTmpDirOwnerOnly verifies the built-in default tmp_dir is
// created with 0700 so staged uploads are private to the owner.
func TestLoadCreatesDefaultTmpDirOwnerOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FILETOASTER_TMP_DIR", "")
	tmpDir := filepath.Join(home, ".filetoaster", "tmp_uploads")

	yaml := "session_secret: x\nadmin_user: testuser\nallowed_paths:\n  - name: Files\n    path: " + t.TempDir() + "\n"
	if _, err := Load(writeConfig(t, yaml)); err != nil {
		t.Fatalf("Load = %v, want nil", err)
	}
	info, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("default tmp_dir not created: %v", err)
	}
	if got := info.Mode().Perm(); got != TmpDirMode {
		t.Errorf("default tmp_dir mode = %o, want %o", got, TmpDirMode)
	}
}

// TestRequireOwnerOnly exercises the permission check directly for a range of
// file and directory modes.
func TestRequireOwnerOnly(t *testing.T) {
	// A correctly-permissioned file and directory pass.
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, []byte("x"), ConfigFileMode); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := requireOwnerOnly(f, ConfigFileMode, "config file"); err != nil {
		t.Errorf("requireOwnerOnly(good file) = %v, want nil", err)
	}
	d := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(d, TmpDirMode); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := requireOwnerOnly(d, TmpDirMode, "tmp_dir"); err != nil {
		t.Errorf("requireOwnerOnly(good dir) = %v, want nil", err)
	}

	// Group or other access is rejected for both files and directories.
	for _, mode := range []os.FileMode{0o644, 0o646} {
		if err := os.Chmod(f, mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		if err := requireOwnerOnly(f, ConfigFileMode, "config file"); err == nil {
			t.Errorf("requireOwnerOnly(file %o) = nil, want error", mode)
		}
	}
	for _, mode := range []os.FileMode{0o755, 0o757, 0o777} {
		if err := os.Chmod(d, mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		if err := requireOwnerOnly(d, TmpDirMode, "tmp_dir"); err == nil {
			t.Errorf("requireOwnerOnly(dir %o) = nil, want error", mode)
		}
	}
}
