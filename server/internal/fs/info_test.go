package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blockUsage returns the on-disk size (allocated blocks, du-style) of p.
func blockUsage(p string) int64 {
	info, err := os.Lstat(p)
	if err != nil {
		panic(err)
	}
	return blockBytes(info)
}

// infoFixture builds a nested tree under a temp dir and returns its resolved
// path: a.txt (50) + b.txt (70) + sub/c.txt (30) + sub/deep/d.txt (10).
func infoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel string, n int) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", 50)
	write("b.txt", 70)
	write(filepath.Join("sub", "c.txt"), 30)
	write(filepath.Join("sub", "deep", "d.txt"), 10)
	return root
}

func TestInfoDirRecursive(t *testing.T) {
	root := infoFixture(t)
	roots := NewRoots(root)
	info, err := Info(roots, root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != "dir" {
		t.Errorf("type = %q, want dir", info.Type)
	}
	// Size is the on-disk footprint: every directory's own blocks plus the
	// allocated blocks of each file within it (du-style), not the apparent bytes.
	want := blockUsage(root) + blockUsage(filepath.Join(root, "sub")) +
		blockUsage(filepath.Join(root, "sub", "deep"))
	for _, f := range []string{"a.txt", "b.txt", "sub/c.txt", "sub/deep/d.txt"} {
		want += blockUsage(filepath.Join(root, f))
	}
	if info.Size != want {
		t.Errorf("size = %d, want on-disk %d", info.Size, want)
	}
	if info.FileCount != 4 {
		t.Errorf("fileCount = %d, want 4", info.FileCount)
	}
	if info.DirCount != 2 {
		t.Errorf("dirCount = %d, want 2", info.DirCount)
	}
	if !strings.HasPrefix(info.Perms, "d") {
		t.Errorf("perms = %q, want to start with 'd'", info.Perms)
	}
}

func TestInfoFileSelf(t *testing.T) {
	root := infoFixture(t)
	roots := NewRoots(root)
	info, err := Info(roots, filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != "file" {
		t.Errorf("type = %q, want file", info.Type)
	}
	// A file reports its allocated footprint, so a 50-byte file counts a full
	// block rather than its 50 apparent bytes.
	if info.Size != blockUsage(filepath.Join(root, "a.txt")) {
		t.Errorf("size = %d, want on-disk %d", info.Size, blockUsage(filepath.Join(root, "a.txt")))
	}
	if info.FileCount != 1 {
		t.Errorf("fileCount = %d, want 1", info.FileCount)
	}
	if info.DirCount != 0 {
		t.Errorf("dirCount = %d, want 0", info.DirCount)
	}
	if !strings.HasPrefix(info.Perms, "-") {
		t.Errorf("perms = %q, want to start with '-'", info.Perms)
	}
}

func TestInfoRejectsEscape(t *testing.T) {
	root := infoFixture(t)
	roots := NewRoots(root)
	outside := filepath.Join(filepath.Dir(root), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Info(roots, outside); err == nil {
		t.Fatal("expected error for path outside roots, got nil")
	}
}

func TestPermString(t *testing.T) {
	cases := []struct {
		mode os.FileMode
		want string
	}{
		{os.ModeDir | 0o755, "drwxr-xr-x"},
		{os.ModeSymlink | 0o777, "lrwxrwxrwx"},
		{0o644, "-rw-r--r--"},
		{0o600, "-rw-------"},
		{0o640, "-rw-r-----"},
	}
	for _, tc := range cases {
		if got := permString(tc.mode); got != tc.want {
			t.Errorf("permString(%o) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}
