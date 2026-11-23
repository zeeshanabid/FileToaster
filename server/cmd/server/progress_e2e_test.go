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
	"sync"
	"testing"
	"time"

	"filetoaster/internal/auth"
	"filetoaster/internal/config"
	"filetoaster/internal/fs"
	"filetoaster/internal/protocol"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
)

func buildCopySvc(t *testing.T, root string) (http.Handler, *service.Service) {
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
	return guard.Middleware(http.HandlerFunc(taskSrv.ServeCopy)), svc
}

func TestReproHubProgressFlows(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := 0
	for i := 0; i < 20; i++ {
		line := bytes.Repeat([]byte("x"), 1000)
		if err := os.WriteFile(filepath.Join(src, "f"+string(rune('a'+i))+".txt"), line, 0o644); err != nil {
			t.Fatal(err)
		}
		want += len(line)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "g.txt"), bytes.Repeat([]byte("y"), 500), 0o644); err != nil {
		t.Fatal(err)
	}
	want += 500

	dst := filepath.Join(parent, "dst")
	handler, svc := buildCopySvc(t, parent)

	// Subscribe to the hub the same way /api/events does.
	sub := svc.Subscribe(nil)
	defer svc.Hub().Unsubscribe(sub)

	// Signed cookie.
	guard := newTestGuard(t)
	req := httptest.NewRequest(http.MethodPost, "/api/copy",
		copyRequestBody([]string{src}, dst, ""))
	req.AddCookie(signedSession(t, guard))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.TaskID == "" {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}

	// Collect published snapshots for this task.
	var mu sync.Mutex
	maxDone := int64(0)
	seenRunning := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev, ok := <-sub.Out():
			if !ok {
				goto done
			}
			if ev.ID != resp.TaskID {
				continue
			}
			mu.Lock()
			if ev.Status == task.StatusRunning {
				seenRunning = true
			}
			if ev.Bytes.Done > maxDone {
				maxDone = ev.Bytes.Done
			}
			t.Logf("ev status=%s done=%d total=%d filesDone=%d filesTotal=%d rate=%d progress=%.1f",
				ev.Status, ev.Bytes.Done, ev.Bytes.Total, ev.Bytes.FilesDone, ev.Bytes.FilesTotal, ev.Bytes.RatePerSec, ev.Progress)
			mu.Unlock()
		case <-time.After(20 * time.Millisecond):
			if s := svc.Get(resp.TaskID); s != nil && s.Status.IsTerminal() {
				// Drain any remaining buffered events, then stop.
				for {
					select {
					case ev := <-sub.Out():
						if ev.ID != resp.TaskID {
							continue
						}
						mu.Lock()
						if ev.Bytes.Done > maxDone {
							maxDone = ev.Bytes.Done
						}
						t.Logf("final ev status=%s done=%d total=%d progress=%.1f", ev.Status, ev.Bytes.Done, ev.Bytes.Total, ev.Progress)
						mu.Unlock()
					default:
						goto done
					}
				}
			}
		}
	}
done:
	t.Logf("maxDone=%d want=%d seenRunning=%v", maxDone, want, seenRunning)
	if !seenRunning {
		t.Errorf("task never published a running snapshot")
	}
	if maxDone <= 0 {
		t.Errorf("hub never saw progress bytes; maxDone=%d want>0", maxDone)
	}
	if maxDone != int64(want) {
		t.Errorf("maxDone=%d want=%d", maxDone, want)
	}
	_ = auth.CookieName
}
