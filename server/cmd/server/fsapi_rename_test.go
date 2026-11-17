package main

import (
  "bytes"
  "encoding/json"
  "io"
  "net/http"
  "net/http/httptest"
  "os"
  "path/filepath"
  "testing"
)

// renameBody marshals a fsRenameRequest into a JSON request body.
func renameBody(paths []string, name string) *bytes.Reader {
  b, _ := json.Marshal(fsRenameRequest{Paths: paths, Name: name})
  return bytes.NewReader(b)
}

func TestServeFsRename(t *testing.T) {
  cfg := buildTestConfig(t)
  root := cfg.AllowedPaths[0].Path
  // Create a couple of files to rename.
  for _, n := range []string{"a.txt", "b.txt"} {
    if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
      t.Fatal(err)
    }
  }

  rec := httptest.NewRecorder()
  serveFsRename(rec, httptest.NewRequest(http.MethodPost, "/api/fs/rename",
    renameBody([]string{filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")}, "renamed")), cfg)

  if rec.Code != http.StatusOK {
    t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
  }
  var body struct {
    OK bool `json:"ok"`
  }
  if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
    t.Fatal(err)
  }
  if !body.OK {
    t.Fatalf("ok = false")
  }
  // Verify the files were renamed to the exact names. "sub" is the directory
  // buildTestConfig seeds into the root and must remain untouched.
  want := map[string]bool{"renamed": true, "renamed (1)": true, "sub": true}
  entries, err := os.ReadDir(root)
  if err != nil {
    t.Fatal(err)
  }
  for _, e := range entries {
    if !want[e.Name()] {
      t.Errorf("unexpected entry %q", e.Name())
    }
    delete(want, e.Name())
  }
  if len(want) != 0 {
    t.Errorf("not all expected files created: %v", want)
  }
}

func TestServeFsRenameValidates(t *testing.T) {
  cfg := buildTestConfig(t)
  root := cfg.AllowedPaths[0].Path
  for _, tc := range []struct {
    name     string
    body     io.Reader
    wantCode int
  }{{
    name:     "empty body",
    body:     http.NoBody,
    wantCode: http.StatusBadRequest,
  }, {
    name:     "missing name",
    body:     renameBody([]string{filepath.Join(root, "a.txt")}, ""),
    wantCode: http.StatusBadRequest,
  }, {
    name:     "no paths",
    body:     renameBody(nil, "x"),
    wantCode: http.StatusBadRequest,
  }} {
    t.Run(tc.name, func(t *testing.T) {
      rec := httptest.NewRecorder()
      serveFsRename(rec, httptest.NewRequest(http.MethodPost, "/api/fs/rename", tc.body), cfg)
      if rec.Code != tc.wantCode {
        t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
      }
    })
  }
}

func TestServeFsRenameConflict(t *testing.T) {
  cfg := buildTestConfig(t)
  root := cfg.AllowedPaths[0].Path
  for _, n := range []string{"a.txt", "b.txt"} {
    if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
      t.Fatal(err)
    }
  }
  // Pre-create the suffixed name so the batch conflicts.
  if err := os.WriteFile(filepath.Join(root, "renamed (1)"), []byte("x"), 0o644); err != nil {
    t.Fatal(err)
  }

  rec := httptest.NewRecorder()
  serveFsRename(rec, httptest.NewRequest(http.MethodPost, "/api/fs/rename",
    renameBody([]string{filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")}, "renamed")), cfg)
  if rec.Code != http.StatusConflict {
    t.Errorf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
  }
  // The selection is left untouched.
  for _, n := range []string{"a.txt", "b.txt", "renamed (1)"} {
    if _, err := os.Stat(filepath.Join(root, n)); err != nil {
      t.Errorf("%q should still exist: %v", n, err)
    }
  }
}

func TestServeFsRenameRejectsEscape(t *testing.T) {
  cfg := buildTestConfig(t)
  outside := filepath.Join(filepath.Dir(cfg.AllowedPaths[0].Path), "outside")
  if err := os.MkdirAll(outside, 0o755); err != nil {
    t.Fatal(err)
  }
  if err := os.WriteFile(filepath.Join(outside, "a.txt"), []byte("x"), 0o644); err != nil {
    t.Fatal(err)
  }

  rec := httptest.NewRecorder()
  serveFsRename(rec, httptest.NewRequest(http.MethodPost, "/api/fs/rename",
    renameBody([]string{filepath.Join(outside, "a.txt")}, "x")), cfg)
  if rec.Code != http.StatusForbidden {
    t.Errorf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
  }
  // The outside file must not have moved.
  if _, err := os.Stat(filepath.Join(outside, "a.txt")); err != nil {
    t.Errorf("outside file should be untouched: %v", err)
  }
}

func TestServeFsRenameRequiresAuth(t *testing.T) {
  guard := newTestGuard(t)
  cfg := buildTestConfig(t)
  root := cfg.AllowedPaths[0].Path

  t.Run("no cookie is rejected", func(t *testing.T) {
    handler := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      serveFsRename(w, r, cfg)
    }))
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/fs/rename",
      renameBody([]string{filepath.Join(root, "a.txt")}, "x")))
    if rec.Code != http.StatusUnauthorized {
      t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
    }
  })
  t.Run("valid cookie is accepted", func(t *testing.T) {
    handler := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      serveFsRename(w, r, cfg)
    }))
    req := httptest.NewRequest(http.MethodPost, "/api/fs/rename",
      renameBody([]string{filepath.Join(root, "a.txt")}, "renamed"))
    req.AddCookie(signedSession(t, guard))
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK {
      t.Errorf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
    }
  })
}
