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
	"path"
	"strings"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
)

// maxCopyRequestSize bounds the copy request body (paths + dest) far above any
// realistic selection without allowing unbounded reads.
const maxCopyRequestSize = 4 << 20 // 4 MiB

// maxCompressRequestSize bounds the compress request body (paths + format) far
// above any realistic selection without allowing unbounded reads.
const maxCompressRequestSize = 4 << 20 // 4 MiB

// Server holds the handlers for the task engine.
type Server struct {
	svc    *service.Service
	logger *slog.Logger
}

// NewServer builds the task HTTP handlers over svc.
func NewServer(svc *service.Service, logger *slog.Logger) *Server {
	return &Server{svc: svc, logger: logger}
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
