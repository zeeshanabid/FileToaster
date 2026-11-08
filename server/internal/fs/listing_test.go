package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTree builds a small directory tree and returns its root.
//
//	root/
//	  a.txt        (100 bytes)
//	  b.log        (200 bytes)
//	  sub/
//	    c.txt      (300 bytes)
//	    deep/
func makeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel string, n int) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("a.txt", 100)
	mustWrite("b.log", 200)
	mustWrite("sub/c.txt", 300)
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestList(t *testing.T) {
	root := makeTree(t)
	roots := NewRoots(root)
	entries, err := List(roots, root)
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	byName := map[string]Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if _, ok := byName["a.txt"]; !ok {
		t.Errorf("expected a.txt in listing, got %+v", entries)
	}
	if d := byName["sub"]; d.Type != "dir" {
		t.Errorf("sub type = %q, want dir", d.Type)
	}
	if f := byName["a.txt"]; f.Type != "file" || f.Size != 100 {
		t.Errorf("a.txt = %+v, want file size 100", f)
	}
	for _, e := range entries {
		if e.Created.IsZero() || e.Modified.IsZero() {
			t.Errorf("entry %q has zero timestamps", e.Name)
		}
	}
}

func TestTotalSize(t *testing.T) {
	root := makeTree(t)
	roots := NewRoots(root)
	total, err := TotalSize(roots, root)
	if err != nil {
		t.Fatalf("TotalSize = %v", err)
	}
	if total != 600 {
		t.Errorf("TotalSize = %d, want 600", total)
	}
}

func TestResolveContainment(t *testing.T) {
	root := makeTree(t)
	roots := NewRoots(root)
	if _, err := roots.Resolve(root); err != nil {
		t.Errorf("resolving the root itself failed: %v", err)
	}
	if _, err := roots.Resolve(filepath.Join(root, "sub")); err != nil {
		t.Errorf("resolving a nested path failed: %v", err)
	}
	if _, err := roots.Resolve("/etc/passwd"); err == nil {
		t.Error("resolving a path outside the roots should fail")
	}
	if _, err := roots.Resolve(filepath.Join(root, "..", "etc")); err == nil {
		t.Error("resolving a traversal path should fail")
	}
	if _, err := roots.Resolve(""); err == nil {
		t.Error("resolving an empty path should fail")
	}
	if _, err := roots.Resolve(string(filepath.Separator)); err == nil {
		t.Error("resolving the filesystem root should fail")
	}
	if _, err := roots.Resolve(filepath.Join(root, "missing")); err == nil {
		t.Error("resolving a missing path should fail")
	}
}

func TestContainmentEdgeCases(t *testing.T) {
	home := t.TempDir()
	roots := NewRoots(home)
	// A sibling that merely shares a prefix must not be considered contained.
	sibling := filepath.Join(filepath.Dir(home), filepath.Base(home)+"-x")
	if roots.Contained(sibling) {
		t.Errorf("sibling %q should not be contained in %q", sibling, home)
	}
	if !roots.Contained(filepath.Join(home, "nested")) {
		t.Error("a nested path should be contained")
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1023, "1023B"},
		{1024, "1KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1MB"},
		{3 * 1024 * 1024, "3MB"},
		{1536 * 1024 * 1024, "1.5GB"},
		{1024 * 1024 * 1024 * 1024, "1TB"},
	}
	for _, c := range cases {
		if got := HumanSize(c.n); got != c.want {
			t.Errorf("HumanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestNewRootsDedupesAndDropsEmpty(t *testing.T) {
	root := t.TempDir()
	roots := NewRoots("", root, root)
	if got := roots.Allowed(); len(got) != 1 || got[0] != filepath.Clean(root) {
		t.Errorf("Allowed = %v, want single cleaned root", got)
	}
}

func TestListOutsideRootsReturnsUserMessage(t *testing.T) {
	root := makeTree(t)
	roots := NewRoots(root)
	_, err := List(roots, "/etc")
	if err == nil {
		t.Fatal("List outside roots should fail")
	}
	// The message must not leak the internal root path.
	if strings.Contains(err.Error(), root) {
		t.Errorf("List error leaked the root path: %v", err)
	}
}
