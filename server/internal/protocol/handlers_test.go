package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
)

// newCopyService builds a service with the copy operation wired the same way
// cmd/server does, along with its protocol server. `roots` are the allowed
// folders the copy may reference.
func newCopyService(roots ...string) *protocolHarness {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.NewService(4, logger, "test-instance")
	resolve := func() *fs.Roots { return fs.NewRoots(roots...) }
	svc.Register(service.CopyOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req struct {
				Paths []string `json:"paths"`
				Dest  string   `json:"dest"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, err
			}
			plan, err := ops.Resolve(resolve(), req.Paths, req.Dest)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.Run(ctx, spec.(*ops.Plan), prog)
		},
	})
	return &protocolHarness{srv: NewServer(svc, logger), svc: svc}
}

type protocolHarness struct {
	srv *Server
	svc *service.Service
}

func copyReq(paths []string, dest, conflict string) string {
	body, _ := json.Marshal(map[string]any{"paths": paths, "dest": dest, "conflict": conflict})
	return string(body)
}

func postCopy(h *protocolHarness, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.srv.ServeCopy(rec, req)
	return rec
}

func TestServeCopySuccess(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(parent, "dst")

	h := newCopyService(parent)
	rec := postCopy(h, copyReq([]string{filepath.Join(src, "a.txt")}, dstDir, ""))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.TaskID == "" {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}

	// The task runs on the pool; wait for it to finish, then the file exists.
	done := make(chan struct{})
	go func() {
		waitFor(t, func() bool {
			s := h.svc.Get(resp.TaskID)
			return s != nil && s.Status == task.StatusFinished
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("copy task did not finish")
	}
	got, err := os.ReadFile(filepath.Join(dstDir, "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("copied file = %q, err %v", got, err)
	}
}

func TestServeCopyResolvesConflictByRename(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	dstDir := filepath.Join(parent, "dst")
	for _, d := range []string{src, dstDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newCopyService(parent)
	// No overwrite decision: with the rename-on-conflict behavior the task still
	// submits (202) and the copy lands alongside the pre-existing file.
	rec := postCopy(h, copyReq([]string{filepath.Join(src, "a.txt")}, dstDir, ""))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.TaskID == "" {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}

	done := make(chan struct{})
	go func() {
		waitFor(t, func() bool {
			s := h.svc.Get(resp.TaskID)
			return s != nil && s.Status == task.StatusFinished
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("copy task did not finish")
	}
	if b, _ := os.ReadFile(filepath.Join(dstDir, "a.txt")); string(b) != "old" {
		t.Errorf("dst a.txt = %q, want old (not clobbered)", b)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "a (copy).txt")); err != nil || string(b) != "new" {
		t.Errorf("dst a (copy).txt = %q, err %v, want new", b, err)
	}
}

func TestServeCopyConflictFieldIsIgnored(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	dstDir := filepath.Join(parent, "dst")
	for _, d := range []string{src, dstDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newCopyService(parent)
	// The client's overwrite flag is now ignored; the copy submits and the
	// conflict is resolved server-side by renaming.
	rec := postCopy(h, copyReq([]string{filepath.Join(src, "a.txt")}, dstDir, "overwrite"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeCopyBadRequest(t *testing.T) {
	parent := t.TempDir()
	h := newCopyService(parent)
	for _, body := range []string{`{}`, `{"dest":"/x"}`, `{"paths":["/a"]}`} {
		rec := postCopy(h, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestServeCopyOutsideRoots(t *testing.T) {
	parent := t.TempDir()
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newCopyService(parent)
	rec := postCopy(h, copyReq([]string{"/etc/passwd"}, dstDir, ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestTasksListAndGet(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newCopyService(parent)

	// Copy into a fresh (non-existent) destination inside the roots: the task
	// runs and finishes, leaving the copy behind.
	missingDest := filepath.Join(parent, "missing-dest")
	rec := postCopy(h, copyReq([]string{filepath.Join(src, "a.txt")}, missingDest, ""))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var submitted struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// List: the task appears, oldest first.
	listRec := httptest.NewRecorder()
	h.srv.ServeTasksList(listRec, httptest.NewRequest(http.MethodGet, "/api/tasks", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRec.Code)
	}
	var list struct {
		Tasks []task.Task `json:"tasks"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if !containsID(list.Tasks, submitted.TaskID) {
		t.Fatalf("task list %v missing %s", list.Tasks, submitted.TaskID)
	}

	// Per-task fetch.
	getRec := httptest.NewRecorder()
	h.srv.ServeTask(getRec, httptest.NewRequest(http.MethodGet, "/api/tasks/"+submitted.TaskID, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d", getRec.Code)
	}
	var gotTask task.Task
	if err := json.Unmarshal(getRec.Body.Bytes(), &gotTask); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if gotTask.ID != submitted.TaskID {
		t.Fatalf("task id = %q, want %q", gotTask.ID, submitted.TaskID)
	}
}

// TestServeClearTasksKeepsActive verifies the clear endpoint deletes terminal
// records (so they do not return in a later snapshot after a browser refresh)
// while leaving running work tracked.
func TestServeClearTasksKeepsActive(t *testing.T) {
	h := newCopyService(t.TempDir())
	store := h.svc.Store()
	store.Add(&task.Task{ID: "finished", Type: "upload", Status: task.StatusFinished})
	store.Add(&task.Task{ID: "failed", Type: "upload", Status: task.StatusFailed})
	store.Add(&task.Task{ID: "running", Type: "copy", Status: task.StatusRunning})
	store.Add(&task.Task{ID: "queued", Type: "copy", Status: task.StatusQueued})

	rec := httptest.NewRecorder()
	h.srv.ServeClearTasks(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/clear", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cleared struct {
		Cleared []string `json:"cleared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(cleared.Cleared, []string{"failed", "finished"}) {
		t.Fatalf("cleared = %v, want [failed finished]", cleared.Cleared)
	}
	if got := h.svc.Get("finished"); got != nil {
		t.Fatalf("finished task still present after clear: %+v", got)
	}
	if got := h.svc.Get("running"); got == nil || got.Status != task.StatusRunning {
		t.Fatalf("running task was not preserved: %+v", got)
	}
	if got := h.svc.Get("queued"); got == nil || got.Status != task.StatusQueued {
		t.Fatalf("queued task was not preserved: %+v", got)
	}
}

// TestServeCancelRunning submits a long-running (blocking) op and verifies the
// cancel endpoint stops a running task deterministically.
func TestServeCancelRunning(t *testing.T) {
	h := newCopyService(t.TempDir())
	h.svc.Register("block", service.Op{
		Prepare: func(body []byte) (any, []byte, error) { return struct{}{}, nil, nil },
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	t0, err := h.svc.Submit("block", nil)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitFor(t, func() bool {
		s := h.svc.Get(t0.ID)
		return s != nil && s.Status == task.StatusRunning
	})

	// Route through a mux with the real pattern so the {id} path value is
	// captured the same way the production server does.
	mux := http.NewServeMux()
	mux.Handle("POST /api/tasks/{id}/cancel", http.HandlerFunc(h.srv.ServeCancel))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/"+t0.ID+"/cancel", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	waitFor(t, func() bool {
		s := h.svc.Get(t0.ID)
		return s != nil && s.Status == task.StatusCancelled
	})
}

func TestServeCancelUnknown(t *testing.T) {
	h := newCopyService(t.TempDir())
	rec := httptest.NewRecorder()
	h.srv.ServeCancel(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/nope/cancel", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestServeEventsSSE(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("payload-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newCopyService(parent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.srv.ServeEvents(w, r)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// Kick off a copy so a task event is published on the stream.
	sub := postCopy(h, copyReq([]string{filepath.Join(src, "a.txt")}, filepath.Join(parent, "sse-dest"), ""))
	if sub.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d", sub.Code)
	}

	// Read the stream until a task event arrives (or the deadline elapses).
	sawTaskEvent := false
	deadline := time.Now().Add(5 * time.Second)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: task") {
			sawTaskEvent = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}
	if !sawTaskEvent {
		t.Fatal("no 'event: task' received on SSE stream")
	}
}

func containsID(tasks []task.Task, id string) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

// waitFor polls cond until it returns true or the timeout elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
