package main

import (
  "os"
  "testing"

  "filetoaster/internal/auth"
)

// TestResolveAdminHashPrecedence covers the documented resolution order:
// FILETOASTER_ADMIN_PASSWORD (env) over the config hash over the default admin
// password, and that the result is never blank.
func TestResolveAdminHashPrecedence(t *testing.T) {
  t.Run("env wins over config hash", func(t *testing.T) {
    t.Setenv("FILETOASTER_ADMIN_PASSWORD", "from-env")
    hash, err := resolveAdminHash("hash-from-config")
    if err != nil {
      t.Fatalf("resolveAdminHash: %v", err)
    }
    if !auth.VerifyPassword(hash, "from-env") {
      t.Fatal("FILETOASTER_ADMIN_PASSWORD must take precedence over the config hash")
    }
  })

  t.Run("config hash used when env unset", func(t *testing.T) {
    os.Unsetenv("FILETOASTER_ADMIN_PASSWORD")
    hash, err := resolveAdminHash("hash-from-config")
    if err != nil {
      t.Fatalf("resolveAdminHash: %v", err)
    }
    if hash != "hash-from-config" {
      t.Fatalf("config hash should pass through unchanged, got %q", hash)
    }
  })

  t.Run("default password when nothing set", func(t *testing.T) {
    os.Unsetenv("FILETOASTER_ADMIN_PASSWORD")
    hash, err := resolveAdminHash("")
    if err != nil {
      t.Fatalf("resolveAdminHash: %v", err)
    }
    if hash == "" {
      t.Fatal("resolved admin hash must never be blank")
    }
    if !auth.VerifyPassword(hash, auth.DefaultPassword) {
      t.Fatal("empty config hash must fall back to the default admin password")
    }
  })
}
