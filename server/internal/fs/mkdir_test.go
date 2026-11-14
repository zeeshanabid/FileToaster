package fs

import (
  "errors"
  "os"
  "path/filepath"
  "testing"
)

// newMkdirRoots returns a Roots over a temp dir containing a.txt and returns the
// temp root for the caller to create directories inside.
func newMkdirRoots(t *testing.T) (*Roots, string) {
  t.Helper()
  root := t.TempDir()
  roots := NewRoots(root)
  if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
    t.Fatal(err)
  }
  return roots, root
}

func TestMkdirCreatesDirectory(t *testing.T) {
  roots, root := newMkdirRoots(t)
  if err := Mkdir(roots, root, "new"); err != nil {
    t.Fatalf("Mkdir: %v", err)
  }
  info, statErr := os.Stat(filepath.Join(root, "new"))
  if statErr != nil || !info.IsDir() {
    t.Fatalf("expected a directory to be created: %v", statErr)
  }
}

func TestMkdirRejectsExisting(t *testing.T) {
  roots, root := newMkdirRoots(t)
  if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
    t.Fatal(err)
  }
  err := Mkdir(roots, root, "a")
  if !errors.Is(err, ErrExists) {
    t.Fatalf("error = %v, want ErrExists", err)
  }
}

func TestMkdirRejectsEmptyName(t *testing.T) {
  roots, root := newMkdirRoots(t)
  for _, name := range []string{"", "   ", "\t"} {
    if err := Mkdir(roots, root, name); !errors.Is(err, ErrEmptyName) {
      t.Fatalf("name %q: error = %v, want ErrEmptyName", name, err)
    }
  }
}

func TestMkdirRejectsInvalidName(t *testing.T) {
  roots, root := newMkdirRoots(t)
  for _, name := range []string{"..", ".", "a/b", "a\000b"} {
    if err := Mkdir(roots, root, name); !errors.Is(err, ErrInvalidName) {
      t.Fatalf("name %q: error = %v, want ErrInvalidName", name, err)
    }
  }
}

func TestMkdirRejectsOutsideRoots(t *testing.T) {
  roots, root := newMkdirRoots(t)
  // The parent is a sibling directory that exists but lies outside the roots;
  // Resolve rejects it before any directory is created.
  outside := filepath.Join(filepath.Dir(root), "outside")
  if err := os.MkdirAll(outside, 0o755); err != nil {
    t.Fatal(err)
  }
  err := Mkdir(roots, outside, "x")
  if !errors.Is(err, ErrOutsideRoots) {
    t.Fatalf("error = %v, want ErrOutsideRoots", err)
  }
}
