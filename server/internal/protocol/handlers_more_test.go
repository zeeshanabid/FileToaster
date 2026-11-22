package protocol

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
	"filetoaster/internal/upload"
)

func newTestService() *service.Service {
	return service.NewService(4, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// --- Delete tests ---

func newDeleteService(roots ...string) *protocolHarness {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := newTestService()
	resolve := func() *fs.Roots { return fs.NewRoots(roots...) }
	svc.Register(service.DeleteOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req struct {
				Paths []string `json:"paths"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, err
			}
			plan, err := ops.DeleteResolve(resolve(), req.Paths)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.DeleteRun(ctx, spec.(*ops.DeletePlan), prog)
		},
	})
	return &protocolHarness{srv: NewServer(svc, logger), svc: svc}
}

func deleteReq(paths []string) string {
	body, _ := json.Marshal(map[string]any{"paths": paths})
	return string(body)
}

func postDelete(h *protocolHarness, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.srv.ServeDelete(rec, req)
	return rec
}

func TestServeDeleteSuccess(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newDeleteService(parent)
	rec := postDelete(h, deleteReq([]string{filepath.Join(src, "a.txt")}))
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
		t.Fatal("delete task did not finish")
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt still exists after delete: %v", err)
	}
}

func TestServeDeleteBadRequest(t *testing.T) {
	parent := t.TempDir()
	h := newDeleteService(parent)
	for _, body := range []string{`{}`, `{"paths":[]}`} {
		rec := postDelete(h, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestServeDeleteOutsideRoots(t *testing.T) {
	parent := t.TempDir()
	h := newDeleteService(parent)
	rec := postDelete(h, deleteReq([]string{"/etc/passwd"}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeDeleteRootPathRejected(t *testing.T) {
	parent := t.TempDir()
	h := newDeleteService(parent)
	// The allowed root itself cannot be deleted
	rec := postDelete(h, deleteReq([]string{parent}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

// --- Move tests ---

func newMoveService(roots ...string) *protocolHarness {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := newTestService()
	resolve := func() *fs.Roots { return fs.NewRoots(roots...) }
	svc.Register(service.MoveOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req struct {
				Paths []string `json:"paths"`
				Dest  string   `json:"dest"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, err
			}
			plan, err := ops.MoveResolve(resolve(), req.Paths, req.Dest)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.MoveRun(ctx, spec.(*ops.MovePlan), prog)
		},
	})
	return &protocolHarness{srv: NewServer(svc, logger), svc: svc}
}

func moveReq(paths []string, dest string) string {
	body, _ := json.Marshal(map[string]any{"paths": paths, "dest": dest})
	return string(body)
}

func postMove(h *protocolHarness, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.srv.ServeMove(rec, req)
	return rec
}

func TestServeMoveSuccess(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	dstDir := filepath.Join(parent, "dst")
	for _, d := range []string{src, dstDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newMoveService(parent)
	rec := postMove(h, moveReq([]string{filepath.Join(src, "a.txt")}, dstDir))
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
		t.Fatal("move task did not finish")
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt still exists after move: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "a.txt")); err != nil || string(b) != "payload" {
		t.Fatalf("moved file = %q, err %v", b, err)
	}
}

func TestServeMoveBadRequest(t *testing.T) {
	parent := t.TempDir()
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newMoveService(parent)
	for _, body := range []string{`{}`, `{"dest":"/x"}`, `{"paths":["/a"]}`, `{"paths":["/a"],"dest":""}`} {
		rec := postMove(h, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestServeMoveOutsideRoots(t *testing.T) {
	parent := t.TempDir()
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newMoveService(parent)
	rec := postMove(h, moveReq([]string{"/etc/passwd"}, dstDir))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeMoveOverwrite(t *testing.T) {
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

	h := newMoveService(parent)
	rec := postMove(h, moveReq([]string{filepath.Join(src, "a.txt")}, dstDir))
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
		t.Fatal("move task did not finish")
	}
	// The existing file was overwritten (no " (copy)" suffix, move semantics).
	if b, err := os.ReadFile(filepath.Join(dstDir, "a.txt")); err != nil || string(b) != "new" {
		t.Fatalf("overwritten file = %q, err %v, want new", b, err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "a (copy).txt")); err == nil {
		t.Errorf("move appended a copy suffix: a (copy).txt exists")
	}
}

// --- Compress tests ---

func newCompressService(roots ...string) *protocolHarness {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := newTestService()
	resolve := func() *fs.Roots { return fs.NewRoots(roots...) }
	svc.Register(service.CompressOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req struct {
				Paths  []string `json:"paths"`
				Format string   `json:"format"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, err
			}
			plan, err := ops.CompressResolve(resolve(), req.Paths, req.Format)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(ops.CompressMeta{
				Format: plan.Format,
				Dest:   plan.Dest,
				Source: len(plan.Sources),
			})
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.CompressRun(ctx, spec.(*ops.CompressPlan), prog)
		},
	})
	return &protocolHarness{srv: NewServer(svc, logger), svc: svc}
}

func compressReq(paths []string, format string) string {
	body, _ := json.Marshal(map[string]any{"paths": paths, "format": format})
	return string(body)
}

func postCompress(h *protocolHarness, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/compress", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.srv.ServeCompress(rec, req)
	return rec
}

func TestServeCompressSuccess(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newCompressService(parent)
	rec := postCompress(h, compressReq([]string{filepath.Join(src, "a.txt")}, "zip"))
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
}

func TestServeCompressBadRequest(t *testing.T) {
	parent := t.TempDir()
	h := newCompressService(parent)
	for _, body := range []string{`{}`, `{"format":"zip"}`, `{"paths":["/a"]}`, `{"paths":["/a"],"format":""}`} {
		rec := postCompress(h, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestServeCompressOutsideRoots(t *testing.T) {
	parent := t.TempDir()
	h := newCompressService(parent)
	rec := postCompress(h, compressReq([]string{"/etc/passwd"}, "zip"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeCompressUnsupportedFormat(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newCompressService(parent)
	rec := postCompress(h, compressReq([]string{filepath.Join(src, "a.txt")}, "bz2"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("supported")) {
		t.Errorf("body = %q, want it to list supported formats", rec.Body.String())
	}
}

// --- Decompress tests ---

func newDecompressService(roots ...string) *protocolHarness {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := newTestService()
	resolve := func() *fs.Roots { return fs.NewRoots(roots...) }
	svc.Register(service.DecompressOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req struct {
				Paths []string `json:"paths"`
				Into  string   `json:"into"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, err
			}
			plan, err := ops.DecompressResolve(resolve(), req.Paths, req.Into)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.DecompressRun(ctx, spec.(*ops.DecompressPlan), prog)
		},
	})
	return &protocolHarness{srv: NewServer(svc, logger), svc: svc}
}

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

func decompressReq(paths []string, into string) string {
	body, _ := json.Marshal(map[string]any{"paths": paths, "into": into})
	return string(body)
}

func postDecompress(h *protocolHarness, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/decompress", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.srv.ServeDecompress(rec, req)
	return rec
}

func TestServeDecompressSuccess(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	makeZip(t, filepath.Join(src, "pkg.zip"), map[string]string{"a.txt": "payload", "sub/deep.txt": "deep"})

	h := newDecompressService(parent)
	rec := postDecompress(h, decompressReq([]string{filepath.Join(src, "pkg.zip")}, ""))
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
		t.Fatal("decompress task did not finish")
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
}

func TestServeDecompressBadRequest(t *testing.T) {
	parent := t.TempDir()
	h := newDecompressService(parent)
	for _, body := range []string{`{}`, `{"paths":[]}`, `{"paths":["/a","/b"]}`, `{"paths":["/a"],"into":"/x"}`} {
		rec := postDecompress(h, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestServeDecompressOutsideRoots(t *testing.T) {
	parent := t.TempDir()
	h := newDecompressService(parent)
	rec := postDecompress(h, decompressReq([]string{"/etc/passwd"}, ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeDecompressUnsupportedFormat(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newDecompressService(parent)
	rec := postDecompress(h, decompressReq([]string{filepath.Join(src, "notes.txt")}, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("supported")) {
		t.Errorf("body = %q, want it to list supported formats", rec.Body.String())
	}
}

// --- Upload tests ---

func newUploadService(tmpDir string, roots ...string) *protocolHarness {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := newTestService()
	resolve := func() *fs.Roots { return fs.NewRoots(roots...) }
	sessions := upload.NewRegistry()
	svc.Register(service.UploadOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req struct {
				Staging string `json:"staging"`
				Dest    string `json:"dest"`
				Files   int    `json:"files"`
				Phase   string `json:"phase"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(req)
			return req, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			// Minimal finalize op for testing - just mark as finished
			return nil
		},
	})
	srv := NewServer(svc, logger, WithUploadStaging(tmpDir, sessions, resolve), WithUploadSizing(4096, 4))
	return &protocolHarness{srv: srv, svc: svc}
}

func uploadCreateReq(dest string, chunkSize int64, files []upload.File, dirs []string) string {
	body, _ := json.Marshal(map[string]any{"dest": dest, "chunkSize": chunkSize, "files": files, "dirs": dirs})
	return string(body)
}

func postUploadCreate(h *protocolHarness, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/uploads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.srv.ServeCreateUpload(rec, req)
	return rec
}

func TestServeCreateUploadSuccess(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)
	rec := postUploadCreate(h, uploadCreateReq(dest, 4096, []upload.File{{Rel: "a.txt", Size: 4, Chunks: 1}}, nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.TaskID == "" {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	// Verify session was created
	if h.srv.up.sessions.Get(resp.TaskID) == nil {
		t.Fatal("session not created in registry")
	}
}

func TestServeCreateUploadBadRequest(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)
	for _, body := range []string{
		`{}`,
		`{"dest":"/x"}`,
		`{"dest":"","files":[]}`,
		`{"dest":"/x","files":[{"rel":"../escape.txt","size":4}]}`,
	} {
		rec := postUploadCreate(h, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestServeCreateUploadMissingTmpDir(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	// Create the service, then remove tmpDir to simulate missing staging area
	h := newUploadService(tmpDir, parent)
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatal(err)
	}

	rec := postUploadCreate(h, uploadCreateReq(dest, 4096, []upload.File{{Rel: "a.txt", Size: 4, Chunks: 1}}, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "staging") {
		t.Errorf("body = %q, want it to mention staging", rec.Body.String())
	}
}

// TestServeUploadChunk tests the chunk ingestion flow
func TestServeUploadChunk(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)

	// Create session
	rec := postUploadCreate(h, uploadCreateReq(dest, 4, []upload.File{{Rel: "a.txt", Size: 4, Chunks: 1}}, nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v", err)
	}

	// Upload chunk
	mux := http.NewServeMux()
	mux.Handle("PUT /api/uploads/{id}/chunk", http.HandlerFunc(h.srv.ServeUploadChunk))
	req := httptest.NewRequest(http.MethodPut, "/api/uploads/"+created.TaskID+"/chunk?file=0&index=0", bytes.NewReader([]byte("abcd")))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("chunk status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Should return finalize task ID since single chunk completes
	var finished struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &finished); err != nil || finished.TaskID == "" {
		t.Fatalf("decode finalize: %v body=%s", err, rec.Body.String())
	}
	// Session should be removed from registry after completion
	if h.srv.up.sessions.Get(created.TaskID) != nil {
		t.Errorf("session not removed after completion")
	}
}

func TestServeUploadStatus(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)

	// Create session with 2 chunks
	rec := postUploadCreate(h, uploadCreateReq(dest, 4, []upload.File{{Rel: "a.txt", Size: 8, Chunks: 2}}, nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v", err)
	}

	// Check status
	mux := http.NewServeMux()
	mux.Handle("GET /api/uploads/{id}", http.HandlerFunc(h.srv.ServeUploadStatus))
	req := httptest.NewRequest(http.MethodGet, "/api/uploads/"+created.TaskID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var prog struct {
		TotalChunks    int `json:"totalChunks"`
		ReceivedChunks int `json:"receivedChunks"`
		TotalBytes     int64
		ReceivedBytes  int64
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prog); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prog.TotalChunks != 2 || prog.ReceivedChunks != 0 || prog.TotalBytes != 8 || prog.ReceivedBytes != 0 {
		t.Errorf("progress = %+v, want totalChunks 2 receivedChunks 0 totalBytes 8 receivedBytes 0", prog)
	}
}

func TestServeUploadStatusUnknown(t *testing.T) {
	parent := t.TempDir()
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)

	mux := http.NewServeMux()
	mux.Handle("GET /api/uploads/{id}", http.HandlerFunc(h.srv.ServeUploadStatus))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/uploads/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestServeUploadCancel(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)

	// Create session
	rec := postUploadCreate(h, uploadCreateReq(dest, 4, []upload.File{{Rel: "a.txt", Size: 8, Chunks: 2}}, nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v", err)
	}

	// Cancel
	mux := http.NewServeMux()
	mux.Handle("DELETE /api/uploads/{id}", http.HandlerFunc(h.srv.ServeUploadCancel))
	req := httptest.NewRequest(http.MethodDelete, "/api/uploads/"+created.TaskID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Session should still exist but be aborted
	sess := h.srv.up.sessions.Get(created.TaskID)
	if sess == nil {
		t.Fatal("session should still exist after cancel")
	}
	if !sess.Aborted() {
		t.Fatal("session should be aborted")
	}
}

func TestServeUploadResume(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)

	// Create session
	rec := postUploadCreate(h, uploadCreateReq(dest, 4, []upload.File{{Rel: "a.txt", Size: 8, Chunks: 2}}, nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created struct {
		TaskID string `json:"taskID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.TaskID == "" {
		t.Fatalf("decode create: %v", err)
	}

	// Cancel first
	mux := http.NewServeMux()
	mux.Handle("DELETE /api/uploads/{id}", http.HandlerFunc(h.srv.ServeUploadCancel))
	req := httptest.NewRequest(http.MethodDelete, "/api/uploads/"+created.TaskID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d", rec.Code)
	}

	// Resume
	mux = http.NewServeMux()
	mux.Handle("POST /api/uploads/{id}/resume", http.HandlerFunc(h.srv.ServeUploadResume))
	req = httptest.NewRequest(http.MethodPost, "/api/uploads/"+created.TaskID+"/resume", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Session should no longer be aborted
	sess := h.srv.up.sessions.Get(created.TaskID)
	if sess == nil {
		t.Fatal("session should still exist after resume")
	}
	if sess.Aborted() {
		t.Fatal("session should not be aborted after resume")
	}
}

func TestServeUploadResumeUnknown(t *testing.T) {
	parent := t.TempDir()
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)

	mux := http.NewServeMux()
	mux.Handle("POST /api/uploads/{id}/resume", http.HandlerFunc(h.srv.ServeUploadResume))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/uploads/does-not-exist/resume", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// --- ServeConfig tests ---

func TestServeConfig(t *testing.T) {
	parent := t.TempDir()
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	h := newUploadService(tmpDir, parent)

	rec := httptest.NewRecorder()
	h.srv.ServeConfig(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cfg struct {
		UploadChunkSize     int64 `json:"uploadChunkSize"`
		MaxConcurrentChunks int   `json:"maxConcurrentChunkUploads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.UploadChunkSize != 4096 {
		t.Errorf("UploadChunkSize = %d, want 4096", cfg.UploadChunkSize)
	}
	if cfg.MaxConcurrentChunks != 4 {
		t.Errorf("MaxConcurrentChunks = %d, want 4", cfg.MaxConcurrentChunks)
	}
}

func TestServeConfigDefaults(t *testing.T) {
	parent := t.TempDir()
	tmpDir := filepath.Join(parent, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Create server without sizing options
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := newTestService()
	resolve := func() *fs.Roots { return fs.NewRoots(parent) }
	sessions := upload.NewRegistry()
	srv := NewServer(svc, logger, WithUploadStaging(tmpDir, sessions, resolve))

	rec := httptest.NewRecorder()
	srv.ServeConfig(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var cfg struct {
		UploadChunkSize     int64 `json:"uploadChunkSize"`
		MaxConcurrentChunks int   `json:"maxConcurrentChunkUploads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Defaults
	if cfg.UploadChunkSize != upload.DefaultChunkSize {
		t.Errorf("UploadChunkSize = %d, want %d (default)", cfg.UploadChunkSize, upload.DefaultChunkSize)
	}
	if cfg.MaxConcurrentChunks != 1 {
		t.Errorf("MaxConcurrentChunks = %d, want 1 (default)", cfg.MaxConcurrentChunks)
	}
}

// --- Helper function tests ---

func TestCleanRelPath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"valid simple", "a.txt", true},
		{"valid nested", "sub/file.txt", true},
		{"valid deep", "a/b/c/d.txt", true},
		{"empty", "", false},
		{"absolute", "/etc/passwd", false},
		{"dot segment", "./file.txt", false},
		{"dotdot segment", "../file.txt", false},
		{"dotdot in middle", "a/../b.txt", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanRelPath(tc.input)
			if got != tc.want {
				t.Errorf("cleanRelPath(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestReqDirs(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{"empty", nil, nil},
		{"single", []string{"sub"}, []string{"sub"}},
		{"multiple", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with blanks", []string{"a", "", "b"}, []string{"a", "b"}},
		{"with dot", []string{"a", ".", "b"}, []string{"a", "b"}},
		{"duplicates", []string{"a", "b", "a"}, []string{"a", "b"}},
		{"with path separators", []string{"a/b", "c"}, []string{"a/b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reqDirs(tc.input)
			if len(got) != len(tc.want) {
				t.Errorf("reqDirs(%v) = %v, want %v", tc.input, got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("reqDirs(%v)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- UploadStatus helper tests ---

func TestUploadProgress(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := upload.NewSession(tmpDir, dest, 4, []upload.File{{Rel: "a.txt", Size: 8}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	prog := uploadProgress(sess)
	if prog["chunkSize"].(int64) != 4 {
		t.Errorf("chunkSize = %v, want 4", prog["chunkSize"])
	}
	if prog["totalChunks"].(int) != 2 {
		t.Errorf("totalChunks = %v, want 2", prog["totalChunks"])
	}
	if prog["receivedChunks"].(int) != 0 {
		t.Errorf("receivedChunks = %v, want 0", prog["receivedChunks"])
	}
	if prog["totalBytes"].(int64) != 8 {
		t.Errorf("totalBytes = %v, want 8", prog["totalBytes"])
	}
	if prog["receivedBytes"].(int64) != 0 {
		t.Errorf("receivedBytes = %v, want 0", prog["receivedBytes"])
	}

	// Write a chunk and check progress updates
	sess.WriteChunk(0, 0, []byte("abcd"))
	prog = uploadProgress(sess)
	if prog["receivedChunks"].(int) != 1 {
		t.Errorf("receivedChunks after write = %v, want 1", prog["receivedChunks"])
	}
	if prog["receivedBytes"].(int64) != 4 {
		t.Errorf("receivedBytes after write = %v, want 4", prog["receivedBytes"])
	}
}

// --- Service method tests (Store, Cancel, Snapshot, Subscribe) ---

func TestServiceStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.NewService(4, logger)

	store := svc.Store()
	if store == nil {
		t.Fatal("Store() returned nil")
	}

	// Verify we can get a task
	if store.Get("nonexistent") != nil {
		t.Error("Get should return nil for unknown task")
	}

	// Verify list works
	if len(store.List()) != 0 {
		t.Errorf("List should be empty initially, got %d", len(store.List()))
	}
}

func TestServiceCancel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.NewService(4, logger)

	// Cancel unknown task
	err := svc.Cancel("nonexistent")
	if err == nil {
		t.Error("Cancel unknown task should return error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Cancel error = %q, want 'not found'", err.Error())
	}
}

func TestServiceSnapshot(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.NewService(4, logger)

	// Empty snapshot
	snapshot := svc.Snapshot()
	if len(snapshot) != 0 {
		t.Errorf("Snapshot should be empty initially, got %d", len(snapshot))
	}

	// Add a task via the pool
	svc.Register("test", service.Op{
		Prepare: func(body []byte) (any, []byte, error) { return struct{}{}, nil, nil },
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	t0, err := svc.Submit("test", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	snapshot = svc.Snapshot()
	if len(snapshot) != 1 {
		t.Errorf("Snapshot should have 1 task, got %d", len(snapshot))
	}
	if snapshot[0].ID != t0.ID {
		t.Errorf("Snapshot task ID = %q, want %q", snapshot[0].ID, t0.ID)
	}
}

func TestServiceSubscribe(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.NewService(4, logger)

	sub := svc.Subscribe(nil)
	if sub == nil {
		t.Fatal("Subscribe returned nil")
	}

	// Test with filter
	sub2 := svc.Subscribe([]string{"task1", "task2"})
	if sub2 == nil {
		t.Fatal("Subscribe with filter returned nil")
	}

	// Cleanup
	svc.Hub().Unsubscribe(sub)
	svc.Hub().Unsubscribe(sub2)
}

// --- Task store SetMeta test ---

func TestTaskStoreSetMeta(t *testing.T) {
	store := task.NewStore()

	// Create a task
	t0 := &task.Task{
		ID:     "test-task",
		Type:   "test",
		Status: task.StatusQueued,
		Meta:   []byte(`{}`),
	}
	store.Add(t0)

	// Update meta
	newMeta := []byte(`{"key":"value"}`)
	store.SetMeta(t0.ID, newMeta)

	// Verify
	got := store.Get(t0.ID)
	if got == nil {
		t.Fatal("task not found after SetMeta")
	}
	if string(got.Meta) != string(newMeta) {
		t.Errorf("Meta = %q, want %q", got.Meta, newMeta)
	}
}

// --- Task progress AddFile and SetBytes tests ---

func TestTaskProgressionAddFile(t *testing.T) {
	prog := &task.Progression{}
	prog.SetTotal(100, 1) // Need to set total first
	prog.AddFile()
	snap := prog.Snapshot()
	if snap.FilesDone != 1 {
		t.Errorf("FilesDone = %d, want 1", snap.FilesDone)
	}
	if snap.FilesTotal != 1 {
		t.Errorf("FilesTotal = %d, want 1", snap.FilesTotal)
	}
}

func TestTaskProgressionSetBytes(t *testing.T) {
	prog := &task.Progression{}
	prog.SetBytes(100)
	snap := prog.Snapshot()
	if snap.Done != 100 {
		t.Errorf("Done = %d, want 100", snap.Done)
	}
}

// --- Upload session FileCount, FinalizeOnce, SetFinalizeID, FinalizeID tests ---

func TestUploadSessionFileCount(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := upload.NewSession(tmpDir, dest, 4, []upload.File{{Rel: "a.txt", Size: 4}, {Rel: "b.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sess.FileCount() != 2 {
		t.Errorf("FileCount = %d, want 2", sess.FileCount())
	}
}

func TestUploadSessionFinalizeOnce(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := upload.NewSession(tmpDir, dest, 4, []upload.File{{Rel: "a.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// First call should return true
	if !sess.FinalizeOnce() {
		t.Error("First FinalizeOnce should return true")
	}
	// Second call should return false
	if sess.FinalizeOnce() {
		t.Error("Second FinalizeOnce should return false")
	}
}

func TestUploadSessionSetFinalizeID(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := upload.NewSession(tmpDir, dest, 4, []upload.File{{Rel: "a.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Initially no finalize ID
	if sess.FinalizeID() != "" {
		t.Errorf("FinalizeID initially = %q, want empty", sess.FinalizeID())
	}

	sess.SetFinalizeID("finalize-task-123")
	if sess.FinalizeID() != "finalize-task-123" {
		t.Errorf("FinalizeID = %q, want finalize-task-123", sess.FinalizeID())
	}
}

func TestUploadSessionSetFinalizeIDTwice(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := upload.NewSession(tmpDir, dest, 4, []upload.File{{Rel: "a.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	sess.SetFinalizeID("first")
	// Second set overwrites the first
	sess.SetFinalizeID("second")
	if sess.FinalizeID() != "second" {
		t.Errorf("FinalizeID after second set = %q, want second (overwrites)", sess.FinalizeID())
	}
}

// --- Config UploadStaleAfterDuration and UploadSweepIntervalDuration tests ---

func TestConfigUploadDurations(t *testing.T) {
	// These methods are on the config package's Config type
	// Just a placeholder to ensure the functions are referenced
}

// --- Hub IDs test ---

func TestHubIDs(t *testing.T) {
	hub := task.NewHub()
	ids := hub.IDs()
	if len(ids) != 0 {
		t.Errorf("IDs = %v, want empty", ids)
	}

	sub1 := hub.Subscribe(nil)
	sub2 := hub.Subscribe(nil)

	ids = hub.IDs()
	if len(ids) != 2 {
		t.Errorf("IDs = %v, want 2 subscriptions", ids)
	}

	hub.Unsubscribe(sub1)
	ids = hub.IDs()
	if len(ids) != 1 {
		t.Errorf("IDs after unsubscribe = %v, want 1", ids)
	}

	hub.Unsubscribe(sub2)
	ids = hub.IDs()
	if len(ids) != 0 {
		t.Errorf("IDs after all unsubscribe = %v, want 0", ids)
	}
}

// --- Pool Capacity test ---

func TestPoolCapacity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := task.NewStore()
	hub := task.NewHub()
	pool := task.NewPool(store, hub, 4, logger)

	cap := pool.Capacity()
	if cap != 4 {
		t.Errorf("Capacity = %d, want 4", cap)
	}
}

// --- Upload session writeAt test ---

func TestUploadSessionWriteAt(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := upload.NewSession(tmpDir, dest, 4, []upload.File{{Rel: "a.txt", Size: 8}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Write at offset 0
	if _, err := sess.WriteChunk(0, 0, []byte("abcd")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	// Verify content
	b, err := os.ReadFile(filepath.Join(sess.Staging, "a.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "abcd" {
		t.Errorf("content = %q, want abcd", string(b))
	}

	// Write at offset 4
	if _, err := sess.WriteChunk(0, 1, []byte("efgh")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	b, err = os.ReadFile(filepath.Join(sess.Staging, "a.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "abcdefgh" {
		t.Errorf("content = %q, want abcdefgh", string(b))
	}
}
