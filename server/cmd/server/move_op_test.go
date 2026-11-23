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

// moveEndpoint bundles the auth-wrapped move handler with the service, wired the
// same way serve.go wires them, so tests exercise the real protection and task
// lifecycle. `root` is the single allowed folder the move may reference.
type moveEndpoint struct {
	Handler http.Handler
	Svc     *service.Service
}

func newMoveEndpoint(t *testing.T, root string) moveEndpoint {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.AllowedPaths = []config.AllowedPath{{Name: "Files", Path: root}}
	cfg.MaxParallelTasks = 4
	fsCfg := newFSConfig(cfg)
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-instance")
	registerMoveOp(svc, roots, cfg)
	taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	guard := newTestGuard(t)
	return moveEndpoint{
		Handler: guard.Middleware(http.HandlerFunc(taskSrv.ServeMove)),
		Svc:     svc,
	}
}

// moveRequestBody marshals a move request body (paths + dest).
func moveRequestBody(paths []string, dest string) io.Reader {
	b, _ := json.Marshal(map[string]any{"paths": paths, "dest": dest})
	return bytes.NewReader(b)
}

// TestServeMoveRequiresAuth verifies the move endpoint is protected by the auth
// middleware: requests without a valid session cookie are rejected with 401
// (both with no cookie and with a tampered cookie), a request targeting a path
// outside the roots is rejected with 403, and a request carrying a valid signed
// cookie is accepted (202) and actually performs the move (the source is gone
// and the destination holds the content).
func TestServeMoveRequiresAuth(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("no cookie is rejected", func(t *testing.T) {
		ep := newMoveEndpoint(t, parent)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/move", moveRequestBody([]string{filepath.Join(src, "a.txt")}, dstDir)))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
		// Nothing was moved when unauthorized: the source is still there.
		if _, err := os.Stat(filepath.Join(src, "a.txt")); os.IsNotExist(err) {
			t.Errorf("unauthorized move removed the source")
		}
	})

	t.Run("tampered cookie is rejected", func(t *testing.T) {
		ep := newMoveEndpoint(t, parent)
		req := httptest.NewRequest(http.MethodPost, "/api/move", moveRequestBody([]string{filepath.Join(src, "a.txt")}, dstDir))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"})
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("outside roots is rejected", func(t *testing.T) {
		ep := newMoveEndpoint(t, parent)
		req := httptest.NewRequest(http.MethodPost, "/api/move", moveRequestBody([]string{"/etc/passwd"}, dstDir))
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid cookie is accepted and moves", func(t *testing.T) {
		ep := newMoveEndpoint(t, parent)
		guard := newTestGuard(t)
		req := httptest.NewRequest(http.MethodPost, "/api/move", moveRequestBody([]string{filepath.Join(src, "a.txt")}, dstDir))
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

		// The task runs on the pool; wait for it to finish, then source is gone.
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
			t.Fatal("move task did not finish")
		}
		if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
			t.Fatalf("a.txt still exists after move: %v", err)
		}
		if b, err := os.ReadFile(filepath.Join(dstDir, "a.txt")); err != nil || string(b) != "payload" {
			t.Fatalf("moved file = %q, err %v", b, err)
		}
	})

	t.Run("move overwrites an existing destination file", func(t *testing.T) {
		parent := t.TempDir()
		src := filepath.Join(parent, "src")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0o644); err != nil {
			t.Fatal(err)
		}
		dstDir := filepath.Join(parent, "dst")
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, "a.txt"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		ep := newMoveEndpoint(t, parent)
		guard := newTestGuard(t)
		req := httptest.NewRequest(http.MethodPost, "/api/move", moveRequestBody([]string{filepath.Join(src, "a.txt")}, dstDir))
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
			t.Fatal("move task did not finish")
		}
		// The existing file was overwritten (no " (copy)" suffix, move semantics).
		if b, err := os.ReadFile(filepath.Join(dstDir, "a.txt")); err != nil || string(b) != "new" {
			t.Fatalf("overwritten file = %q, err %v, want new", b, err)
		}
		if _, err := os.Stat(filepath.Join(dstDir, "a (copy).txt")); err == nil {
			t.Errorf("move appended a copy suffix: a (copy).txt exists")
		}
	})
}
