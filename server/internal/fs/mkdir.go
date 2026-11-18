package fs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mkdir errors. The sentinels let the API layer map a failure to a precise HTTP
// status and a UI-facing message without leaking internal details.
var (
	ErrExists      = errors.New("directory already exists")
	ErrEmptyName   = errors.New("directory name is required")
	ErrInvalidName = errors.New("invalid directory name")
)

// Mkdir creates a new directory named `name` directly inside `parent`, confined
// to the allowed roots. The parent must resolve and exist; the created path must
// stay within a root. An existing target returns ErrExists so the UI can ask the
// user to rename or overwrite.
func Mkdir(roots *Roots, parent, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: directory name is required", ErrEmptyName)
	}
	// A name may not be `.`, `..`, contain a path separator, backslash, or NUL.
	// Rejecting these keeps the created path rooted at parent+name and can never
	// climb out or trip the filesystem.
	if name == "." || name == ".." ||
		strings.ContainsRune(name, '/') ||
		strings.ContainsRune(name, '\\') ||
		strings.ContainsRune(name, 0) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}

	canonParent, err := roots.Resolve(parent)
	if err != nil {
		return fsError(err)
	}
	target := filepath.Clean(filepath.Join(canonParent, name))
	if !roots.Contained(target) {
		return ErrOutsideRoots
	}
	if _, statErr := os.Stat(target); statErr == nil {
		return fmt.Errorf("%w: %q", ErrExists, name)
	}
	if mkErr := os.Mkdir(target, 0o755); mkErr != nil {
		return fmt.Errorf("%w: could not create the directory", mkErr)
	}
	return nil
}
