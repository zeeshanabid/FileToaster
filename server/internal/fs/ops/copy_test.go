package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// writeTree creates a temp directory tree rooted at t and returns its path.
func writeTree(t *testing.T, layout map[string]string) string {
	t.Helper()
	return writeTreeAt(t, t.TempDir(), layout)
}

// writeTreeAt writes a tree under the given root and returns the root.
func writeTreeAt(t *testing.T, root string, layout map[string]string) string {
	t.Helper()
	for rel, content := range layout {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if content == "" {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func rootsFor(t *testing.T, dirs ...string) *fs.Roots {
	t.Helper()
	clean := make([]string, 0, len(dirs))
	for _, d := range dirs {
		clean = append(clean, filepath.Clean(d))
	}
	return fs.NewRoots(clean...)
}

func TestResolveValidSingleFile(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "hello"})
	dstDir := filepath.Join(src, "dst")
	roots := rootsFor(t, src)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, dstDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(plan.Items))
	}
	want := filepath.Join(dstDir, "a.txt")
	if plan.Items[0].Dst != want {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, want)
	}
}

func TestResolveDetectsConflict(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "hello"})
	// Destination folder already exists with a file of the same name.
	existingDst := filepath.Join(parent, "dst")
	if err := os.MkdirAll(existingDst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingDst, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	// The computed destination already exists, so Resolve renames it to a
	// " (copy)" variant instead of reporting a conflict to clobber "old".
	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, existingDst)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Items[0].Dst != filepath.Join(existingDst, "a (copy).txt") {
		t.Fatalf("Dst = %q, want %q", plan.Items[0].Dst, filepath.Join(existingDst, "a (copy).txt"))
	}
}

func TestResolveErrors(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "x"})
	roots := rootsFor(t, src)

	cases := []struct {
		name    string
		paths   []string
		dest    string
		wantErr error
	}{
		{"empty paths", nil, filepath.Join(src, "dst"), ErrEmptyPaths},
		{"empty dest", []string{filepath.Join(src, "a.txt")}, "", ErrEmptyDest},
		{"outside roots", []string{"/etc/passwd"}, filepath.Join(src, "dst"), fs.ErrOutsideRoots},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(roots, tc.paths, tc.dest)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Resolve err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveDestIsFileWithMultiple(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "1", "b.txt": "2"})
	fileTarget := filepath.Join(src, "a.txt")
	roots := rootsFor(t, src)

	_, err := Resolve(roots, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt")}, fileTarget)
	if !errors.Is(err, ErrDestIsFile) {
		t.Fatalf("err = %v, want ErrDestIsFile", err)
	}
}

func TestResolveSelfCopy(t *testing.T) {
	src := writeTree(t, map[string]string{"dir/inner.txt": "x"})
	dir := filepath.Join(src, "dir")
	roots := rootsFor(t, src)

	_, err := Resolve(roots, []string{dir}, filepath.Join(dir, "copy"))
	if !errors.Is(err, ErrSelfCopy) {
		t.Fatalf("err = %v, want ErrSelfCopy", err)
	}
}

func TestResolveSamePathProducesCopy(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "hello"})
	roots := rootsFor(t, src)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(plan.Items))
	}
	// The destination must not equal the source and must carry a copy marker.
	if plan.Items[0].Dst == plan.Items[0].Src {
		t.Fatalf("Dst %q must not equal Src %q", plan.Items[0].Dst, plan.Items[0].Src)
	}
	want := filepath.Join(src, "a (copy).txt")
	if plan.Items[0].Dst != want {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, want)
	}
}

func TestResolveSamePathNoExtensionCopy(t *testing.T) {
	src := writeTree(t, map[string]string{"Makefile": "x"})
	roots := rootsFor(t, src)

	plan, err := Resolve(roots, []string{filepath.Join(src, "Makefile")}, src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Items[0].Dst != filepath.Join(src, "Makefile (copy)") {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, filepath.Join(src, "Makefile (copy)"))
	}
}

func TestResolveSamePathDirProducesError(t *testing.T) {
	src := writeTree(t, map[string]string{"dir/inner.txt": "x"})
	dir := filepath.Join(src, "dir")
	roots := rootsFor(t, src)

	_, err := Resolve(roots, []string{dir}, dir)
	if !errors.Is(err, ErrSelfCopy) {
		t.Fatalf("err = %v, want ErrSelfCopy", err)
	}
}

func TestResolveCopyDirIntoParentProducesCopy(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"dir/inner.txt": "x"})
	dir := filepath.Join(src, "dir")
	roots := rootsFor(t, parent)

	// Pasting a directory into its own parent folder lands the computed target
	// back on the source path (parent/dir), so Resolve renames it to a
	// " (copy)" variant instead of merging the contents into the parent.
	plan, err := Resolve(roots, []string{dir}, src)
	if err != nil {
		t.Fatalf("Resolve: got %v, want no error", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(plan.Items))
	}
	want := filepath.Join(src, "dir (copy)")
	if plan.Items[0].Dst != want {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, want)
	}
}

func TestRunSamePathCopyPreservesOriginal(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "root"})
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := Run(context.Background(), plan, &task.Progression{}, DefaultCopyConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The original file is untouched ...
	if b, _ := os.ReadFile(filepath.Join(src, "a.txt")); string(b) != "root" {
		t.Errorf("original a.txt = %q, want root", b)
	}
	// ... and the copy landed alongside it.
	if b, err := os.ReadFile(filepath.Join(src, "a (copy).txt")); err != nil || string(b) != "root" {
		t.Errorf("a (copy).txt = %q, err %v", b, err)
	}
}

func TestResolveCopyIntoSubdirectory(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "hello"})
	dst := filepath.Join(parent, "dst") // a not-yet-existing destination folder
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, dst)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Items[0].Dst != filepath.Join(dst, "a.txt") {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, filepath.Join(dst, "a.txt"))
	}
}

func TestRunCopyIntoSubdirectory(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "hello"})
	dst := filepath.Join(parent, "dst") // a not-yet-existing destination folder
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, dst)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := Run(context.Background(), plan, &task.Progression{}, DefaultCopyConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The destination folder is created and the file lands inside it, byte-identical.
	if st, err := os.Stat(dst); err != nil || !st.IsDir() {
		t.Errorf("dst not created: err %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil || string(b) != "hello" {
		t.Errorf("dst a.txt = %q, err %v", b, err)
	}
}

func TestRunRecursiveCopy(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{
		"root.txt":    "root",
		"a/one.txt":   "one",
		"a/b/two.txt": "two",
		"emptydir/":   "",
	})
	dstDir := filepath.Join(parent, "dst")
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{src}, dstDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	prog := &task.Progression{}
	done := make(chan struct{})
	prog.SetSink(func(bp task.ByteProgress) { /* observe */ })
	go func() {
		_ = Run(context.Background(), plan, prog, DefaultCopyConfig())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run timed out")
	}

	snap := prog.Snapshot()
	if snap.FilesDone != 3 {
		t.Errorf("files done = %d, want 3", snap.FilesDone)
	}
	if snap.Total != 4+3+3 {
		t.Errorf("total bytes = %d, want %d", snap.Total, 4+3+3)
	}
	// The copy is byte-identical and preserves the directory structure, nested
	// under the source directory's own basename at the destination.
	if b, err := os.ReadFile(filepath.Join(dstDir, "src", "root.txt")); err != nil || string(b) != "root" {
		t.Errorf("src/root.txt = %q, err %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "src", "a", "b", "two.txt")); err != nil || string(b) != "two" {
		t.Errorf("src/a/b/two.txt = %q, err %v", b, err)
	}
	if st, err := os.Stat(filepath.Join(dstDir, "src", "emptydir")); err != nil || !st.IsDir() {
		t.Errorf("src/emptydir not copied: err %v", err)
	}
}

func TestRunOverwritesExisting(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "new"})
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, dstDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := Run(context.Background(), plan, &task.Progression{}, DefaultCopyConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The pre-existing file is not clobbered; the copy lands alongside it.
	if b, _ := os.ReadFile(filepath.Join(dstDir, "a.txt")); string(b) != "old" {
		t.Errorf("a.txt = %q, want old", b)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "a (copy).txt")); err != nil || string(b) != "new" {
		t.Errorf("a (copy).txt = %q, err %v", b, err)
	}
}

func TestRunCancellation(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "data"})
	dstDir := filepath.Join(parent, "dst")
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, dstDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	err = Run(ctx, plan, &task.Progression{}, DefaultCopyConfig())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
}

// TestResolveDestMustBeDirectory verifies the destination is always treated as a
// directory: pasting a single item onto an existing file (not a folder) is
// rejected, since a file is not a valid paste target.
func TestResolveDestMustBeDirectory(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "x", "sub/inner.txt": "y"})
	fileTarget := filepath.Join(src, "a.txt")
	roots := rootsFor(t, src)

	// The destination must be a directory regardless of the source type: a file
	// source and a directory source into an existing file are both rejected.
	cases := []string{
		filepath.Join(src, "a.txt"),
		filepath.Join(src, "sub"),
	}
	for _, srcPath := range cases {
		_, err := Resolve(roots, []string{srcPath}, fileTarget)
		if !errors.Is(err, ErrDestIsFile) {
			t.Fatalf("src %s: err = %v, want ErrDestIsFile", srcPath, err)
		}
	}
}

// TestResolveNumberedCopySuffixes verifies that when the " (copy)" target also
// exists, numbering continues (" (copy) 1", " (copy) 2", …) until a free name.
func TestResolveNumberedCopySuffixes(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "new"})
	// The destination folder already holds a.txt, a (copy).txt and a (copy) 1.txt.
	dst := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "a (copy).txt", "a (copy) 1.txt"} {
		if err := os.WriteFile(filepath.Join(dst, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, dst)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := filepath.Join(dst, "a (copy) 2.txt")
	if plan.Items[0].Dst != want {
		t.Fatalf("Dst = %q, want %q", plan.Items[0].Dst, want)
	}
}

// TestRunNumberedCopySuffixes verifies the numbered copies actually land on disk
// without clobbering any pre-existing file.
func TestRunNumberedCopySuffixes(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "new"})
	dst := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "a (copy).txt", "a (copy) 1.txt"} {
		if err := os.WriteFile(filepath.Join(dst, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a.txt")}, dst)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := Run(context.Background(), plan, &task.Progression{}, DefaultCopyConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, name := range []string{"a.txt", "a (copy).txt", "a (copy) 1.txt"} {
		if b, err := os.ReadFile(filepath.Join(dst, name)); err != nil || string(b) != "old" {
			t.Errorf("%s = %q, err %v, want old", name, b, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dst, "a (copy) 2.txt")); err != nil || string(b) != "new" {
		t.Errorf("a (copy) 2.txt = %q, err %v, want new", b, err)
	}
}

// TestResolveCopyMultipleDirsIntoExistingFolderRenames verifies that when several
// directories are pasted and one already exists at the target basename, that one
// is renamed while the others keep their names.
func TestResolveCopyMultipleDirsIntoExistingFolderRenames(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a/x": "1", "b/y": "2"})
	dst := filepath.Join(parent, "dst")
	// existing/b already exists, so it must be renamed to "b (copy)".
	if err := os.MkdirAll(filepath.Join(dst, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{filepath.Join(src, "a"), filepath.Join(src, "b")}, dst)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := map[string]bool{}
	for _, it := range plan.Items {
		names[filepath.Base(it.Dst)] = true
	}
	if !names["a"] || !names["b (copy)"] {
		t.Fatalf("dst basenames = %v, want a and b (copy)", names)
	}
}

// TestRunCopyDirIntoParentProducesCopy verifies running a copy of a directory
// into its own parent folder writes "<parent>/<name> (copy)" without merging
// the contents into the parent — exactly what happens when copying
// /data/src/sub into /data/src.
func TestRunCopyDirIntoParentProducesCopy(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"sub/inner.txt": "x", "sub/note.txt": "n"})
	srcDir := filepath.Join(src, "sub")
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{srcDir}, src)
	if err != nil {
		t.Fatalf("Resolve: %v, want no error", err)
	}
	if err := Run(context.Background(), plan, &task.Progression{}, DefaultCopyConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The original directory is untouched.
	if b, err := os.ReadFile(filepath.Join(srcDir, "inner.txt")); err != nil || string(b) != "x" {
		t.Errorf("sub/inner.txt = %q, err %v", b, err)
	}
	// The copy lands as "sub (copy)", with its contents nested under it.
	if b, err := os.ReadFile(filepath.Join(src, "sub (copy)", "inner.txt")); err != nil || string(b) != "x" {
		t.Errorf("sub (copy)/inner.txt = %q, err %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(src, "sub (copy)", "note.txt")); err != nil || string(b) != "n" {
		t.Errorf("sub (copy)/note.txt = %q, err %v", b, err)
	}
	// No merge occurred: src must not gain sub's children directly.
	if _, err := os.Stat(filepath.Join(src, "inner.txt")); err == nil {
		t.Errorf("src/inner.txt should not exist (contents were not merged into the parent)")
	}
}

// TestResolveCopyDirCreatesNamedTarget verifies a single directory pasted into a
// folder (not its own parent) is recreated under its basename: pkg pasted into
// /root/ becomes /root/pkg, so its contents never spill into the target folder.
func TestResolveCopyDirCreatesNamedTarget(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "go", "pkg")
	writeTreeAt(t, src, map[string]string{"inner.txt": "x", "sub/deep.txt": "y"})
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{src}, parent)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(plan.Items))
	}
	want := filepath.Join(parent, "pkg")
	if plan.Items[0].Dst != want {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, want)
	}
}

// TestResolveCopyDirRenamesWhenTargetExists verifies the established rename
// logic: pasting pkg into /root/ when /root/pkg already exists yields
// /root/pkg (copy), never clobbering the pre-existing directory.
func TestResolveCopyDirRenamesWhenTargetExists(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "go", "pkg")
	writeTreeAt(t, src, map[string]string{"inner.txt": "x"})
	existing := filepath.Join(parent, "pkg")
	writeTreeAt(t, existing, map[string]string{"old.txt": "old"})
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{src}, parent)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := filepath.Join(parent, "pkg (copy)")
	if plan.Items[0].Dst != want {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, want)
	}
}

// TestRunCopyDirCreatesNamedTarget verifies running the plan recreates the
// source directory under the target folder with its contents intact.
func TestRunCopyDirCreatesNamedTarget(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "go", "pkg")
	writeTreeAt(t, src, map[string]string{"inner.txt": "x", "sub/deep.txt": "y"})
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{src}, parent)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := Run(context.Background(), plan, &task.Progression{}, DefaultCopyConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(parent, "pkg", "inner.txt")); err != nil || string(b) != "x" {
		t.Errorf("pkg/inner.txt = %q, err %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(parent, "pkg", "sub", "deep.txt")); err != nil || string(b) != "y" {
		t.Errorf("pkg/sub/deep.txt = %q, err %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "inner.txt")); string(b) != "x" {
		t.Errorf("original inner.txt = %q, want x", b)
	}
}

// TestRunCopyDirRenamesWhenTargetExists verifies running the plan writes the
// copy to "pkg (copy)" without clobbering the pre-existing directory.
func TestRunCopyDirRenamesWhenTargetExists(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "go", "pkg")
	writeTreeAt(t, src, map[string]string{"inner.txt": "new"})
	existing := filepath.Join(parent, "pkg")
	writeTreeAt(t, existing, map[string]string{"keep.txt": "old"})
	roots := rootsFor(t, parent)

	plan, err := Resolve(roots, []string{src}, parent)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := Run(context.Background(), plan, &task.Progression{}, DefaultCopyConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(existing, "keep.txt")); string(b) != "old" {
		t.Errorf("pkg/keep.txt = %q, want old", b)
	}
	if b, err := os.ReadFile(filepath.Join(parent, "pkg (copy)", "inner.txt")); err != nil || string(b) != "new" {
		t.Errorf("pkg (copy)/inner.txt = %q, err %v", b, err)
	}
}
