package fs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Entry is one directory entry returned by List.
type Entry struct {
	// Path is the absolute path of the entry within the allowed roots.
	Path string `json:"path"`
	// Name is the base name of the entry.
	Name string `json:"name"`
	// Type is "file" or "dir".
	Type string `json:"type"`
	// Size is the size in bytes (0 for directories on most systems).
	Size int64 `json:"size"`
	// Created is the entry's creation (birth/change) time; it falls back to the
	// modified time on platforms without a birth time.
	Created time.Time `json:"created"`
	// Modified is the entry's last-modification time.
	Modified time.Time `json:"modified"`
}

// List returns the entries directly inside dir, confined to the allowed roots.
// Each entry reports its full path, name, type, size, and created/modified times.
func List(roots *Roots, dir string) ([]Entry, error) {
	target, err := roots.Resolve(dir)
	if err != nil {
		return nil, fsError(err)
	}
	return listDir(target)
}

// listDir reads and stat-s every child of dir. Children that cannot be stat'd
// are skipped rather than aborting the whole listing.
func listDir(dir string) ([]Entry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dir, de.Name())
		kind := "file"
		if de.IsDir() {
			kind = "dir"
		}
		created, modified := entryTimes(full, info)
		out = append(out, Entry{
			Path:     full,
			Name:     de.Name(),
			Type:     kind,
			Size:     info.Size(),
			Created:  created,
			Modified: modified,
		})
	}
	return out, nil
}

// TotalSize returns the sum of the sizes of all files under dir (recursively),
// confined to the allowed roots. Symlinked directories are not followed, which
// keeps the walk finite even on cyclic trees.
func TotalSize(roots *Roots, dir string) (int64, error) {
	target, err := roots.Resolve(dir)
	if err != nil {
		return 0, fsError(err)
	}
	var total int64
	err = filepath.WalkDir(target, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !d.IsDir() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// entryTimes derives the created and modified times for a path. On systems with
// a Stat_t (e.g. Linux) the created time uses the change time, since Go does not
// expose a birth time; both fall back to the modification time otherwise.
func entryTimes(full string, info os.FileInfo) (created, modified time.Time) {
	modified = info.ModTime()
	created = modified
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		created = time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
	}
	return created, modified
}

// HumanSize formats a byte count using 1024-based units, e.g. 1536 -> "1.5KB",
// 1572864 -> "1.5MB".
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + "B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	if exp < 0 || exp >= len(units) {
		return "0B"
	}
	v := strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64)
	v = strings.TrimSuffix(v, ".0")
	return v + units[exp]
}

// fsError converts a containment resolution error into a UI-facing message
// without leaking internal details. The original sentinel is preserved so
// callers can still detect it with errors.Is.
func fsError(err error) error {
	switch {
	case errors.Is(err, ErrOutsideRoots):
		return fmt.Errorf("%w: the requested path is outside the allowed folders", ErrOutsideRoots)
	case errors.Is(err, filepath.ErrBadPattern):
		return fmt.Errorf("%w: the requested path is not valid", filepath.ErrBadPattern)
	default:
		return fmt.Errorf("%w: could not read the requested path", err)
	}
}
