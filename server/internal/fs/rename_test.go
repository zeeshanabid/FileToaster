package fs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// newRenameRoots returns a Roots over a temp dir and the root path. The caller
// can create files/directories inside root to set up rename scenarios.
func newRenameRoots(t *testing.T) (*Roots, string) {
	t.Helper()
	root := t.TempDir()
	return NewRoots(root), root
}

// writeFiles creates the named entries (files unless the name ends with "/") in
// root and returns their absolute paths in the order given.
func writeFiles(t *testing.T, root string, names ...string) []string {
	t.Helper()
	paths := make([]string, 0, len(names))
	for _, n := range names {
		full := filepath.Join(root, n)
		if len(n) > 0 && n[len(n)-1] == '/' {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		paths = append(paths, full)
	}
	return paths
}

// listing returns the sorted base names currently in root.
func listing(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestRenameSingleFile(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "a.txt")

	if err := Rename(roots, "renamed", files); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"renamed.txt"}) {
		t.Fatalf("listing = %v, want [renamed.txt]", got)
	}
}

func TestRenameSingleFileKeepsStemWhenNameHasNoExtension(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "report.pdf")

	if err := Rename(roots, "invoice", files); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"invoice.pdf"}) {
		t.Fatalf("listing = %v, want [invoice.pdf]", got)
	}
}

func TestRenameMultipleFilesSuffixes(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "one.jpg", "two.jpg", "three.jpg")

	if err := Rename(roots, "rename", files); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"rename (1).jpg", "rename (2).jpg", "rename.jpg"}) {
		t.Fatalf("listing = %v, want the three suffixed names", got)
	}
}

func TestRenameMultipleFilesPreserveOwnExtensions(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "a.txt", "b.md", "c.log")

	if err := Rename(roots, "report", files); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"report (1).md", "report (2).log", "report.txt"}) {
		t.Fatalf("listing = %v, want per-file extensions preserved", got)
	}
}

func TestRenameDirectory(t *testing.T) {
	roots, root := newRenameRoots(t)
	if err := os.MkdirAll(filepath.Join(root, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "old")

	if err := Rename(roots, "new", []string{dir}); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"new"}) {
		t.Fatalf("listing = %v, want [new]", got)
	}
}

func TestRenameBlankName(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "a.txt")

	for _, name := range []string{"", "   ", "\t"} {
		if err := Rename(roots, name, files); !errors.Is(err, RenameEmptyName) {
			t.Fatalf("name %q: error = %v, want RenameEmptyName", name, err)
		}
	}
	// Nothing was touched.
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Fatalf("listing = %v, want [a.txt] unchanged", got)
	}
}

func TestRenameInvalidName(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "a.txt")
	for _, name := range []string{"..", ".", "a/b", "a\000b"} {
		if err := Rename(roots, name, files); !errors.Is(err, RenameInvalidName) {
			t.Fatalf("name %q: error = %v, want RenameInvalidName", name, err)
		}
	}
}

func TestRenameNoPaths(t *testing.T) {
	roots, _ := newRenameRoots(t)
	if err := Rename(roots, "x", nil); !errors.Is(err, RenameNoPaths) {
		t.Fatalf("error = %v, want RenameNoPaths", err)
	}
	if err := Rename(roots, "x", []string{}); !errors.Is(err, RenameNoPaths) {
		t.Fatalf("error = %v, want RenameNoPaths", err)
	}
}

func TestRenameConflictsAndLeavesFilesAsIs(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "one.jpg", "two.jpg")
	// A pre-existing entry that would collide with the suffixed name of the 2nd
	// file: "rename (1).jpg".
	if err := os.WriteFile(filepath.Join(root, "rename (1).jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Rename(roots, "rename", files)
	if !errors.Is(err, RenameExists) {
		t.Fatalf("error = %v, want RenameExists", err)
	}
	// The selection is left untouched (plus the pre-existing file still present).
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"one.jpg", "rename (1).jpg", "two.jpg"}) {
		t.Fatalf("listing = %v, want files unchanged", got)
	}
}

func TestRenameConflictWithAnotherSource(t *testing.T) {
	roots, root := newRenameRoots(t)
	// Renaming foo.txt to "bar" puts it on bar.txt's current path, which would
	// clobber bar.txt (also in the selection) before it is renamed.
	files := writeFiles(t, root, "foo.txt", "bar.txt")

	err := Rename(roots, "bar", files)
	if !errors.Is(err, RenameExists) {
		t.Fatalf("error = %v, want RenameExists", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"bar.txt", "foo.txt"}) {
		t.Fatalf("listing = %v, want files unchanged", got)
	}
}

func TestRenameNoOpWhenAlreadyCorrect(t *testing.T) {
	roots, root := newRenameRoots(t)
	files := writeFiles(t, root, "a.txt")

	if err := Rename(roots, "a", files); err != nil {
		t.Fatalf("Rename (no-op): %v", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Fatalf("listing = %v, want [a.txt] unchanged", got)
	}
}

func TestRenameRejectsOutsideRoots(t *testing.T) {
	roots, root := newRenameRoots(t)
	writeFiles(t, root, "a.txt")
	outside := filepath.Join(filepath.Dir(root), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// The outside file must exist so Resolve canonicalizes it, then the
	// containment check rejects it with 403 rather than a 400 "not found".
	if err := os.WriteFile(filepath.Join(outside, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Rename(roots, "x", []string{filepath.Join(outside, "a.txt")})
	if !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("error = %v, want ErrOutsideRoots", err)
	}
	// The outside file must not have moved.
	if got := listing(t, outside); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Fatalf("outside listing = %v, want [a.txt]", got)
	}
}

// TestRenameLeavesFilesAsIsOnResolutionError verifies that when one selection is
// unresolvable (its parent is a regular file, not a directory), the whole rename
// is rejected and the other, valid selections are untouched.
func TestRenameLeavesFilesAsIsOnResolutionError(t *testing.T) {
	roots, root := newRenameRoots(t)
	valid := filepath.Join(root, "a.txt")
	if err := os.WriteFile(valid, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A regular file that cannot serve as a parent for a child path.
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Rename(roots, "renamed", []string{valid, filepath.Join(blocker, "child")})
	if errors.Is(err, RenameExists) || errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("error = %v, want a resolution error", err)
	}
	if got := listing(t, root); !reflect.DeepEqual(got, []string{"a.txt", "blocker"}) {
		t.Fatalf("listing = %v, want files unchanged", got)
	}
}
