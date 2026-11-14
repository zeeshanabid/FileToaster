package fs

import (
  "errors"
  "fmt"
  "os"
  "path/filepath"
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
