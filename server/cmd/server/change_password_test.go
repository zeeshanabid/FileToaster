package main

import (
  "encoding/json"
  "net/http"
  "net/http/httptest"
  "os"
  "path/filepath"
  "strings"
  "testing"

  "filetoaster/internal/auth"
  "filetoaster/internal/config"
)

// writeTestConfig writes a minimal, valid config file to path.
func writeTestConfig(t *testing.T, path, root string) {
  t.Helper()
  tmp := filepath.Join(t.TempDir(), "uploads")
  if err := os.MkdirAll(tmp, 0o755); err != nil {
    t.Fatalf("create tmp_dir: %v", err)
  }
  content := "addr: \"127.0.0.1:0\"\n" +
    "allowed_paths:\n  - name: Files\n    path: " + root + "\n" +
    "max_parallel_tasks: 2\n" +
    "tmp_dir: " + tmp + "\n" +
    "session_secret: session-secret\n" +
    "admin_password_hash: $argon2id$v=19$old\n"
  if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
    t.Fatalf("write test config: %v", err)
  }
}

func changePasswordReq(t *testing.T, g *auth.Guard, current, newest string) *httptest.ResponseRecorder {
  t.Helper()
  rec := httptest.NewRecorder()
  body := `{"current_password":` + jsonString(current) + `,"new_password":` + jsonString(newest) + `}`
  g.ServeChangePassword(rec, httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
    strings.NewReader(body)))
  return rec
}

// TestChangePasswordPersistsToConfig wires the guard's ConfigSaver to
// config.ChangeAdminPassword and asserts that a rotated password is written to
// disk, replaces the live hash, and is accepted by a freshly built guard.
func TestChangePasswordPersistsToConfig(t *testing.T) {
  root := t.TempDir()
  path := filepath.Join(t.TempDir(), "config.yaml")
  writeTestConfig(t, path, root)

  adminHash, err := auth.HashPassword("current-admin")
  if err != nil {
    t.Fatalf("HashPassword: %v", err)
  }
  guard, err := auth.NewGuard("session-secret", auth.DefaultUser, adminHash)
  if err != nil {
    t.Fatalf("NewGuard: %v", err)
  }
  guard.ConfigSaver = func(h string) error { return config.ChangeAdminPassword(path, h) }

  rec := changePasswordReq(t, guard, "current-admin", "rotated-123")
  if rec.Code != http.StatusOK {
    t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
  }

  // The on-disk hash must verify the new password and reject the old one.
  onDisk, err := config.LoadForEdit(path)
  if err != nil {
    t.Fatalf("LoadForEdit: %v", err)
  }
  if !auth.VerifyPassword(onDisk.AdminPasswordHash, "rotated-123") {
    t.Fatal("config file must store a hash verifying the new password")
  }
  if auth.VerifyPassword(onDisk.AdminPasswordHash, "current-admin") {
    t.Fatal("config file must not still verify the old password")
  }

  // A guard rebuilt from the file accepts the rotated password as current.
  guard2, err := auth.NewGuard("session-secret", auth.DefaultUser, onDisk.AdminPasswordHash)
  if err != nil {
    t.Fatalf("NewGuard: %v", err)
  }
  guard2.ConfigSaver = guard.ConfigSaver
  rec2 := changePasswordReq(t, guard2, "rotated-123", "again-456")
  if rec2.Code != http.StatusOK {
    t.Fatalf("status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
  }
}

// TestChangePasswordRejectsWrongCurrentEndToEnd verifies the file is untouched
// when the reported current password is wrong.
func TestChangePasswordRejectsWrongCurrentEndToEnd(t *testing.T) {
  root := t.TempDir()
  path := filepath.Join(t.TempDir(), "config.yaml")
  writeTestConfig(t, path, root)

  adminHash, _ := auth.HashPassword("current-admin")
  guard, _ := auth.NewGuard("session-secret", auth.DefaultUser, adminHash)
  guard.ConfigSaver = func(h string) error { return config.ChangeAdminPassword(path, h) }

  rec := changePasswordReq(t, guard, "wrong", "rotated-123")
  if rec.Code != http.StatusUnauthorized {
    t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
  }

  onDisk, err := config.LoadForEdit(path)
  if err != nil {
    t.Fatalf("LoadForEdit: %v", err)
  }
  if auth.VerifyPassword(onDisk.AdminPasswordHash, "rotated-123") {
    t.Fatal("config file must be unchanged after a rejected change")
  }
}

func jsonString(s string) string {
  b, _ := json.Marshal(s)
  return string(b)
}
