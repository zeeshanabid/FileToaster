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

// copyEndpoint bundles the auth-wrapped copy handler with the service, wired the
// same way serve.go wires them, so tests exercise the real protection and task
// lifecycle. `root` is the single allowed folder the copy may reference.
type copyEndpoint struct {
	Handler http.Handler
	Svc     *service.Service
}

func newCopyEndpoint(t *testing.T, root string) copyEndpoint {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.AllowedPaths = []config.AllowedPath{{Name: "Files", Path: root}}
	cfg.MaxParallelTasks = 4
	fsCfg := newFSConfig(cfg)
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-instance")
	registerCopyOp(svc, roots, cfg)
	taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	guard := newTestGuard(t)
	return copyEndpoint{
		Handler: guard.Middleware(http.HandlerFunc(taskSrv.ServeCopy)),
		Svc:     svc,
	}
}

// copyRequestBody marshals a copy request body (paths + dest + conflict).
func copyRequestBody(paths []string, dest, conflict string) io.Reader {
	b, _ := json.Marshal(map[string]any{"paths": paths, "dest": dest, "conflict": conflict})
	return bytes.NewReader(b)
}

// TestServeCopyRequiresAuth verifies the copy endpoint is protected by the auth
// middleware: requests without a valid session cookie are rejected with 401
// (both with no cookie and with a tampered cookie), and a request carrying a
// valid signed cookie is accepted (202) and actually performs the copy.
func TestServeCopyRequiresAuth(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(parent, "dst")

	t.Run("no cookie is rejected", func(t *testing.T) {
		ep := newCopyEndpoint(t, parent)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/copy", copyRequestBody([]string{filepath.Join(src, "a.txt")}, dstDir, "")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
		// Nothing was written to the destination when unauthorized.
		if _, err := os.Stat(filepath.Join(dstDir, "a.txt")); !os.IsNotExist(err) {
			t.Errorf("unauthorized copy wrote a file: stat err = %v", err)
		}
	})

	t.Run("tampered cookie is rejected", func(t *testing.T) {
		ep := newCopyEndpoint(t, parent)
		req := httptest.NewRequest(http.MethodPost, "/api/copy", copyRequestBody([]string{filepath.Join(src, "a.txt")}, dstDir, ""))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"})
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid cookie is accepted and copies", func(t *testing.T) {
		ep := newCopyEndpoint(t, parent)
		guard := newTestGuard(t)
		req := httptest.NewRequest(http.MethodPost, "/api/copy", copyRequestBody([]string{filepath.Join(src, "a.txt")}, dstDir, ""))
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

		// The task runs on the pool; wait for it to finish, then the file exists.
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
			t.Fatal("copy task did not finish")
		}
		got, err := os.ReadFile(filepath.Join(dstDir, "a.txt"))
		if err != nil || string(got) != "payload" {
			t.Fatalf("copied file = %q, err %v", got, err)
		}
	})
}
