package fs

import (
  "errors"
  "fmt"
  "os"
  "path/filepath"
  "strings"
)

// Rename errors. The sentinels let the API layer map a failure to a precise HTTP
// status and a UI-facing message without leaking internal details.
var (
  RenameExists      = errors.New("a file or folder with that name already exists")
  RenameEmptyName   = errors.New("a rename name is required")
  RenameInvalidName = errors.New("invalid rename name")
  RenameNoPaths     = errors.New("at least one path is required")
)

type renameOp struct {
  src string
  dst string
}

// Rename renames the given paths within the allowed roots. The first path is
// renamed to `name`; each subsequent path is renamed to `name (N)` for its
// index, so a batch of selections receives distinct, ordered names. When the
// user's name carries an extension (e.g. "hello.zip") it is used as-is for
// every file; otherwise each file keeps its own extension, so renaming a.txt
// and b.md with the stem "report" yields "report.txt" and "report (1).md".
//
// The operation is atomic-ish: every target is conflict-checked up front, and if
// any rename fails the ones that already succeeded are rolled back, leaving the
// selection exactly as it started.
func Rename(roots *Roots, name string, paths []string) error {
  if len(paths) == 0 {
    return RenameNoPaths
  }
  name = strings.TrimSpace(name)
  if name == "" {
    return fmt.Errorf("%w: rename name is required", RenameEmptyName)
  }
  if invalidName(name) {
    return fmt.Errorf("%w: %q", RenameInvalidName, name)
  }

  items := make([]renameOp, 0, len(paths))
  sources := make(map[string]struct{}, len(paths))
  userStem, userExt := splitBase(name)
  // If the user typed an explicit extension (e.g. "hello.zip"), honour it for
  // every file. Otherwise append each file's own extension to the stem, so
  // renaming a.txt and b.md with "report" yields "report.txt" and "report (1).md".
  hasUserExt := userExt != ""
  for i, p := range paths {
    canon, err := roots.Resolve(p)
    if err != nil {
      return fsError(err)
    }
    var newName string
    if hasUserExt {
      // The user named the target exactly; only the batch suffix changes.
      newName = userStem + userExt
      if i > 0 {
        newName = fmt.Sprintf("%s (%d)%s", userStem, i, userExt)
      }
    } else {
      newName = userStem
      if i > 0 {
        newName = fmt.Sprintf("%s (%d)", userStem, i)
      }
      // Each file keeps its own extension.
      _, ext := splitBase(filepath.Base(canon))
      newName += ext
    }
    parent := filepath.Dir(canon)
    dst := filepath.Clean(filepath.Join(parent, newName))
    if !roots.Contained(dst) {
      return ErrOutsideRoots
    }
    sources[canon] = struct{}{}
    items = append(items, renameOp{src: canon, dst: dst})
  }

  // Conflict check runs before any rename so a clash leaves the selection
  // untouched. A target equal to the source's own path is a no-op.
  for _, it := range items {
    if it.dst == it.src {
      continue
    }
    if _, isSource := sources[it.dst]; isSource {
      return fmt.Errorf("%w: %q", RenameExists, filepath.Base(it.dst))
    }
    _, statErr := os.Stat(it.dst)
    switch {
    case statErr == nil:
      return fmt.Errorf("%w: %q", RenameExists, filepath.Base(it.dst))
    case os.IsNotExist(statErr):
      // Target is free.
    default:
      return fmt.Errorf("%w: could not check %q", statErr, it.dst)
    }
  }

  // Perform the renames, rolling back the completed ones on the first failure
  // so the selection is left exactly as it started.
  done := make([]renameOp, 0, len(items))
  for _, it := range items {
    if it.dst == it.src {
      continue
    }
    if err := os.Rename(it.src, it.dst); err != nil {
      for k := len(done) - 1; k >= 0; k-- {
        _ = os.Rename(done[k].dst, done[k].src)
      }
      return fmt.Errorf("%w: could not rename", err)
    }
    done = append(done, it)
  }
  return nil
}

// invalidName rejects a rename name that is `.`, `..`, or contains a path
// separator, backslash, or NUL — the same guard as Mkdir — so the computed path
// stays rooted at the source's parent and can never climb out or trip the FS.
func invalidName(name string) bool {
  return name == "." || name == ".." ||
    strings.ContainsRune(name, '/') ||
    strings.ContainsRune(name, '\\') ||
    strings.ContainsRune(name, 0)
}

// splitBase returns a file name split into its stem and its extension (the
// trailing dot and everything after it). A leading-dot name such as ".bashrc"
// is treated as extension-less so it is not renamed to a hidden file.
func splitBase(name string) (stem, ext string) {
  idx := strings.LastIndex(name, ".")
  if idx <= 0 {
    return name, ""
  }
  return name[:idx], name[idx:]
}
