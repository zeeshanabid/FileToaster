// Package protocol exposes the task engine over HTTP: the copy operation, a
// task snapshot list, per-task fetch, cancellation, and the SSE progress stream.
// Every handler is stateless over the service and wrapped in auth middleware by
// the caller, so file operations stay behind a valid session cookie.
package protocol

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
	"filetoaster/internal/upload"
)

// maxCopyRequestSize bounds the copy request body (paths + dest) far above any
// realistic selection without allowing unbounded reads.
const maxCopyRequestSize = 4 << 20 // 4 MiB

// maxCompressRequestSize bounds the compress request body (paths + format) far
// above any realistic selection without allowing unbounded reads.
const maxCompressRequestSize = 4 << 20 // 4 MiB

// maxUploadManifestSize bounds the upload-create request body (the file + dir
// manifest) far above any realistic selection without allowing unbounded reads.
const maxUploadManifestSize = 1 << 20 // 1 MiB

// Server holds the handlers for the task engine.
type Server struct {
	svc    *service.Service
	logger *slog.Logger

	up uploadDeps
}

// uploadDeps is the state the upload handlers need: tmpDir where sessions stage
// their chunks, the in-memory session registry, a roots factory to confine the
// upload destination to the allowed folders, and the upload sizing the client
// should use (advertised via ServeConfig). It is nil for callers that never
// exercise the upload handlers.
type uploadDeps struct {
	tmpDir   string
	sessions *upload.Registry
	roots    func() *fs.Roots
	// chunkSize is the fixed per-file chunk size the server advertises; 0 means
	// the built-in default. maxConcurrentChunks caps how many chunks a client
	// uploads at once (a global limit), to bound simultaneous requests.
	chunkSize       int64
	maxConcurrent   int
}

// ServerOptions configures the optional upload handlers on a Server.
type ServerOptions struct {
	tmpDir   string
	sessions *upload.Registry
	roots    func() *fs.Roots
	chunkSize       int64
	maxConcurrent   int
}

// ServerOption configures an optional part of a Server.
type ServerOption func(*ServerOptions)

// WithUploadStaging wires the upload handlers: tmpDir where sessions stage their
// chunks, the in-memory session registry, and a roots factory that confines the
// upload destination to the allowed folders.
func WithUploadStaging(tmpDir string, sessions *upload.Registry, roots func() *fs.Roots) ServerOption {
	return func(o *ServerOptions) {
		o.tmpDir = tmpDir
		o.sessions = sessions
		o.roots = roots
	}
}

// WithUploadSizing advertises the upload sizing the client should use: the fixed
// per-file chunk size in bytes and the maximum number of chunks to upload at once
// (a global limit across every file and session). ServeConfig returns these.
func WithUploadSizing(chunkSize int64, maxConcurrent int) ServerOption {
	return func(o *ServerOptions) {
		o.chunkSize = chunkSize
		o.maxConcurrent = maxConcurrent
	}
}

// NewServer builds the task HTTP handlers over svc. The upload handlers are
// optional and attached with WithUploadStaging; callers that never serve uploads
// need no options.
func NewServer(svc *service.Service, logger *slog.Logger, opts ...ServerOption) *Server {
	o := &ServerOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return &Server{svc: svc, logger: logger, up: uploadDeps{tmpDir: o.tmpDir, sessions: o.sessions, roots: o.roots, chunkSize: o.chunkSize, maxConcurrent: o.maxConcurrent}}
}

type copyRequest struct {
	Paths []string `json:"paths"`
	Dest  string   `json:"dest"`
}

// ServeCopy handles a copy request: it enqueues a copy task and returns its id.
// Destination collisions are resolved server-side by renaming (see ops.Resolve),
// so no overwrite prompt or 409 is produced.
func (s *Server) ServeCopy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCopyRequestSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	var req copyRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Paths) == 0 || strings.TrimSpace(req.Dest) == "" {
		writeError(w, http.StatusBadRequest, "the request requires 'paths' and a 'dest'")
		return
	}

	taskRec, err := s.svc.Submit(service.CopyOperation, body)
	if err != nil {
		status, message := copyStatus(err)
		writeError(w, status, message)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"taskID": taskRec.ID})
}

// copyStatus maps an operation failure to an HTTP status and user-facing message.
func copyStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the source or destination is outside the allowed folders"
	case errors.Is(err, task.ErrShuttingDown):
		return http.StatusServiceUnavailable, "the server is shutting down; please try again later"
	case errors.Is(err, service.ErrUnknownOperation):
		return http.StatusInternalServerError, "the requested operation is not available"
	default:
		return http.StatusBadRequest, "could not copy the selected items"
	}
}

type deleteRequest struct {
	Paths []string `json:"paths"`
}

// ServeDelete handles a delete request: it enqueues a delete task and returns its
// id. Delete is destructive and has no destination, so there is no conflict
// handling; the client must confirm before submitting.
func (s *Server) ServeDelete(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCopyRequestSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	var req deleteRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "the request requires one or more 'paths'")
		return
	}

	taskRec, err := s.svc.Submit(service.DeleteOperation, body)
	if err != nil {
		status, message := deleteStatus(err)
		writeError(w, status, message)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"taskID": taskRec.ID})
}

// deleteStatus maps an operation failure to an HTTP status and user-facing message.
func deleteStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the selected path is outside the allowed folders"
	case errors.Is(err, ops.DeleteErrRootPath):
		return http.StatusBadRequest, "the selected path is inside an allowed root and cannot be deleted"
	case errors.Is(err, task.ErrShuttingDown):
		return http.StatusServiceUnavailable, "the server is shutting down; please try again later"
	case errors.Is(err, service.ErrUnknownOperation):
		return http.StatusInternalServerError, "the requested operation is not available"
	default:
		return http.StatusBadRequest, "could not delete the selected items"
	}
}

type moveRequest struct {
	Paths []string `json:"paths"`
	Dest  string   `json:"dest"`
}

// ServeMove handles a move request: it enqueues a move task and returns its id.
// A move overwrites an existing same-named item at the destination, so there is
// no conflict handling and no 409 is produced; the client confirms before submit.
func (s *Server) ServeMove(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCopyRequestSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	var req moveRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Paths) == 0 || strings.TrimSpace(req.Dest) == "" {
		writeError(w, http.StatusBadRequest, "the request requires 'paths' and a 'dest'")
		return
	}

	taskRec, err := s.svc.Submit(service.MoveOperation, body)
	if err != nil {
		status, message := moveStatus(err)
		writeError(w, status, message)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"taskID": taskRec.ID})
}

// moveStatus maps an operation failure to an HTTP status and user-facing message.
func moveStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the source or destination is outside the allowed folders"
	case errors.Is(err, ops.ErrMoveEmptyPaths),
		errors.Is(err, ops.ErrMoveEmptyDest),
		errors.Is(err, ops.ErrSelfMove),
		errors.Is(err, ops.ErrOverlappingSources):
		return http.StatusBadRequest, "could not move the selected items"
	case errors.Is(err, task.ErrShuttingDown):
		return http.StatusServiceUnavailable, "the server is shutting down; please try again later"
	case errors.Is(err, service.ErrUnknownOperation):
		return http.StatusInternalServerError, "the requested operation is not available"
	default:
		return http.StatusBadRequest, "could not move the selected items"
	}
}

type compressRequest struct {
	Paths  []string `json:"paths"`
	Format string   `json:"format"`
}

// ServeCompress handles a compress request: it enqueues a compress task and
// returns its id. The archive is written in place by the server (renaming a
// colliding target), so no overwrite prompt or 409 is produced.
func (s *Server) ServeCompress(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCompressRequestSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	var req compressRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Paths) == 0 || strings.TrimSpace(req.Format) == "" {
		writeError(w, http.StatusBadRequest, "the request requires 'paths' and a 'format'")
		return
	}

	taskRec, err := s.svc.Submit(service.CompressOperation, body)
	if err != nil {
		status, message := compressStatus(err)
		writeError(w, status, message)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"taskID": taskRec.ID})
}

// compressStatus maps an operation failure to an HTTP status and user-facing
// message. An unsupported format surfaces the human "supported: ..." message
// rather than a panic.
func compressStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the selected path is outside the allowed folders"
	case errors.Is(err, ops.CompressArchiveFormatUnsupported):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, ops.ErrEmptyPaths),
		errors.Is(err, ops.CompressErrDifferentDirectories),
		errors.Is(err, ops.CompressErrOverlappingSelection):
		return http.StatusBadRequest, "could not compress the selected items"
	case errors.Is(err, task.ErrShuttingDown):
		return http.StatusServiceUnavailable, "the server is shutting down; please try again later"
	case errors.Is(err, service.ErrUnknownOperation):
		return http.StatusInternalServerError, "the requested operation is not available"
	default:
		return http.StatusBadRequest, "could not compress the selected items"
	}
}

type decompressRequest struct {
	Paths []string `json:"paths"`
	Into  string   `json:"into"`
}

// ServeDecompress handles a decompress request: it enqueues a decompress task
// and returns its id. The archive is read-only and extracted into a sibling of
// the archive, so no overwrite prompt or 409 is produced.
func (s *Server) ServeDecompress(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCompressRequestSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	var req decompressRequest
	if err := json.Unmarshal(body, &req); err != nil || len(req.Paths) != 1 {
		writeError(w, http.StatusBadRequest, "the request requires exactly one 'path'")
		return
	}

	taskRec, err := s.svc.Submit(service.DecompressOperation, body)
	if err != nil {
		status, message := decompressStatus(err)
		writeError(w, status, message)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"taskID": taskRec.ID})
}

// decompressStatus maps an operation failure to an HTTP status and user-facing
// message. An unsupported format surfaces the human "supported: ..." message
// rather than a panic.
func decompressStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the selected path is outside the allowed folders"
	case errors.Is(err, ops.DecompressUnsupportedFormat):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, ops.DecompressMultiplePaths),
		errors.Is(err, ops.DecompressArchiveTraversal),
		errors.Is(err, ops.ErrEmptyPaths):
		return http.StatusBadRequest, "could not decompress the selected archive"
	case errors.Is(err, task.ErrShuttingDown):
		return http.StatusServiceUnavailable, "the server is shutting down; please try again later"
	case errors.Is(err, service.ErrUnknownOperation):
		return http.StatusInternalServerError, "the requested operation is not available"
	default:
		return http.StatusBadRequest, "could not decompress the selected archive"
	}
}

// uploadCreateRequest is the body of POST /api/uploads: the destination folder,
// the (optional) chunk size, the file manifest, and the list of relative dir
// paths (including empty ones) to recreate on finalize.
type uploadCreateRequest struct {
	Dest      string        `json:"dest"`
	ChunkSize int64         `json:"chunkSize"`
	Files     []upload.File `json:"files"`
	Dirs      []string      `json:"dirs"`
}

// ServeCreateUpload starts an upload session. It confines the destination,
// creates the staging dir under tmp_dir, records the session, and returns its id.
func (s *Server) ServeCreateUpload(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUploadManifestSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	var req uploadCreateRequest
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Dest) == "" || len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "the request requires 'dest' and one or more 'files'")
		return
	}
	for _, f := range req.Files {
		if !cleanRelPath(f.Rel) {
			writeError(w, http.StatusBadRequest, "the upload manifest contains an invalid entry name")
			return
		}
	}
	for _, d := range reqDirs(req.Dirs) {
		if !cleanRelPath(d) {
			writeError(w, http.StatusBadRequest, "the upload manifest contains an invalid entry name")
			return
		}
	}

	// Confine the destination to the allowed roots before staging anything.
	dest, err := s.up.roots().Resolve(req.Dest)
	if err != nil {
		status, message := uploadStatus(err)
		writeError(w, status, message)
		return
	}

	// Refuse to stage when the configured tmp_dir is unavailable. Startup config
	// validation guarantees it exists; re-check here so an upload never silently
	// creates the directory.
	if info, err := os.Stat(s.up.tmpDir); err != nil || !info.IsDir() {
		writeError(w, http.StatusInternalServerError, "upload staging area is not available")
		return
	}
	chunkSize := req.ChunkSize
	if chunkSize <= 0 {
		chunkSize = upload.DefaultChunkSize
	}
	sess, err := upload.NewSession(s.up.tmpDir, dest, chunkSize, req.Files, reqDirs(req.Dirs))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the upload")
		return
	}
	s.up.sessions.Create(sess)

	writeJSON(w, http.StatusAccepted, map[string]any{"taskID": sess.ID})
}

// ServeUploadChunk ingests one chunk. The file and chunk indices come from the
// query string; the chunk bytes are the request body (size-limited). When the
// final chunk arrives the upload is finalized by enqueuing a tracked op, and the
// session (now owned by that op) is dropped so the id resolves to the finalize
// task from here on.
func (s *Server) ServeUploadChunk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.up.sessions.Get(id)
	if sess == nil {
		writeError(w, http.StatusNotFound, "upload session not found")
		return
	}

	fileIdx, chunkIdx, ok := parseChunkIndices(r.URL.Query())
	if !ok {
		writeError(w, http.StatusBadRequest, "the 'file' and 'index' query parameters are required integers")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, int64(sess.ChunkSize)+1024)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "chunk exceeds the expected size or could not be read")
		return
	}

	completed, err := sess.WriteChunk(fileIdx, chunkIdx, data)
	if err != nil {
		status, message := uploadStatus(err)
		writeError(w, status, message)
		return
	}
	if !completed {
		writeJSON(w, http.StatusAccepted, uploadProgress(sess))
		return
	}

	// This write completed the upload: enqueue the finalize op exactly once and
	// hand the staging cleanup to it.
	if !sess.FinalizeOnce() {
		// A concurrent completion already won the race; report the running finalize.
		writeJSON(w, http.StatusAccepted, uploadProgress(sess))
		return
	}
	body, _ := json.Marshal(map[string]any{
		"staging": sess.Staging,
		"dest":    sess.Dest,
		"files":   sess.FileCount(),
		"phase":   "finalizing",
	})
	taskRec, subErr := s.svc.Submit(service.UploadOperation, body)
	if subErr != nil {
		status, message := uploadStatus(subErr)
		writeError(w, status, message)
		return
	}
	sess.SetFinalizeID(taskRec.ID)
	s.up.sessions.Delete(id)

	writeJSON(w, http.StatusAccepted, map[string]any{"taskID": taskRec.ID})
}

// ServeUploadStatus returns the resumable status of a session (present chunks,
// byte counts) so a client can re-send only the missing chunks.
func (s *Server) ServeUploadStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.up.sessions.Get(id)
	if sess == nil {
		writeError(w, http.StatusNotFound, "upload session not found")
		return
	}
	writeJSON(w, http.StatusOK, uploadProgress(sess))
}

// ServeUploadCancel aborts ingestion but keeps the already-received chunks so the
// upload stays resumable. The staging dir and session remain in the registry for
// the grace period the age sweep allows; only then are they reclaimed.
func (s *Server) ServeUploadCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.up.sessions.Get(id)
	if sess == nil {
		writeError(w, http.StatusNotFound, "upload session not found")
		return
	}
	sess.SetAborted()
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": id})
}

// serverConfigResponse is the upload sizing the server advertises so the client
// slices files and limits concurrency consistently (see GET /api/config).
type serverConfigResponse struct {
	UploadChunkSize       int64 `json:"uploadChunkSize"`
	MaxConcurrentChunks   int   `json:"maxConcurrentChunkUploads"`
}

// ServeConfig returns the upload sizing the client should use: the fixed per-file
// chunk size (falling back to the built-in default when unset) and the maximum
// number of chunks to upload at once, across every file and session. It is
// intentionally public so the SPA can read it before any session cookie exists.
func (s *Server) ServeConfig(w http.ResponseWriter, r *http.Request) {
	chunkSize := s.up.chunkSize
	if chunkSize <= 0 {
		chunkSize = upload.DefaultChunkSize
	}
	maxConcurrent := s.up.maxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	writeJSON(w, http.StatusOK, serverConfigResponse{
		UploadChunkSize:   chunkSize,
		MaxConcurrentChunks: maxConcurrent,
	})
}

// uploadProgress renders a session's resumable snapshot.
func uploadProgress(sess *upload.Session) map[string]any {
	st := sess.Status()
	return map[string]any{
		"chunkSize":      st.ChunkSize,
		"totalChunks":    st.TotalChunks,
		"receivedChunks": st.ReceivedChunks,
		"totalBytes":     st.TotalBytes,
		"receivedBytes":  st.ReceivedBytes,
	}
}

// uploadStatus maps an upload failure to an HTTP status and user-facing message.
func uploadStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the destination is outside the allowed folders"
	case errors.Is(err, upload.ErrNoSession):
		return http.StatusNotFound, "upload session not found"
	case errors.Is(err, upload.ErrChunkOutOfRange):
		return http.StatusBadRequest, "the chunk index is out of range"
	default:
		return http.StatusBadRequest, "the upload chunk could not be stored"
	}
}

// parseChunkIndices reads the file and index query parameters as non-negative
// integers.
func parseChunkIndices(q url.Values) (file int, chunk int, ok bool) {
	file, ok = atoiq(q.Get("file"))
	if !ok {
		return 0, 0, false
	}
	chunk, ok = atoiq(q.Get("index"))
	if !ok {
		return 0, 0, false
	}
	return file, chunk, true
}

func atoiq(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// cleanRelPath reports whether name is a clean relative path: non-empty, not
// absolute, and free of "." or ".." segments and path separators that escape.
func cleanRelPath(name string) bool {
	if name == "" {
		return false
	}
	if filepath.IsAbs(name) {
		return false
	}
	parts := strings.Split(name, string(filepath.Separator))
	for _, p := range parts {
		if p == "." || p == ".." {
			return false
		}
	}
	return true
}

// reqDirs returns the manifest dirs with blanks and duplicates dropped.
func reqDirs(dirs []string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		d = filepath.Clean(d)
		if d == "" || d == "." {
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// ServeTasksList returns the snapshot of every tracked task, oldest first.
func (s *Server) ServeTasksList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.svc.Snapshot()})
}

// ServeTask returns a single task by id.
func (s *Server) ServeTask(w http.ResponseWriter, r *http.Request) {
	id := path.Base(r.URL.Path)
	t := s.svc.Get(id)
	if t == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// ServeCancel cancels a queued or running task.
func (s *Server) ServeCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.Cancel(id); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusBadRequest, "could not cancel the task")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": id})
}

// ServeEvents streams task deltas to a client as Server-Sent Events. The optional
// `ids` query parameter (comma-separated) filters the stream to those tasks; with
// no filter every task is streamed. The connection is kept alive with periodic
// comments and closed when the client disconnects.
func (s *Server) ServeEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var ids []string
	if raw := r.URL.Query().Get("ids"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				ids = append(ids, part)
			}
		}
	}

	sub := s.svc.Subscribe(ids)
	defer s.svc.Hub().Unsubscribe(sub)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.Out():
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, ev); err != nil {
				return
			}
			// Flush after every event so the client sees progress in almost
			// real time instead of waiting for the next keepalive tick.
			flusher.Flush()
		case <-ticker.C:
			// A comment is ignored by SSE clients but keeps proxies/hop-alive alive.
			_, _ = w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, flusher http.Flusher, t *task.Task) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "event: task\ndata: "); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, _ = io.WriteString(w, "\n\n")
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
