package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// --- Resolve ---------------------------------------------------------------

func TestMoveResolveSingleFile(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "hello"})
	dstDir := filepath.Join(src, "dst")
	roots := rootsFor(t, src)

	plan, err := MoveResolve(roots, []string{filepath.Join(src, "a.txt")}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(plan.Items))
	}
	// A file keeps its basename at the destination; no copy suffix on collision.
	if plan.Items[0].Dst != filepath.Join(dstDir, "a.txt") {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, filepath.Join(dstDir, "a.txt"))
	}
}

func TestMoveResolveDirCreatesNamedTarget(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "go", "pkg")
	writeTreeAt(t, src, map[string]string{"inner.txt": "x", "sub/deep.txt": "y"})
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{src}, parent)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(plan.Items))
	}
	// A single directory is recreated under its own basename; no " (copy)".
	if plan.Items[0].Dst != filepath.Join(parent, "pkg") {
		t.Errorf("Dst = %q, want %q", plan.Items[0].Dst, filepath.Join(parent, "pkg"))
	}
}

func TestMoveResolveCollisionHasNoCopySuffix(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "new"})
	// The destination folder already holds a.txt.
	dst := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{filepath.Join(src, "a.txt")}, dst)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	// The computed destination is the overwrite target as-is, never "a (copy).txt".
	if plan.Items[0].Dst != filepath.Join(dst, "a.txt") {
		t.Fatalf("Dst = %q, want %q (overwrite, no copy suffix)", plan.Items[0].Dst, filepath.Join(dst, "a.txt"))
	}
}

func TestMoveResolveErrors(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "x"})
	roots := rootsFor(t, src)

	cases := []struct {
		name    string
		paths   []string
		dest    string
		wantErr error
	}{
		{"empty paths", nil, filepath.Join(src, "dst"), ErrMoveEmptyPaths},
		{"empty dest", []string{filepath.Join(src, "a.txt")}, "", ErrMoveEmptyDest},
		{"outside roots", []string{"/etc/passwd"}, filepath.Join(src, "dst"), fs.ErrOutsideRoots},
		{"self move (file onto itself)", []string{filepath.Join(src, "a.txt")}, src, ErrSelfMove},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MoveResolve(roots, tc.paths, tc.dest)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("MoveResolve err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestMoveResolveSymlinkEscape verifies a symlink that resolves outside the
// allowed roots is rejected, just like an absolute escape.
func TestMoveResolveSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "x"})
	if err := os.MkdirAll(filepath.Join(parent, "outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "outside"), filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := MoveResolve(rootsFor(t, src), []string{filepath.Join(src, "link")}, src)
	if !errors.Is(err, fs.ErrOutsideRoots) {
		t.Fatalf("err = %v, want fs.ErrOutsideRoots", err)
	}
}

// TestMoveResolveDirIntoOwnSubtree verifies moving a directory into one of its
// own paths is rejected with ErrSelfMove.
func TestMoveResolveDirIntoOwnSubtree(t *testing.T) {
	src := writeTree(t, map[string]string{"dir/inner.txt": "x"})
	dir := filepath.Join(src, "dir")
	roots := rootsFor(t, src)

	_, err := MoveResolve(roots, []string{dir}, filepath.Join(dir, "copy"))
	if !errors.Is(err, ErrSelfMove) {
		t.Fatalf("err = %v, want ErrSelfMove", err)
	}
}

// TestMoveResolveOverlappingSelection verifies a selection that contains a path
// inside another selected path is rejected with ErrOverlappingSources.
func TestMoveResolveOverlappingSelection(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a/x.txt": "1", "a/b/y.txt": "2"})
	roots := rootsFor(t, parent)

	_, err := MoveResolve(roots, []string{filepath.Join(src, "a"), filepath.Join(src, "a", "b")}, parent)
	if !errors.Is(err, ErrOverlappingSources) {
		t.Fatalf("err = %v, want ErrOverlappingSources", err)
	}
}

// --- Run --------------------------------------------------------------------

func TestMoveRunSingleFile(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "hello"})
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{filepath.Join(src, "a.txt")}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	if err := MoveRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("MoveRun: %v", err)
	}
	// Source gone, dest exists with the new content.
	if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("a.txt still exists after move: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "a.txt")); err != nil || string(b) != "hello" {
		t.Errorf("dst a.txt = %q, err %v", b, err)
	}
}

// TestMoveRunOverwriteFile verifies moving a file onto an existing file replaces
// it atomically: the dest file now holds the new content and the source is gone.
func TestMoveRunOverwriteFile(t *testing.T) {
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

	plan, err := MoveResolve(roots, []string{filepath.Join(src, "a.txt")}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	prog := &task.Progression{}
	if err := MoveRun(context.Background(), plan, prog); err != nil {
		t.Fatalf("MoveRun: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "a.txt")); err != nil || string(b) != "new" {
		t.Errorf("dst a.txt = %q, err %v, want new", b, err)
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("src a.txt still exists after move: %v", err)
	}
	snap := prog.Snapshot()
	if snap.FilesDone != 1 || snap.FilesTotal != 1 {
		t.Errorf("files = %d/%d, want 1/1", snap.FilesDone, snap.FilesTotal)
	}
}

// TestMoveRunDirRecursive verifies a directory is moved via rename: contents are
// intact and nested under the source's basename at the destination.
func TestMoveRunDirRecursive(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{
		"root.txt":    "root",
		"a/one.txt":   "one",
		"a/b/two.txt": "two",
		"emptydir/":   "",
	})
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{src}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	prog := &task.Progression{}
	done := make(chan struct{})
	prog.SetSink(func(bp task.ByteProgress) { /* observe */ })
	go func() {
		_ = MoveRun(context.Background(), plan, prog)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MoveRun timed out")
	}
	// Source gone; contents nested under dst/src.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still exists after move: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "src", "root.txt")); err != nil || string(b) != "root" {
		t.Errorf("dst/src/root.txt = %q, err %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "src", "a", "b", "two.txt")); err != nil || string(b) != "two" {
		t.Errorf("dst/src/a/b/two.txt = %q, err %v", b, err)
	}
	snap := prog.Snapshot()
	if snap.FilesDone != 1 || snap.FilesTotal != 1 {
		t.Errorf("files = %d/%d, want 1/1 (a directory moves atomically)", snap.FilesDone, snap.FilesTotal)
	}
}

// TestMoveRunOverwriteDir verifies moving a directory onto an existing non-empty
// directory replaces it (contents swapped), the source is gone, and no stale
// temp is left behind. A directory move targets destDir/<basename>, so the
// collision is destDir/src.
func TestMoveRunOverwriteDir(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"inner.txt": "new"})
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The computed destination (dstDir/src) already exists and is non-empty.
	writeTreeAt(t, filepath.Join(dstDir, "src"), map[string]string{"old.txt": "old"})
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{src}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	if plan.Items[0].Dst != filepath.Join(dstDir, "src") {
		t.Fatalf("Dst = %q, want %q", plan.Items[0].Dst, filepath.Join(dstDir, "src"))
	}
	if err := MoveRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("MoveRun: %v", err)
	}
	// The old destination contents are gone; the source's contents are in place.
	if _, err := os.Stat(filepath.Join(dstDir, "src", "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old.txt should be gone after directory overwrite: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "src", "inner.txt")); err != nil || string(b) != "new" {
		t.Errorf("dst/src/inner.txt = %q, err %v, want new", b, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still exists after move: %v", err)
	}
	// No leftover temp directory in the destination folder.
	entries, _ := os.ReadDir(dstDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".movetmp-") {
			t.Errorf("stale temp left behind: %s", e.Name())
		}
	}
}

func TestMoveRunMixedMultiple(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "1", "sub/inner.txt": "2"})
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "sub")}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	if err := MoveRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("MoveRun: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("a.txt still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "sub")); !os.IsNotExist(err) {
		t.Errorf("sub still exists: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dstDir, "sub", "inner.txt")); err != nil || string(b) != "2" {
		t.Errorf("dst/sub/inner.txt = %q, err %v", b, err)
	}
}

// TestMoveRunProgress verifies the progress total equals the recursive byte sum
// and FilesDone increments per top-level item.
func TestMoveRunProgress(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{
		"root.txt":  "root", // 4 bytes
		"a/one.txt": "one",  // 3 bytes
	})
	dstDir := filepath.Join(parent, "dst")
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{src}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	prog := &task.Progression{}
	done := make(chan struct{})
	prog.SetSink(func(bp task.ByteProgress) { /* observe */ })
	go func() {
		_ = MoveRun(context.Background(), plan, prog)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MoveRun timed out")
	}
	snap := prog.Snapshot()
	// One top-level item (the directory); bytes = 4 + 3.
	if snap.FilesTotal != 1 || snap.FilesDone != 1 {
		t.Errorf("files = %d/%d, want 1/1", snap.FilesDone, snap.FilesTotal)
	}
	if snap.Total != 7 || snap.Done != 7 {
		t.Errorf("bytes = %d/%d, want 7/7", snap.Done, snap.Total)
	}
}

// TestMoveRunCancellationPreCancelled verifies a pre-cancelled context returns
// context.Canceled immediately and moves nothing.
func TestMoveRunCancellationPreCancelled(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "data"})
	dstDir := filepath.Join(parent, "dst")
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{filepath.Join(src, "a.txt")}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = MoveRun(ctx, plan, &task.Progression{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MoveRun err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); os.IsNotExist(err) {
		t.Errorf("a.txt was moved despite a pre-cancelled context")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("a.txt was created at the destination despite cancellation")
	}
}

// TestMoveRunCancellationStopsFurtherMoves verifies a context cancelled mid walk
// stops the remaining items: the in-flight item is moved, the next is left
// intact, and Run returns context.Canceled.
func TestMoveRunCancellationStopsFurtherMoves(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "data", "b.txt": "data"})
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := MoveResolve(roots, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt")}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prog := &task.Progression{}
	var runErr error
	done := make(chan struct{})
	prog.SetSink(func(bp task.ByteProgress) {
		cancel() // stop as soon as the first item reports progress
	})
	go func() {
		runErr = MoveRun(ctx, plan, prog)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MoveRun timed out")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("MoveRun err = %v, want context.Canceled", runErr)
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("a.txt should have been moved mid-walk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "b.txt")); os.IsNotExist(err) {
		t.Errorf("b.txt should still exist after cancellation")
	}
}

// TestMoveRunPartialFailure verifies that when one destination cannot be
// overwritten (an existing same-named directory is made read-only, so its
// temp-swap fails), the deletable siblings move, the returned error aggregates
// the failure, and progress excludes the failed item. Skipped under root, which
// bypasses DAC.
func TestMoveRunPartialFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"good.txt": "ok", "c.txt": "data"})
	dstDir := filepath.Join(parent, "dst")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// An existing same-named destination that is a non-empty, read-only
	// directory: its temp-swap cannot clear it, so this move fails while the
	// writable sibling still moves into the (writable) destination folder.
	locked := filepath.Join(dstDir, "c.txt")
	writeTreeAt(t, locked, map[string]string{"inner.txt": "x"})
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	roots := rootsFor(t, parent)

	// good.txt moves into dst cleanly; c.txt hits the read-only directory.
	plan, err := MoveResolve(roots, []string{filepath.Join(src, "good.txt"), filepath.Join(src, "c.txt")}, dstDir)
	if err != nil {
		t.Fatalf("MoveResolve: %v", err)
	}
	prog := &task.Progression{}
	runErr := MoveRun(context.Background(), plan, prog)

	// The deletable sibling moved; the failed one is left intact at the source.
	if _, err := os.Stat(filepath.Join(dstDir, "good.txt")); err != nil {
		t.Errorf("good.txt should have moved into dst: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "good.txt")); !os.IsNotExist(err) {
		t.Errorf("src/good.txt should have moved (gone): %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "c.txt")); os.IsNotExist(err) {
		t.Errorf("c.txt should still exist (its move failed): %v", err)
	}
	// The locked directory is untouched.
	if _, err := os.Stat(locked); err != nil {
		t.Errorf("locked dir should be untouched: %v", err)
	}

	var mErr *moveError
	if !errors.As(runErr, &mErr) {
		t.Fatalf("MoveRun err = %T (%v), want *moveError", runErr, runErr)
	}
	if mErr.total != 2 {
		t.Errorf("aggregated total = %d, want 2", mErr.total)
	}
	if mErr.failed != 1 {
		t.Errorf("aggregated failed = %d, want 1", mErr.failed)
	}

	snap := prog.Snapshot()
	if snap.FilesDone != 1 {
		t.Errorf("files done = %d, want 1 (the failed item is excluded)", snap.FilesDone)
	}
}

// --- dirSwap ---------------------------------------------------------------

// TestDirSwap verifies dirSwap replaces an existing non-empty dst directory with
// src, leaves no stale temp, and on failure leaves dst unchanged.
func TestDirSwap(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"new.txt": "new"})
	dst := filepath.Join(parent, "dst")
	writeTreeAt(t, dst, map[string]string{"old.txt": "old"})
	prog := &task.Progression{}

	if err := dirSwap(src, dst, 3, prog); err != nil {
		t.Fatalf("dirSwap: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "new.txt")); err != nil || string(b) != "new" {
		t.Errorf("dst/new.txt = %q, err %v, want new", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old.txt should be gone after swap: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be gone after swap: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".movetmp-") {
			t.Errorf("stale temp left behind: %s", e.Name())
		}
	}
	snap := prog.Snapshot()
	if snap.FilesDone != 1 || snap.Done != 3 {
		t.Errorf("progress = files %d/%d bytes %d/%d, want 1/1 and 3/3", snap.FilesDone, snap.FilesTotal, snap.Done, snap.Total)
	}
}

// --- copyThenDelete (cross-fs branch) --------------------------------------

func TestCopyThenDeleteFresh(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a/one.txt": "one", "b/two.txt": "two"})
	dst := filepath.Join(parent, "dst")
	prog := &task.Progression{}
	var errs []error

	if err := copyThenDelete(context.Background(), src, dst, prog, &errs); err != nil {
		t.Fatalf("copyThenDelete: %v", err)
	}
	// Source removed; recursive contents intact under dst.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still exists: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "a", "one.txt")); err != nil || string(b) != "one" {
		t.Errorf("dst/a/one.txt = %q, err %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "b", "two.txt")); err != nil || string(b) != "two" {
		t.Errorf("dst/b/two.txt = %q, err %v", b, err)
	}
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none", errs)
	}
}

func TestCopyThenDeleteOverwritesFile(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "src", "a.txt")
	writeTreeAt(t, filepath.Join(parent, "src"), map[string]string{"a.txt": "new"})
	dst := filepath.Join(parent, "dst")
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog := &task.Progression{}
	var errs []error
	if err := copyThenDelete(context.Background(), file, dst, prog, &errs); err != nil {
		t.Fatalf("copyThenDelete: %v", err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "new" {
		t.Errorf("dst = %q, err %v, want new", b, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("src still exists: %v", err)
	}
}

// TestCopyThenDeletePartialFailure verifies a mid-copy failure records the error
// into errs, does not AddFile, and leaves the source intact.
func TestCopyThenDeletePartialFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	// Create an unreadable file inside the source so the recursive copy fails.
	writeTreeAt(t, src, map[string]string{"good.txt": "ok"})
	if err := os.MkdirAll(filepath.Join(src, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(src, "bad", "locked.txt")
	if err := os.WriteFile(target, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "dst")
	prog := &task.Progression{}
	var errs []error
	copyThenDelete(context.Background(), src, dst, prog, &errs)

	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errs)
	}
	// Source left intact after the partial copy.
	if _, err := os.Stat(filepath.Join(src, "good.txt")); err != nil {
		t.Errorf("src/good.txt should still exist: %v", err)
	}
	snap := prog.Snapshot()
	if snap.FilesDone != 0 {
		t.Errorf("files done = %d, want 0 (failed copy is not item-complete)", snap.FilesDone)
	}
}

// --- sameDevice -------------------------------------------------------------

func TestSameDevice(t *testing.T) {
	parent := t.TempDir()
	a := filepath.Join(parent, "a")
	b := filepath.Join(parent, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	same, err := sameDevice(a, b)
	if err != nil {
		t.Fatalf("sameDevice: %v", err)
	}
	if !same {
		t.Errorf("sameDevice(%q, %q) = false, want true (same temp dir)", a, b)
	}
	if _, err := sameDevice(filepath.Join(parent, "missing"), parent); err == nil {
		t.Errorf("sameDevice(missing) = nil, want error")
	}
}
