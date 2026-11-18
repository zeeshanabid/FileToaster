package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"filetoaster/internal/download"
	"filetoaster/internal/fs"
)

// downloadBufferSize is the copy buffer for the streaming download handler. It
// matches the copy/decompress buffer so a single-file download is throughput-bound
// rather than bounded by small I/O rounds. A single pool serves every download so
// the allocation is amortized across requests instead of 8 MiB per download.
const downloadBufferSize = 8 << 20

// maxDownloadRequestSize bounds the download request body (a single path) far
// above any realistic filesystem path without allowing unbounded reads.
const maxDownloadRequestSize = 1 << 20

// downloadStaleAfter is how long an issued-but-unfetched token is tolerated before
// the background sweep drops it. downloadSweepInterval is how often that sweep
// runs. Both are independent of the upload sweep timing.
const (
	downloadStaleAfter    = 10 * time.Minute
	downloadSweepInterval = 1 * time.Minute
)

// downloadConfig is the runtime state the download handlers need: a roots factory
// that confines every source to the allowed folders, and the token registry that
// maps a POSTed path to its one-time download token.
type downloadConfig struct {
	roots    func() *fs.Roots
	registry *download.Registry
}

func newDownloadConfig(roots func() *fs.Roots, registry *download.Registry) downloadConfig {
	return downloadConfig{roots: roots, registry: registry}
}

// copyBufferPool is the shared copy buffer the download handler streams through.
var copyBufferPool = sync.Pool{New: func() any { return make([]byte, downloadBufferSize) }}

// downloadRequest is the JSON body of a download POST: the selected source path(s).
type downloadRequest struct {
	Paths []string `json:"paths"`
}

// serveDownloads handles POST /api/downloads. It confines the single selected
// path to the allowed roots, rejects directories and multi-path selections, issues
// a one-time token for the resolved file, and returns it. The client then GETs
// /downloads/{token}; the source path never leaves the POST body, so the
// streaming URL can only ever carry the unguessable token — no client path can
// reach the endpoint, and traversal is impossible.
func serveDownloads(w http.ResponseWriter, r *http.Request, cfg downloadConfig) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDownloadRequestSize)
	var req downloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the 'paths' request field is required")
		return
	}
	req.Paths = uniqueTrimPaths(req.Paths)
	if len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "select a single file to download")
		return
	}
	if len(req.Paths) > 1 {
		writeError(w, http.StatusBadRequest, "download operates on a single file")
		return
	}

	path, err := cfg.roots().Resolve(req.Paths[0])
	if err != nil {
		writeError(w, downloadStatus(err), downloadMessage(err))
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		// A missing file fails resolve; a directory reaches here — report a
		// clear download-scope error rather than attempting to stream a folder.
		writeError(w, http.StatusBadRequest, "download operates on a single file, not a folder")
		return
	}

	token := cfg.registry.Issue(path)
	writeJSON(w, http.StatusAccepted, map[string]any{"token": token})
}

// serveDownloadToken handles GET /downloads/{token}. It consumes the token (removing
// the mapping so it cannot be reused), streams the bound file to the client with a
// large buffered copy, and sets the download headers (Content-Type, Content-Length,
// a Content-Disposition attachment, and no-cache). An unknown or already-consumed
// token is a 404; a file that vanished after token issuance is a 500 — the token is
// already gone in both cases.
func serveDownloadToken(w http.ResponseWriter, r *http.Request, cfg downloadConfig) {
	token := r.PathValue("token")
	path, ok := cfg.registry.Take(token)
	if !ok {
		writeError(w, http.StatusNotFound, "this download link is no longer valid")
		return
	}

	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the file is no longer available")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the file is no longer available")
		return
	}

	w.Header().Set("Content-Type", contentType(path))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename(path)+`"`)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)

	buf := copyBufferPool.Get().([]byte)
	defer copyBufferPool.Put(buf)
	_, _ = io.CopyBuffer(w, f, buf)
}

// downloadStatus maps an fs resolution error to an HTTP status.
func downloadStatus(err error) int {
	if errors.Is(err, fs.ErrOutsideRoots) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

// downloadMessage maps an fs resolution error to a user-facing message.
func downloadMessage(err error) string {
	if errors.Is(err, fs.ErrOutsideRoots) {
		return "the selected file is outside the allowed folders"
	}
	return "could not read the selected file"
}

// contentType returns the MIME type for path's extension, or octet-stream when
// unknown. It is a cheap extension lookup, not a content probe, so it stays
// efficient regardless of file size.
func contentType(path string) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// filename returns the base name of path for the Content-Disposition header.
func filename(path string) string {
	base := filepath.Base(path)
	if base == "." || base == "/" {
		return "download"
	}
	return base
}

// uniqueTrimPaths drops blanks and duplicates from a path slice, preserving first
// occurrence order, so the single-file check is not fooled by an empty or repeated
// path.
func uniqueTrimPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// downloadCleanup owns the background token age sweep. The sweep runs on a ticker
// and is stopped (via its context) on shutdown; it occupies no task-pool slot and
// is never a tracked task.
type downloadCleanup struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startDownloadCleanup removes tokens older than staleAfter on a ticker ticking
// every interval. The returned value stops the sweep and reports its completion.
func startDownloadCleanup(registry *download.Registry, staleAfter, interval time.Duration) downloadCleanup {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				registry.Sweep(staleAfter)
			}
		}
	}()
	return downloadCleanup{cancel: cancel, done: done}
}
