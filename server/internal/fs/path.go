// Package fs exposes the filesystem operations FileToaster's UI needs:
// listing a directory, computing a directory's total size, and — most
// importantly — confining every operation to the server's configured allowed
// roots. Path-traversal, symlink, and absolute escapes are rejected so a client
// can never reach anything outside those roots.
package fs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutsideRoots signals that a requested path is not contained within any of
// the configured allowed roots.
var ErrOutsideRoots = errors.New("path is outside the allowed roots")

// Roots is the set of directories the server may serve. Every path handed to an
// fs function must be canonicalized and contained within one of these roots.
type Roots struct {
	roots []string
}

// NewRoots builds a Roots from already-resolved absolute directory paths. The
// paths are cleaned; empty entries are dropped.
func NewRoots(paths ...string) *Roots {
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		clean := filepath.Clean(p)
		if clean == "" || clean == "." {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		normalized = append(normalized, clean)
	}
	return &Roots{roots: normalized}
}

// Allowed returns the resolved absolute roots, in order.
func (r *Roots) Allowed() []string { return r.roots }

// Contained reports whether canonPath (already cleaned) lies within a root,
// either equal to it or nested under it.
func (r *Roots) Contained(canonPath string) bool {
	canonPath = filepath.Clean(canonPath)
	for _, root := range r.roots {
		if canonPath == root || strings.HasPrefix(canonPath, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Resolve canonicalizes rawPath (following symlinks) and verifies it lies within
// a root. It returns the canonical path, or an error for a missing path, a
// symlink escape, or an absolute escape.
func (r *Roots) Resolve(rawPath string) (string, error) {
	if rawPath == "" {
		return "", errors.New("path is required")
	}
	if rawPath == string(filepath.Separator) {
		return "", errors.New("cannot list the filesystem root")
	}
	canonical, err := filepath.EvalSymlinks(rawPath)
	if err != nil {
		return "", fmt.Errorf("path is not accessible: %w", err)
	}
	if !r.Contained(canonical) {
		return "", ErrOutsideRoots
	}
	return canonical, nil
}
