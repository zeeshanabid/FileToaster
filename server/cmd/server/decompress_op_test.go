package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
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

// decompressEndpoint bundles the auth-wrapped decompress handler with the
// service, wired the same way serve.go wires them, so tests exercise the real
// protection and task lifecycle. `root` is the single allowed folder the
// decompress may reference.
type decompressEndpoint struct {
	Handler http.Handler
	Svc     *service.Service
}

func newDecompressEndpoint(t *testing.T, root string) decompressEndpoint {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.AllowedPaths = []config.AllowedPath{{Name: "Files", Path: root}}
	cfg.MaxParallelTasks = 4
	fsCfg := newFSConfig(cfg)
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-instance")
	registerDecompressOp(svc, roots, cfg)
	taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	guard := newTestGuard(t)
	return decompressEndpoint{
		Handler: guard.Middleware(http.HandlerFunc(taskSrv.ServeDecompress)),
		Svc:     svc,
	}
}

// decompressRequestBody marshals a decompress request body (paths + into).
func decompressRequestBody(paths []string, into string) io.Reader {
	b, _ := json.Marshal(map[string]any{"paths": paths, "into": into})
	return bytes.NewReader(b)
}

// makeZip writes a small zip archive at path with the given entry layout.
func makeZip(t *testing.T, path string, layout map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range layout {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitTaskFinished(ep decompressEndpoint, taskID string, timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if s := ep.Svc.Get(taskID); s != nil && s.Status == task.StatusFinished {
				close(done)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout + time.Second):
		return errors.New("decompress task did not finish in time")
	}
}

// TestServeDecompressRequiresAuth verifies the decompress endpoint is protected
// by the auth middleware: no cookie or tampered cookie is rejected with 401, and
// a valid signed cookie is accepted (202) and actually extracts the archive
// while leaving the original byte-identical.
func TestServeDecompressRequiresAuth(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	makeZip(t, filepath.Join(src, "pkg.zip"), map[string]string{"a.txt": "payload", "sub/deep.txt": "deep"})

	t.Run("no cookie is rejected", func(t *testing.T) {
		ep := newDecompressEndpoint(t, parent)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/decompress", decompressRequestBody([]string{filepath.Join(src, "pkg.zip")}, "")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
		// Nothing was extracted when unauthorized.
		if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
			t.Errorf("unauthorized decompress wrote a file: stat err = %v", err)
		}
	})

	t.Run("tampered cookie is rejected", func(t *testing.T) {
		ep := newDecompressEndpoint(t, parent)
		req := httptest.NewRequest(http.MethodPost, "/api/decompress", decompressRequestBody([]string{filepath.Join(src, "pkg.zip")}, ""))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"})
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid cookie is accepted and extracts", func(t *testing.T) {
		ep := newDecompressEndpoint(t, parent)
		guard := newTestGuard(t)
		req := httptest.NewRequest(http.MethodPost, "/api/decompress", decompressRequestBody([]string{filepath.Join(src, "pkg.zip")}, ""))
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
		if err := waitTaskFinished(ep, resp.TaskID, 5*time.Second); err != nil {
			t.Fatalf("decompress task did not finish: %v", err)
		}
		if b, _ := os.ReadFile(filepath.Join(src, "a.txt")); string(b) != "payload" {
			t.Errorf("a.txt = %q, want payload", b)
		}
		if b, _ := os.ReadFile(filepath.Join(src, "sub", "deep.txt")); string(b) != "deep" {
			t.Errorf("sub/deep.txt = %q, want deep", b)
		}
		// The original archive is intact.
		if _, err := os.Stat(filepath.Join(src, "pkg.zip")); err != nil {
			t.Errorf("original archive vanished: %v", err)
		}
	})
}

// TestServeDecompressUnsupportedFormat verifies an unsupported file reaching the
// backend returns 400 with the human "supported: ..." message, not a panic.
func TestServeDecompressUnsupportedFormat(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	ep := newDecompressEndpoint(t, parent)
	req := httptest.NewRequest(http.MethodPost, "/api/decompress", decompressRequestBody([]string{filepath.Join(src, "notes.txt")}, ""))
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

// TestServeDecompressRequiresSingleFile verifies the endpoint rejects an empty
// or multi-path body with 400 without enqueuing a task.
func TestServeDecompressRequiresSingleFile(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	makeZip(t, filepath.Join(src, "a.zip"), map[string]string{"a.txt": "1"})
	makeZip(t, filepath.Join(src, "b.zip"), map[string]string{"b.txt": "2"})
	ep := newDecompressEndpoint(t, parent)

	t.Run("empty paths", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/decompress", decompressRequestBody(nil, ""))
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("multiple paths", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/decompress", decompressRequestBody([]string{
			filepath.Join(src, "a.zip"),
			filepath.Join(src, "b.zip"),
		}, ""))
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if n := len(ep.Svc.Snapshot()); n != 0 {
			t.Errorf("tasks enqueued = %d, want 0", n)
		}
	})
}
