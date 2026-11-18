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
// index, so a batch of selections receives distinct, ordered names. The name is
// used exactly as the user typed it: an extension the user includes (e.g.
// "hello.zip") is kept, and no extension is appended when the user omits one, so
// renaming hello.txt with the stem "hello" yields exactly "hello".
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
	for i, p := range paths {
		canon, err := roots.Resolve(p)
		if err != nil {
			return fsError(err)
		}
		// The target name is exactly the user's string; only a batch suffix is
		// added for later selections. No extension is appended or swapped in, so a
		// bare "hello" renames hello.txt to exactly "hello".
		var newName string
		if i > 0 {
			newName = fmt.Sprintf("%s (%d)", name, i)
		} else {
			newName = name
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
