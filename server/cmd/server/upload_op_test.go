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
	"strconv"
	"testing"
	"time"

	"filetoaster/internal/auth"
	"filetoaster/internal/config"
	"filetoaster/internal/fs"
	"filetoaster/internal/protocol"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
	"filetoaster/internal/upload"
)

// uploadEndpoint bundles the upload + task handlers with the service, wired the
// way serve.go wires them, so tests exercise the real auth protection and the
// full task lifecycle (chunk ingestion → tracked finalize op → staged copy).
// `root` is the single allowed folder the upload destination resolves into;
// tmpDir is the staging area outside the roots.
type uploadEndpoint struct {
	Handler http.Handler
	Svc     *service.Service
}

func newUploadEndpoint(t *testing.T, root, tmpDir string) uploadEndpoint {
	t.Helper()
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("create tmp_dir: %v", err)
	}
	cfg := &config.Config{
		AllowedPaths:     []config.AllowedPath{{Name: "Files", Path: root}},
		MaxParallelTasks: 4,
	}
	fsCfg := newFSConfig(cfg)
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registerUploadOp(svc, roots)
	sessions := upload.NewRegistry()
	taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), protocol.WithUploadStaging(tmpDir, sessions, roots))
	guard := newTestGuard(t)
	mux := new(http.ServeMux)
	mux.Handle("POST /api/uploads", guard.Middleware(http.HandlerFunc(taskSrv.ServeCreateUpload)))
	mux.Handle("PUT /api/uploads/{id}/chunk", guard.Middleware(http.HandlerFunc(taskSrv.ServeUploadChunk)))
	mux.Handle("GET /api/uploads/{id}", guard.Middleware(http.HandlerFunc(taskSrv.ServeUploadStatus)))
	mux.Handle("DELETE /api/uploads/{id}", guard.Middleware(http.HandlerFunc(taskSrv.ServeUploadCancel)))
	return uploadEndpoint{Handler: mux, Svc: svc}
}

// uploadCreateBody marshals an upload-create request (dest, chunk size, files,
// dirs) into a JSON body.
func uploadCreateBody(dest string, chunkSize int64, files []upload.File, dirs []string) io.Reader {
	b, _ := json.Marshal(map[string]any{"dest": dest, "chunkSize": chunkSize, "files": files, "dirs": dirs})
	return bytes.NewReader(b)
}

// uploadChunk sends one chunk to the endpoint, failing the test on a non-202.
func uploadChunk(t *testing.T, ep uploadEndpoint, guard *auth.Guard, id string, file, index int, data string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/uploads/"+id+"/chunk?file="+strconv.Itoa(file)+"&index="+strconv.Itoa(index), bytes.NewReader([]byte(data)))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("chunk %d/%d status = %d, want 202 (body %s)", file, index, rec.Code, rec.Body.String())
	}
}

// waitForStatus polls the service until the task reaches want or the deadline
// elapses; returns nil if the deadline passes first.
func waitForStatus(ep uploadEndpoint, id string, want task.Status) *task.Task {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if t := ep.Svc.Get(id); t != nil && t.Status == want {
			return t
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func TestUploadCreateRequiresAuth(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	ep := newUploadEndpoint(t, parent, filepath.Join(parent, "tmp"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/uploads",
		uploadCreateBody(dest, upload.DefaultChunkSize, []upload.File{{Rel: "a.txt", Size: 4}}, nil))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestUploadCreateRejectsInvalidManifest(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	guard := newTestGuard(t)
	ep := newUploadEndpoint(t, parent, filepath.Join(parent, "tmp"))

	for _, tc := range []struct {
		name     string
		body     io.Reader
		wantCode int
	}{{
		name:     "empty dest",
		body:     uploadCreateBody("", upload.DefaultChunkSize, []upload.File{{Rel: "a.txt", Size: 4}}, nil),
		wantCode: http.StatusBadRequest,
	}, {
		name:     "empty files",
		body:     uploadCreateBody(dest, upload.DefaultChunkSize, nil, nil),
		wantCode: http.StatusBadRequest,
	}, {
		name:     "path traversal in entry name",
		body:     uploadCreateBody(dest, upload.DefaultChunkSize, []upload.File{{Rel: "../escape.txt", Size: 4}}, nil),
		wantCode: http.StatusBadRequest,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/uploads", tc.body)
			req.AddCookie(signedSession(t, guard))
			rec := httptest.NewRecorder()
			ep.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// TestUploadCreateRefusesMissingTmpDir verifies the upload endpoint refuses to
// stage when the configured tmp_dir does not exist — it must not create it.
func TestUploadCreateRefusesMissingTmpDir(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	// tmpDir deliberately does not exist; the handler must refuse, not create it.
	tmpDir := filepath.Join(parent, "tmp")
	guard := newTestGuard(t)
	ep := newUploadEndpoint(t, parent, tmpDir)
	// Drop the dir the harness created so the endpoint sees a missing staging area.
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/uploads",
		uploadCreateBody(dest, upload.DefaultChunkSize, []upload.File{{Rel: "a.txt", Size: 4}}, nil))
	req.AddCookie(signedSession(t, guard))
	rec := httptest.NewRecorder()
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("tmp_dir was created (err=%v), want it left uncreated", err)
	}
}

// TestUploadFullFlowSingleChunk drives the complete path: create a session, send
// the single chunk, watch the auto-triggered finalize task run, and assert the
// staged file was copied into the destination and the staging dir was consumed.
func TestUploadFullFlowSingleChunk(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	ep := newUploadEndpoint(t, parent, tmpDir)
	guard := newTestGuard(t)

	// Create the session.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/uploads",
		uploadCreateBody(dest, 4, []upload.File{{Rel: "a.txt", Size: 4, Chunks: 1}}, nil))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v body=%s", err, rec.Body.String())
	}

	// Upload the one chunk; completion auto-triggers the finalize op.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/uploads/"+created.TaskID+"/chunk?file=0&index=0", bytes.NewReader([]byte("abcd")))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("chunk status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var finished struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&finished); err != nil || finished.TaskID == "" {
		t.Fatalf("decode finalize: %v body=%s", err, rec.Body.String())
	}

	// The finalize op runs on the pool; wait for it to finish.
	if task := waitForStatus(ep, finished.TaskID, task.StatusFinished); task == nil {
		t.Fatal("finalize task did not finish")
	}

	// The staged file was copied into the destination and the staging dir removed.
	if b, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(b) != "abcd" {
		t.Fatalf("final file = %q, err %v", b, err)
	}
	matched, _ := filepath.Glob(filepath.Join(tmpDir, "filetoaster-*"))
	if len(matched) != 0 {
		t.Errorf("staging dir not consumed: %v", matched)
	}
}

// TestUploadResumeStatusAndMultiChunk verifies the status endpoint reports partial
// progress so a client can resume, and that the upload finalizes only once every
// chunk of every file has arrived.
func TestUploadResumeStatusAndMultiChunk(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	ep := newUploadEndpoint(t, parent, tmpDir)
	guard := newTestGuard(t)

	// Two files: a.txt (2 chunks) and sub/b.txt (1 chunk) = 3 total chunks.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/uploads",
		uploadCreateBody(dest, 4,
			[]upload.File{{Rel: "a.txt", Size: 8, Chunks: 2}, {Rel: "sub/b.txt", Size: 4, Chunks: 1}},
			[]string{"sub"}))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v body=%s", err, rec.Body.String())
	}

	// Send the first chunk of the first file only.
	uploadChunk(t, ep, guard, created.TaskID, 0, 0, "0123")

	// The status endpoint reports the partial progress so the client resumes.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/uploads/"+created.TaskID, nil)
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var prog struct {
		TotalChunks    int `json:"totalChunks"`
		ReceivedChunks int `json:"receivedChunks"`
		TotalBytes     int64
		ReceivedBytes  int64
	}
	if err := json.NewDecoder(rec.Body).Decode(&prog); err != nil {
		t.Fatalf("decode status: %v body=%s", err, rec.Body.String())
	}
	if prog.TotalChunks != 3 || prog.ReceivedChunks != 1 || prog.TotalBytes != 12 || prog.ReceivedBytes != 4 {
		t.Errorf("progress = %+v, want totalChunks 3 receivedChunks 1 totalBytes 12 receivedBytes 4", prog)
	}

	// Resume the missing chunks (1 of file 0, then the completing chunk of file 1).
	uploadChunk(t, ep, guard, created.TaskID, 0, 1, "4567")

	// The final chunk completes the upload and returns the finalize task id.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/uploads/"+created.TaskID+"/chunk?file=1&index=0", bytes.NewReader([]byte("abcd")))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("completing chunk status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var finished struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&finished); err != nil || finished.TaskID == "" {
		t.Fatalf("decode finalize: %v body=%s", err, rec.Body.String())
	}

	if task := waitForStatus(ep, finished.TaskID, task.StatusFinished); task == nil {
		t.Fatal("finalize task did not finish")
	}
	if b, err := os.ReadFile(filepath.Join(dest, "a.txt")); err != nil || string(b) != "01234567" {
		t.Fatalf("final a.txt = %q, err %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "sub", "b.txt")); err != nil || string(b) != "abcd" {
		t.Fatalf("final sub/b.txt = %q, err %v", b, err)
	}
	matched, _ := filepath.Glob(filepath.Join(tmpDir, "filetoaster-*"))
	if len(matched) != 0 {
		t.Errorf("staging dir not consumed: %v", matched)
	}
}

// TestUploadCancelKeepsChunksResumable verifies cancelling a session keeps the
// already-received chunks so the upload can still be resumed, and that the
// staging dir survives (not finalized) until the age sweep reclaims it.
func TestUploadCancelKeepsChunksResumable(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	ep := newUploadEndpoint(t, parent, tmpDir)
	guard := newTestGuard(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/uploads",
		uploadCreateBody(dest, 4, []upload.File{{Rel: "a.txt", Size: 8, Chunks: 2}}, nil))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v body=%s", err, rec.Body.String())
	}

	uploadChunk(t, ep, guard, created.TaskID, 0, 0, "0123")

	// Cancel: the session stays in the registry, chunks kept.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/uploads/"+created.TaskID, nil)
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// The destination was never touched (no finalize happened).
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("canceled upload created a file at the destination")
	}
	// The staging dir survives for the grace period (age sweep reclaims later).
	if _, err := os.Stat(filepath.Join(tmpDir, "filetoaster-"+created.TaskID)); err != nil {
		t.Errorf("staging dir after cancel: %v", err)
	}
}

func TestUploadRejectsOutOfRangeChunk(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	ep := newUploadEndpoint(t, parent, filepath.Join(parent, "tmp"))
	guard := newTestGuard(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/uploads",
		uploadCreateBody(dest, 4, []upload.File{{Rel: "a.txt", Size: 4, Chunks: 1}}, nil))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v body=%s", err, rec.Body.String())
	}

	// Chunk index 9 is out of range for the single-chunk file.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/uploads/"+created.TaskID+"/chunk?file=0&index=9", bytes.NewReader([]byte("abcd")))
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("out-of-range chunk status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestUploadStatusUnknownSessionReturns404(t *testing.T) {
	parent := t.TempDir()
	ep := newUploadEndpoint(t, parent, filepath.Join(parent, "tmp"))
	guard := newTestGuard(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/uploads/does-not-exist", nil)
	req.AddCookie(signedSession(t, guard))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestUploadChunkRejectsOutOfRangeWithoutAuth(t *testing.T) {
	parent := t.TempDir()
	ep := newUploadEndpoint(t, parent, filepath.Join(parent, "tmp"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/uploads/x/chunk?file=0&index=0", bytes.NewReader([]byte("abcd")))
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}
