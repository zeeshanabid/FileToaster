package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"filetoaster/internal/config"
	"filetoaster/internal/fs"
)

// fsConfig is the subset of the runtime config the fs handlers need. It is
// decoupled from *config.Config so the handlers are easy to unit test.
type fsConfig struct {
	AllowedPaths []config.AllowedPath
}

func newFSConfig(cfg *config.Config) fsConfig {
	return fsConfig{AllowedPaths: cfg.AllowedPaths}
}

// serveRoots writes the configured allowed paths as name/path pairs the UI uses
// to build folder icons. Path is the already-resolved absolute directory.
func serveRoots(w http.ResponseWriter, r *http.Request, cfg fsConfig) {
	type rootJSON struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	paths := make([]string, 0, len(cfg.AllowedPaths))
	out := make([]rootJSON, 0, len(cfg.AllowedPaths))
	for _, ap := range cfg.AllowedPaths {
		if ap.Path == "" {
			continue
		}
		resolved := resolveRootPath(ap.Path)
		paths = append(paths, resolved)
		out = append(out, rootJSON{Name: ap.Name, Path: resolved})
	}
	// Sanity-check containment so a misconfigured root is reported, not served.
	fs.NewRoots(paths...)
	writeJSON(w, http.StatusOK, map[string]any{"allowed_paths": out})
}

// resolveRootPath canonicalizes a configured allowed root the same way requests
// are canonicalized (EvalSymlinks + Clean). A symlinked root such as /bin is
// served at its resolved target (e.g. /usr/bin) so the UI opens the directory
// actually referenced and containment comparisons stay consistent. A path that
// cannot be resolved falls back to its cleaned form so a stale root is still
// shown.
func resolveRootPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

// fsListRequest is the JSON body of a listing POST. The path travels in the
// request body, not the URL, so it is never truncated by URL length limits and
// never lands in access logs or proxy headers.
type fsListRequest struct {
	Path string `json:"path"`
}

// maxFsRequestSize caps the listing request body far above any real filesystem
// path, bounding request size without rejecting legitimate paths.
const maxFsRequestSize = 1 << 20 // 1 MiB

// serveFsList reads the `path` request field from the JSON body, confines it to
// the allowed roots, and returns the directory's entries plus the total size.
// Containment failures are reported with a message that does not leak internal
// paths.
func serveFsList(w http.ResponseWriter, r *http.Request, cfg fsConfig) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFsRequestSize)
	var req fsListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the 'path' request field is required")
		return
	}
	rawPath := req.Path
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "the 'path' request field is required")
		return
	}

	roots := fs.NewRoots(pathsOf(cfg)...)
	entries, err := fs.List(roots, rawPath)
	if err != nil {
		status, message := fsStatus(err)
		writeError(w, status, message)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":    rawPath,
		"entries": entries,
	})
}

// pathsOf returns the resolved absolute paths of the config's allowed entries.
func pathsOf(cfg fsConfig) []string {
	paths := make([]string, 0, len(cfg.AllowedPaths))
	for _, ap := range cfg.AllowedPaths {
		if ap.Path != "" {
			paths = append(paths, ap.Path)
		}
	}
	return paths
}

// fsStatus maps an fs resolution error to an HTTP status and user-facing
// message.
func fsStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the requested path is outside the allowed folders"
	default:
		return http.StatusBadRequest, "could not read the requested path"
	}
}

// createStatus maps a Mkdir failure to a status and message, reusing the
// containment rule but with create-oriented wording for the other cases.
func createStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fs.ErrOutsideRoots):
		return http.StatusForbidden, "the requested location is outside the allowed folders"
	case errors.Is(err, fs.ErrExists):
		return http.StatusConflict, "a folder with that name already exists"
	case errors.Is(err, fs.ErrEmptyName):
		return http.StatusBadRequest, "a directory name is required"
	case errors.Is(err, fs.ErrInvalidName):
		return http.StatusBadRequest, "the directory name is not valid"
	default:
		return http.StatusBadRequest, "could not create the directory"
	}
}

type fsMkdirRequest struct {
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

// serveFsMkdir creates a directory named `name` inside `parent`, confined to the
// allowed roots. It returns 201 with the created path, or a typed error status.
func serveFsMkdir(w http.ResponseWriter, r *http.Request, cfg fsConfig) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFsRequestSize)
	var req fsMkdirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the 'parent' and 'name' request fields are required")
		return
	}
	if req.Parent == "" || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "the 'parent' and 'name' request fields are required")
		return
	}

	roots := fs.NewRoots(pathsOf(cfg)...)
	if err := fs.Mkdir(roots, req.Parent, req.Name); err != nil {
		status, message := createStatus(err)
		writeError(w, status, message)
		return
	}

	created := filepath.Clean(filepath.Join(req.Parent, strings.TrimSpace(req.Name)))
	writeJSON(w, http.StatusCreated, map[string]any{"path": created})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
