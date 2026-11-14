package main

import (
  "bytes"
  "context"
  "encoding/json"
  "fmt"
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
  "filetoaster/internal/fs/ops"
  "filetoaster/internal/protocol"
  "filetoaster/internal/service"
  "filetoaster/internal/task"
)

// waitFor polls cond until it returns true or the deadline elapses. Delete tasks
// run on the pool asynchronously, so a test must wait for the pool to observe the
// task's transition between states rather than assume it is immediate.
func waitFor(t *testing.T, cond func() bool) {
  t.Helper()
  deadline := time.Now().Add(5 * time.Second)
  for time.Now().Before(deadline) {
    if cond() {
      return
    }
    time.Sleep(2 * time.Millisecond)
  }
  t.Fatalf("condition not met within deadline")
}

// deleteEndpoint bundles the auth-wrapped delete handler with the service, wired
// the same way serve.go wires them, so tests exercise the real protection and
// task lifecycle. `root` is the single allowed folder the delete may reference.
type deleteEndpoint struct {
  Handler http.Handler
  Svc     *service.Service
}

func newDeleteEndpoint(t *testing.T, root string) deleteEndpoint {
  t.Helper()
  cfg := &config.Config{
    AllowedPaths:     []config.AllowedPath{{Name: "Files", Path: root}},
    MaxParallelTasks: 4,
  }
  fsCfg := newFSConfig(cfg)
  roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
  svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)))
  registerDeleteOp(svc, roots)
  taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
  guard := newTestGuard(t)
  return deleteEndpoint{
    Handler: guard.Middleware(http.HandlerFunc(taskSrv.ServeDelete)),
    Svc:     svc,
  }
}

// deleteRequestBody marshals a delete request body (paths only; there is no
// destination for delete).
func deleteRequestBody(paths []string) io.Reader {
  b, _ := json.Marshal(map[string]any{"paths": paths})
  return bytes.NewReader(b)
}

// TestServeDeleteRequiresAuth verifies the delete endpoint is protected by the
// auth middleware: requests without a valid session cookie are rejected with 401
// (both with no cookie and with a tampered cookie), and a request carrying a
// valid signed cookie is accepted (202) and actually performs the deletion.
func TestServeDeleteRequiresAuth(t *testing.T) {
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  if err := os.MkdirAll(src, 0o755); err != nil {
    t.Fatal(err)
  }
  if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("payload"), 0o644); err != nil {
    t.Fatal(err)
  }

  t.Run("no cookie is rejected", func(t *testing.T) {
    ep := newDeleteEndpoint(t, parent)
    rec := httptest.NewRecorder()
    ep.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/delete", deleteRequestBody([]string{filepath.Join(src, "a.txt")})))
    if rec.Code != http.StatusUnauthorized {
      t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
    }
    // Nothing was deleted when unauthorized.
    if _, err := os.Stat(filepath.Join(src, "a.txt")); os.IsNotExist(err) {
      t.Errorf("unauthorized delete removed a file")
    }
  })

  t.Run("tampered cookie is rejected", func(t *testing.T) {
    ep := newDeleteEndpoint(t, parent)
    req := httptest.NewRequest(http.MethodPost, "/api/delete", deleteRequestBody([]string{filepath.Join(src, "a.txt")}))
    req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"})
    rec := httptest.NewRecorder()
    ep.Handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusUnauthorized {
      t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
    }
  })

  t.Run("valid cookie is accepted and deletes", func(t *testing.T) {
    ep := newDeleteEndpoint(t, parent)
    guard := newTestGuard(t)
    req := httptest.NewRequest(http.MethodPost, "/api/delete", deleteRequestBody([]string{filepath.Join(src, "a.txt")}))
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

    // The task runs on the pool; wait for it to finish, then the file is gone.
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
      t.Fatal("delete task did not finish")
    }
    if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
      t.Fatalf("a.txt still exists after delete: %v", err)
    }
  })

  t.Run("escape outside roots is rejected", func(t *testing.T) {
    ep := newDeleteEndpoint(t, parent)
    req := httptest.NewRequest(http.MethodPost, "/api/delete", deleteRequestBody([]string{"/etc/passwd"}))
    req.AddCookie(signedSession(t, newTestGuard(t)))
    rec := httptest.NewRecorder()
    ep.Handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusForbidden {
      t.Errorf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
    }
  })
}

// TestServeDeleteCancellable verifies a delete task submitted through /api/delete
// can be stopped through the (auth-wrapped) cancel endpoint and reaches
// StatusCancelled. The delete op's Run blocks on the context and then returns
// ctx.Err() — exactly the contract DeleteRun honors on cancellation — so the
// running state is observable and cancellation deterministic.
func TestServeDeleteCancellable(t *testing.T) {
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  if err := os.MkdirAll(src, 0o755); err != nil {
    t.Fatal(err)
  }
  if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("payload"), 0o644); err != nil {
    t.Fatal(err)
  }

  cfg := &config.Config{
    AllowedPaths:     []config.AllowedPath{{Name: "Files", Path: parent}},
    MaxParallelTasks: 4,
  }
  fsCfg := newFSConfig(cfg)
  roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
  svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)))
  guard := newTestGuard(t)
  taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

  // Register the delete op keyed the way production does. Prepare performs the
  // real path resolution (so the request shape is exercised); Run blocks on the
  // context and returns ctx.Err(), mirroring DeleteRun's cancel contract.
  svc.Register(service.DeleteOperation, service.Op{
    Prepare: func(body []byte) (any, []byte, error) {
      var req deleteTaskRequest
      if err := json.Unmarshal(body, &req); err != nil || len(req.Paths) == 0 {
        return nil, nil, fmt.Errorf("invalid delete request: %v", err)
      }
      plan, err := ops.DeleteResolve(roots(), req.Paths)
      if err != nil {
        return nil, nil, err
      }
      meta, _ := json.Marshal(plan)
      return plan, meta, nil
    },
    Run: func(ctx context.Context, spec any, prog *task.Progression) error {
      <-ctx.Done()
      return ctx.Err()
    },
  })

  mux := http.NewServeMux()
  mux.Handle("POST /api/delete", guard.Middleware(http.HandlerFunc(taskSrv.ServeDelete)))
  mux.Handle("POST /api/tasks/{id}/cancel", guard.Middleware(http.HandlerFunc(taskSrv.ServeCancel)))

  deleteReq := func(cookie *http.Cookie) *http.Request {
    req := httptest.NewRequest(http.MethodPost, "/api/delete", deleteRequestBody([]string{filepath.Join(src, "a.txt")}))
    if cookie != nil {
      req.AddCookie(cookie)
    }
    return req
  }

  // An unauthorized delete is rejected, so no task is ever created.
  rec := httptest.NewRecorder()
  mux.ServeHTTP(rec, deleteReq(nil))
  if rec.Code != http.StatusUnauthorized {
    t.Fatalf("unauthorized delete status = %d, want 401", rec.Code)
  }

  // An authenticated delete is accepted and returns a task id.
  rec = httptest.NewRecorder()
  mux.ServeHTTP(rec, deleteReq(signedSession(t, guard)))
  if rec.Code != http.StatusAccepted {
    t.Fatalf("delete status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
  }
  var resp struct {
    TaskID string `json:"taskID"`
  }
  if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.TaskID == "" {
    t.Fatalf("decode: %v body=%s", err, rec.Body.String())
  }
  taskID := resp.TaskID

  // Wait until the pool has picked the task up (running) before cancelling.
  waitFor(t, func() bool {
    s := svc.Get(taskID)
    return s != nil && s.Status == task.StatusRunning
  })

  // Cancel through the authenticated cancel endpoint.
  cancReq := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/cancel", nil)
  cancReq.AddCookie(signedSession(t, guard))
  rec = httptest.NewRecorder()
  mux.ServeHTTP(rec, cancReq)
  if rec.Code != http.StatusOK {
    t.Fatalf("cancel status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
  }

  // The task reaches StatusCancelled and the file was NOT deleted, proving the
  // cancellation propagated to the running task and stopped the work.
  waitFor(t, func() bool {
    s := svc.Get(taskID)
    return s != nil && s.Status == task.StatusCancelled
  })
  if _, err := os.Stat(filepath.Join(src, "a.txt")); os.IsNotExist(err) {
    t.Fatalf("cancellation did not prevent deletion")
  }
}
