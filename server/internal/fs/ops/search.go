package ops

import (
	"context"
	"encoding/json"
	"errors"
	iofs "io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// SearchResolve sentinels. They surface as 400s (validation) and are named with
// a Search prefix because the ops package is shared with the other operations.
var (
	// SearchErrEmptyRoot is returned when no search root path is supplied.
	SearchErrEmptyRoot = errors.New("a search folder is required")
	// SearchErrEmptyCriteria is returned when there is neither a query nor any
	// filter: such a search would just re-list the folder.
	SearchErrEmptyCriteria = errors.New("enter a search term or set at least one filter")
	// SearchErrNotDirectory is returned when the search root resolves to a file.
	SearchErrNotDirectory = errors.New("the search folder is not a directory")
	// SearchErrInvalidSizeRange is returned when a minimum size exceeds the max.
	SearchErrInvalidSizeRange = errors.New("the minimum size cannot exceed the maximum size")
	// SearchErrInvalidDate is returned when a date filter is not a valid time.
	SearchErrInvalidDate = errors.New("a date filter is not a valid timestamp")
)

// SearchRequest is the client-supplied search specification. Dates are optional
// RFC-3339 (or "YYYY-MM-DD[THH:MM[:SS]]") timestamps; empty means unbounded.
// Sizes are in bytes (the client converts its unit selector before sending).
type SearchRequest struct {
	RootPath       string   `json:"rootPath"`
	Query          string   `json:"query"`
	MimeTypes      []string `json:"mimeTypes"`
	Extensions     []string `json:"extensions"`
	MinSize        int64    `json:"minSize"`
	MaxSize        int64    `json:"maxSize"`
	CreatedAfter   string   `json:"createdAfter"`
	CreatedBefore  string   `json:"createdBefore"`
	ModifiedAfter  string   `json:"modifiedAfter"`
	ModifiedBefore string   `json:"modifiedBefore"`
}

// SearchPlan is a validated, resolved search ready to execute. It is serialized
// into the task's meta so the client can label and describe the running search.
type SearchPlan struct {
	RootPath       string    `json:"rootPath"`
	Query          string    `json:"query"`
	MimeTypes      []string  `json:"mimeTypes,omitempty"`
	Extensions     []string  `json:"extensions,omitempty"`
	MinSize        int64     `json:"minSize,omitempty"`
	MaxSize        int64     `json:"maxSize,omitempty"`
	CreatedAfter   time.Time `json:"createdAfter,omitzero"`
	CreatedBefore  time.Time `json:"createdBefore,omitzero"`
	ModifiedAfter  time.Time `json:"modifiedAfter,omitzero"`
	ModifiedBefore time.Time `json:"modifiedBefore,omitzero"`
}

// SearchResult is one match streamed back to the client.
type SearchResult struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Type     string    `json:"type"` // "file" or "dir"
	Size     int64     `json:"size"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
	MimeType string    `json:"mimeType,omitempty"`
}

// SearchResolve validates the request, resolves the root against the allowed
// roots, and normalizes the filters into a ready-to-execute plan. The root is
// canonicalized and confined to the roots (a traversal/symlink/absolute escape
// returns fs.ErrOutsideRoots) and must resolve to a directory.
func SearchResolve(roots *fs.Roots, req SearchRequest) (*SearchPlan, error) {
	rawRoot := strings.TrimSpace(req.RootPath)
	if rawRoot == "" {
		return nil, SearchErrEmptyRoot
	}
	query := strings.TrimSpace(req.Query)
	mimeTypes := normalizeList(req.MimeTypes)
	extensions := normalizeExtensions(req.Extensions)
	if query == "" && len(mimeTypes) == 0 && len(extensions) == 0 && req.MinSize <= 0 && req.MaxSize <= 0 &&
		strings.TrimSpace(req.CreatedAfter) == "" && strings.TrimSpace(req.CreatedBefore) == "" &&
		strings.TrimSpace(req.ModifiedAfter) == "" && strings.TrimSpace(req.ModifiedBefore) == "" {
		return nil, SearchErrEmptyCriteria
	}

	root, err := roots.Resolve(rawRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, SearchErrNotDirectory
	}

	plan := &SearchPlan{
		RootPath:   root,
		Query:      query,
		MimeTypes:  mimeTypes,
		Extensions: extensions,
		MinSize:    maxInt64(req.MinSize, 0),
		MaxSize:    maxInt64(req.MaxSize, 0),
	}
	if plan.MaxSize > 0 && plan.MinSize > plan.MaxSize {
		return nil, SearchErrInvalidSizeRange
	}
	if plan.CreatedAfter, err = parseOptionalTime(req.CreatedAfter); err != nil {
		return nil, err
	}
	if plan.CreatedBefore, err = parseOptionalTime(req.CreatedBefore); err != nil {
		return nil, err
	}
	if plan.ModifiedAfter, err = parseOptionalTime(req.ModifiedAfter); err != nil {
		return nil, err
	}
	if plan.ModifiedBefore, err = parseOptionalTime(req.ModifiedBefore); err != nil {
		return nil, err
	}
	return plan, nil
}

// SearchRun walks the plan's root recursively (without following symlinked
// directories), applies every filter with AND semantics, and streams each match
// to the client as a "search-result" event. It reports progress as files scanned
// against an upfront file count so the task panel shows a real 0..100 bar. A
// "search-complete" event carrying the scanned/matches totals is emitted when the
// walk finishes. Unreadable entries are skipped (like listing), and a cancelled
// context aborts the walk and is returned as-is (the task is then cancelled).
func SearchRun(ctx context.Context, plan *SearchPlan, prog *task.Progression) error {
	// A best-effort upfront count of the files under the root gives the panel a
	// determinate total. The walk below counts files as it goes; directories are
	// matched too but are not part of the byte/file progress. The count honours
	// cancellation so a cancelled search stops promptly instead of walking the
	// whole tree first.
	prog.SetTotal(0, countFiles(ctx, plan.RootPath))
	if err := ctx.Err(); err != nil {
		return err
	}

	scanned := 0
	matches := 0
	emit := func(result SearchResult) {
		if data, err := json.Marshal(result); err == nil {
			prog.ReportEvent("search-result", data)
		}
	}

	walkErr := filepath.WalkDir(plan.RootPath, func(path string, d iofs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// An unreadable directory is skipped whole; an unreadable file is
			// skipped individually. Neither aborts the search.
			if d != nil && d.IsDir() {
				return iofs.SkipDir
			}
			return nil
		}
		if path == plan.RootPath {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if d.IsDir() {
			if result, ok := plan.matchDir(path, d.Name(), info); ok {
				matches++
				emit(result)
			}
			return nil
		}
		// A regular file: advance the progress bar even when it does not match.
		scanned++
		if result, ok := plan.matchFile(path, d.Name(), info); ok {
			matches++
			emit(result)
		}
		prog.AddFile()
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	if data, err := json.Marshal(map[string]int{"scanned": scanned, "matches": matches}); err == nil {
		prog.ReportEvent("search-complete", data)
	}
	return nil
}

// hasFileFilters reports whether any filter restricts results to files (a file
// type, extension, or size filter). When one is set, directories never match,
// because they have no type/size of their own.
func (p *SearchPlan) hasFileFilters() bool {
	return len(p.MimeTypes) > 0 || len(p.Extensions) > 0 || p.MinSize > 0 || p.MaxSize > 0
}

// matchDir evaluates a directory entry. Directories are excluded whenever a
// file-only filter is active; otherwise only the name query and the date
// filters apply.
func (p *SearchPlan) matchDir(path, name string, info iofs.FileInfo) (SearchResult, bool) {
	if p.hasFileFilters() {
		return SearchResult{}, false
	}
	return p.match(path, name, "dir", info, "")
}

// matchFile evaluates a file entry against the extension, MIME type, size, date
// and name filters.
func (p *SearchPlan) matchFile(path, name string, info iofs.FileInfo) (SearchResult, bool) {
	if len(p.Extensions) > 0 && !hasExtension(name, p.Extensions) {
		return SearchResult{}, false
	}
	mimeType := detectMimeType(name)
	if len(p.MimeTypes) > 0 && !matchesMime(mimeType, p.MimeTypes) {
		return SearchResult{}, false
	}
	size := info.Size()
	if p.MinSize > 0 && size < p.MinSize {
		return SearchResult{}, false
	}
	if p.MaxSize > 0 && size > p.MaxSize {
		return SearchResult{}, false
	}
	return p.match(path, name, "file", info, mimeType)
}

// match applies the name query and the created/modified date filters, which are
// common to files and directories, and builds the result.
func (p *SearchPlan) match(path, name, kind string, info iofs.FileInfo, mimeType string) (SearchResult, bool) {
	if p.Query != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(p.Query)) {
		return SearchResult{}, false
	}
	created, modified := searchTimes(path, info)
	if !p.CreatedAfter.IsZero() && created.Before(p.CreatedAfter) {
		return SearchResult{}, false
	}
	if !p.CreatedBefore.IsZero() && created.After(p.CreatedBefore) {
		return SearchResult{}, false
	}
	if !p.ModifiedAfter.IsZero() && modified.Before(p.ModifiedAfter) {
		return SearchResult{}, false
	}
	if !p.ModifiedBefore.IsZero() && modified.After(p.ModifiedBefore) {
		return SearchResult{}, false
	}
	return SearchResult{
		Path:     path,
		Name:     name,
		Type:     kind,
		Size:     info.Size(),
		Created:  created,
		Modified: modified,
		MimeType: mimeType,
	}, true
}

// countFiles counts the regular files under root, best-effort: unreadable
// directories and files are skipped rather than failing the count. The walk
// stops early when ctx is cancelled so a cancelled search does not count the
// entire tree before returning.
func countFiles(ctx context.Context, root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, d iofs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return iofs.SkipDir
			}
			return nil
		}
		if path != root && !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

// searchTimes derives the created/modified times for a path. On systems with a
// Stat_t (Linux) the created time uses the change time (Go exposes no birth
// time); both fall back to the modification time otherwise. It mirrors the
// listing endpoint's entryTimes so search times match the listing times.
func searchTimes(path string, info iofs.FileInfo) (created, modified time.Time) {
	modified = info.ModTime()
	created = modified
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		created = time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
	}
	return created, modified
}

// parseOptionalTime parses an optional timestamp. An empty string means "no
// bound" and returns the zero time. A non-empty but unparseable value returns
// SearchErrInvalidDate.
func parseOptionalTime(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, SearchErrInvalidDate
}

// normalizeList lower-cases, trims, and de-duplicates a list of MIME types.
func normalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// normalizeExtensions lower-cases, strips a leading dot, trims, and de-dupes a
// list of file extensions (e.g. ".TXT" and "txt" both become "txt").
func normalizeExtensions(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		v = strings.TrimPrefix(v, ".")
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// hasExtension reports whether name's extension (without the dot, lower-cased)
// is one of exts. A file whose name has no extension never matches.
func hasExtension(name string, exts []string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return false
	}
	for _, want := range exts {
		if ext == want {
			return true
		}
	}
	return false
}

// matchesMime reports whether the detected MIME type of a file matches any of
// the requested types. A request ending in "/*" is a prefix match (e.g.
// "image/*"); otherwise the types must be equal. Callers only invoke this with a
// non-empty want list.
func matchesMime(got string, want []string) bool {
	if got == "" {
		return false
	}
	got = strings.ToLower(got)
	for _, w := range want {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		if strings.HasSuffix(w, "/*") {
			if strings.HasPrefix(got, strings.TrimSuffix(w, "*")) {
				return true
			}
			continue
		}
		if got == w {
			return true
		}
	}
	return false
}

// commonMimeTypes maps the file extensions the search UI offers to their MIME
// type. It is deterministic (unlike mime.TypeByExtension, which can consult the
// host's /etc/mime.types), with that lookup as a fallback for anything else.
var commonMimeTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".ico":  "image/x-icon",
	".heic": "image/heic",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".mkv":  "video/x-matroska",
	".webm": "video/webm",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".zip":  "application/zip",
	".tar":  "application/x-tar",
	".gz":   "application/gzip",
	".tgz":  "application/gzip",
	".7z":   "application/x-7z-compressed",
	".rar":  "application/vnd.rar",
	".txt":  "text/plain",
	".md":   "text/markdown",
	".csv":  "text/csv",
	".html": "text/html",
	".htm":  "text/html",
	".css":  "text/css",
	".js":   "text/javascript",
	".mjs":  "text/javascript",
	".json": "application/json",
	".xml":  "application/xml",
	".yaml": "application/yaml",
	".yml":  "application/yaml",
	".go":   "text/x-go",
	".ts":   "text/x-typescript",
	".py":   "text/x-python",
	".rs":   "text/x-rust",
	".java": "text/x-java",
	".sh":   "application/x-sh",
}

// detectMimeType returns the MIME type for a file name's extension, using the
// built-in common map first and falling back to the stdlib lookup. The type is
// returned without any parameters (e.g. "text/plain", not
// "text/plain; charset=utf-8"); an unknown extension yields an empty string.
func detectMimeType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return ""
	}
	if mt, ok := commonMimeTypes[ext]; ok {
		return mt
	}
	if mt := mime.TypeByExtension(ext); mt != "" {
		if i := strings.IndexByte(mt, ';'); i >= 0 {
			mt = mt[:i]
		}
		return strings.ToLower(strings.TrimSpace(mt))
	}
	return ""
}

func maxInt64(v, min int64) int64 {
	if v < min {
		return min
	}
	return v
}
