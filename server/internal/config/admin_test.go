package config

import (
	"os"
	"path/filepath"
	"testing"

	"filetoaster/internal/auth"
)

// TestWriteDefaultConfigAdminCredentials verifies an auto-created default config
// boots the built-in admin/admin credentials.
func TestWriteDefaultConfigAdminCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("FILETOASTER_TMP_DIR", t.TempDir())
	if _, err := WriteDefaultConfig(path); err != nil {
		t.Fatalf("WriteDefaultConfig: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load default config: %v", err)
	}
	if cfg.AdminUser != auth.DefaultUser {
		t.Errorf("AdminUser = %q, want %q", cfg.AdminUser, auth.DefaultUser)
	}
	if cfg.AdminPasswordHash == "" {
		t.Fatal("default config must set a non-empty admin_password_hash")
	}
	if !auth.VerifyPassword(cfg.AdminPasswordHash, auth.DefaultPassword) {
		t.Fatal("default admin_password_hash must verify against the default admin password")
	}
}

// TestLoadCustomAdminCredentials verifies a config file may pin its own admin
// user and password hash, and that the loaded hash verifies that password.
func TestLoadCustomAdminCredentials(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	custom, err := auth.HashPassword("s3cret-custom")
	if err != nil {
		t.Fatal(err)
	}
	content := "addr: \"127.0.0.1:0\"\n" +
		"allowed_paths:\n  - name: Files\n    path: " + root + "\n" +
		"max_parallel_tasks: 1\n" +
		"tmp_dir: " + tmp + "\n" +
		"session_secret: xyz\n" +
		"admin_user: root\n" +
		"admin_password_hash: " + custom + "\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AdminUser != "root" {
		t.Errorf("AdminUser = %q, want root", cfg.AdminUser)
	}
	if !auth.VerifyPassword(cfg.AdminPasswordHash, "s3cret-custom") {
		t.Fatal("loaded admin_password_hash must verify against the configured password")
	}
}
