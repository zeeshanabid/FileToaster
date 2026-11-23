package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filetoaster/internal/auth"
	"filetoaster/internal/config"
	"filetoaster/internal/fs"
	"filetoaster/internal/protocol"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
)

// compressEndpoint bundles the auth-wrapped compress handler with the service,
// wired the same way serve.go wires them, so tests exercise the real protection
// and task lifecycle. `root` is the single allowed folder the compress may reference.
type compressEndpoint struct {
	Handler http.Handler
	Svc     *service.Service
}

func newCompressEndpoint(t *testing.T, root string) compressEndpoint {
	t.Helper()
	cfg := &config.Config{
		AllowedPaths:     []config.AllowedPath{{Name: "Files", Path: root}},
		MaxParallelTasks: 4,
	}
	fsCfg := newFSConfig(cfg)
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-instance")
	registerCompressOp(svc, roots)
	taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	guard := newTestGuard(t)
	return compressEndpoint{
		Handler: guard.Middleware(http.HandlerFunc(taskSrv.ServeCompress)),
		Svc:     svc,
	}
}

// compressRequestBody marshals a compress request body (paths + format).
func compressRequestBody(paths []string, format string) io.Reader {
	b, _ := json.Marshal(map[string]any{"paths": paths, "format": format})
	return bytes.NewReader(b)
}

// TestServeCompressRequiresAuth verifies the compress endpoint is protected by
// the auth middleware: requests without a valid session cookie are rejected with
// 401 (both with no cookie and with a tampered cookie), a request targeting a
// path outside the roots is rejected with 403, and a request carrying a valid
// signed cookie is accepted (202) and actually performs the compression.
func TestServeCompressRequiresAuth(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("no cookie is rejected", func(t *testing.T) {
		ep := newCompressEndpoint(t, parent)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody([]string{filepath.Join(src, "a.txt")}, "zip")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
		// Nothing was compressed when unauthorized.
		entries, _ := os.ReadDir(src)
		if len(entries) != 1 {
			t.Errorf("unauthorized compress created files: %v", entries)
		}
	})

	t.Run("tampered cookie is rejected", func(t *testing.T) {
		ep := newCompressEndpoint(t, parent)
		req := httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody([]string{filepath.Join(src, "a.txt")}, "zip"))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"})
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("outside roots is rejected", func(t *testing.T) {
		ep := newCompressEndpoint(t, parent)
		req := httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody([]string{"/etc/passwd"}, "zip"))
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid cookie is accepted and compresses", func(t *testing.T) {
		ep := newCompressEndpoint(t, parent)
		guard := newTestGuard(t)
		req := httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody([]string{filepath.Join(src, "a.txt")}, "zip"))
		req.AddCookie(signedSession(t, guard))
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			TaskID string `json:"taskID"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.TaskID == "" {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}

		// The task runs on the pool; wait for it to finish, then the archive exists.
		done := make(chan struct{})
		go func() {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if s := ep.Svc.Get(resp.TaskID); s != nil && s.Status == task.StatusFinished {
					close(done)
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Fatal("compress task did not finish")
		}
		// The archive should be created as a.zip in the same directory as a.txt
		if _, err := os.Stat(filepath.Join(src, "a.zip")); os.IsNotExist(err) {
			t.Fatalf("a.zip was not created: %v", err)
		}
		// Original file still exists
		if _, err := os.Stat(filepath.Join(src, "a.txt")); os.IsNotExist(err) {
			t.Fatalf("a.txt was removed: %v", err)
		}
	})
}

// TestServeCompressUnsupportedFormat verifies an unsupported format returns 400
// with the human "supported: ..." message, not a panic.
func TestServeCompressUnsupportedFormat(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	ep := newCompressEndpoint(t, parent)
	req := httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody([]string{filepath.Join(src, "notes.txt")}, "bz2"))
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("supported")) {
		t.Errorf("body = %q, want it to list supported formats", rec.Body.String())
	}
}

// TestServeCompressRequiresPathsAndFormat verifies the endpoint rejects an empty
// or malformed body with 400 without enqueuing a task.
func TestServeCompressRequiresPathsAndFormat(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.zip"), []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	ep := newCompressEndpoint(t, parent)
	cookie := signedSession(t, newTestGuard(t))

	t.Run("empty paths", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody(nil, "zip"))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if n := len(ep.Svc.Snapshot()); n != 0 {
			t.Errorf("tasks enqueued = %d, want 0", n)
		}
	})

	t.Run("empty format", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody([]string{filepath.Join(src, "a.zip")}, ""))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if n := len(ep.Svc.Snapshot()); n != 0 {
			t.Errorf("tasks enqueued = %d, want 0", n)
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/compress", bytes.NewReader([]byte(`not-json`)))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

// TestServeCompressConflictNumbering verifies a colliding target is renumbered
// " (1)", " (2)", … before the extension, and never overwrites an existing file.
func TestServeCompressConflictNumbering(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The target (a.zip) and its first numbered variant already sit beside a.txt.
	for _, name := range []string{"a.zip", "a (1).zip"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ep := newCompressEndpoint(t, parent)
	guard := newTestGuard(t)
	req := httptest.NewRequest(http.MethodPost, "/api/compress", compressRequestBody([]string{filepath.Join(src, "a.txt")}, "zip"))
	req.AddCookie(signedSession(t, guard))
	rec := httptest.NewRecorder()
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.TaskID == "" {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}

	done := make(chan struct{})
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if s := ep.Svc.Get(resp.TaskID); s != nil && s.Status == task.StatusFinished {
				close(done)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("compress task did not finish")
	}

	// The pre-existing a.zip and a (1).zip must be untouched
	for _, name := range []string{"a.zip", "a (1).zip"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil || string(b) != "old" {
			t.Errorf("pre-existing %s = %q, want old (must not be overwritten)", name, b)
		}
	}
	// The new archive should be a (2).zip
	b, err := os.ReadFile(filepath.Join(src, "a (2).zip"))
	if err != nil {
		t.Fatalf("a (2).zip was not created: %v", err)
	}
	// Verify it's a valid zip with the content
	// We just check it exists and is non-empty
	if len(b) == 0 {
		t.Errorf("a (2).zip is empty")
	}
}
