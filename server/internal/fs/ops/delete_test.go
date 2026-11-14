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

func TestDeleteResolveEmptyPaths(t *testing.T) {
  src := writeTree(t, map[string]string{"a.txt": "x"})
  _, err := DeleteResolve(rootsFor(t, src), nil)
  if !errors.Is(err, DeleteErrEmptyPaths) {
    t.Fatalf("err = %v, want DeleteErrEmptyPaths", err)
  }
}

func TestDeleteResolveOutsideRoots(t *testing.T) {
  src := writeTree(t, map[string]string{"a.txt": "x"})
  _, err := DeleteResolve(rootsFor(t, src), []string{"/etc/passwd"})
  if !errors.Is(err, fs.ErrOutsideRoots) {
    t.Fatalf("err = %v, want fs.ErrOutsideRoots", err)
  }
}

// TestDeleteResolveSymlinkEscape verifies a symlink that resolves to a path
// outside the allowed roots is rejected, just like an absolute escape.
func TestDeleteResolveSymlinkEscape(t *testing.T) {
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  writeTreeAt(t, src, map[string]string{"a.txt": "x"})
  if err := os.MkdirAll(filepath.Join(parent, "outside"), 0o755); err != nil {
    t.Fatal(err)
  }
  if err := os.Symlink(filepath.Join(parent, "outside"), filepath.Join(src, "link")); err != nil {
    t.Fatal(err)
  }
  _, err := DeleteResolve(rootsFor(t, src), []string{filepath.Join(src, "link")})
  if !errors.Is(err, fs.ErrOutsideRoots) {
    t.Fatalf("err = %v, want fs.ErrOutsideRoots", err)
  }
}

// TestDeleteResolveAllowedRootRejected verifies deleting a configured allowed
// root itself is rejected with DeleteErrRootPath (defense-in-depth).
func TestDeleteResolveAllowedRootRejected(t *testing.T) {
  src := writeTree(t, map[string]string{"a.txt": "x"})
  _, err := DeleteResolve(rootsFor(t, src), []string{src})
  if !errors.Is(err, DeleteErrRootPath) {
    t.Fatalf("err = %v, want DeleteErrRootPath", err)
  }
}

// TestDeleteResolveMixedMultiple verifies multiple files and directories resolve
// to their canonical absolute paths while preserving request order and order.
func TestDeleteResolveMixedMultiple(t *testing.T) {
  parent := t.TempDir()
  writeTreeAt(t, filepath.Join(parent, "a"), map[string]string{"a.txt": "1"})
  writeTreeAt(t, filepath.Join(parent, "b", "sub"), map[string]string{"b.txt": "2"})
  roots := rootsFor(t, parent)

  plan, err := DeleteResolve(roots, []string{
    filepath.Join(parent, "b"),
    filepath.Join(parent, "a", "a.txt"),
  })
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  if len(plan.Paths) != 2 {
    t.Fatalf("paths = %d, want 2", len(plan.Paths))
  }
  if plan.Paths[0] != filepath.Join(parent, "b") {
    t.Errorf("paths[0] = %q, want %q", plan.Paths[0], filepath.Join(parent, "b"))
  }
  if plan.Paths[1] != filepath.Join(parent, "a", "a.txt") {
    t.Errorf("paths[1] = %q, want %q", plan.Paths[1], filepath.Join(parent, "a", "a.txt"))
  }
}

// --- Run --------------------------------------------------------------------

func TestDeleteRunSingleFile(t *testing.T) {
  src := writeTree(t, map[string]string{"a.txt": "hello"})
  roots := rootsFor(t, src)

  plan, err := DeleteResolve(roots, []string{filepath.Join(src, "a.txt")})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  if err := DeleteRun(context.Background(), plan, &task.Progression{}); err != nil {
    t.Fatalf("DeleteRun: %v", err)
  }
  if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
    t.Errorf("a.txt still exists after delete: %v", err)
  }
}

func TestDeleteRunDirRecursive(t *testing.T) {
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  writeTreeAt(t, src, map[string]string{"a/one.txt": "1", "a/b/two.txt": "2"})
  roots := rootsFor(t, parent)

  plan, err := DeleteResolve(roots, []string{src})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  if err := DeleteRun(context.Background(), plan, &task.Progression{}); err != nil {
    t.Fatalf("DeleteRun: %v", err)
  }
  // Parent and all contents are gone.
  if _, err := os.Stat(src); !os.IsNotExist(err) {
    t.Errorf("src still exists after delete: %v", err)
  }
  if _, err := os.Stat(filepath.Join(src, "a", "b", "two.txt")); !os.IsNotExist(err) {
    t.Errorf("nested file still exists after delete: %v", err)
  }
}

func TestDeleteRunEmptyDir(t *testing.T) {
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  if err := os.MkdirAll(filepath.Join(src, "empty"), 0o755); err != nil {
    t.Fatal(err)
  }
  roots := rootsFor(t, parent)

  plan, err := DeleteResolve(roots, []string{filepath.Join(src, "empty")})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  prog := &task.Progression{}
  if err := DeleteRun(context.Background(), plan, prog); err != nil {
    t.Fatalf("DeleteRun: %v", err)
  }
  if _, err := os.Stat(filepath.Join(src, "empty")); !os.IsNotExist(err) {
    t.Errorf("empty dir still exists after delete: %v", err)
  }
  snap := prog.Snapshot()
  if snap.FilesTotal != 1 || snap.FilesDone != 1 {
    t.Errorf("files = %d/%d, want 1/1 (the empty dir itself)", snap.FilesDone, snap.FilesTotal)
  }
  if snap.Total != 0 || snap.Done != 0 {
    t.Errorf("bytes = %d/%d, want 0/0", snap.Done, snap.Total)
  }
}

// TestDeleteRunProgressCountsRecursive verifies the progress total (files +
// directories and bytes) is measured up front and every entry is reported as it
// is removed, so FilesDone reaches the recursive count and Done the byte total.
func TestDeleteRunProgressCountsRecursive(t *testing.T) {
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  writeTreeAt(t, src, map[string]string{
    "root.txt":    "root", // 1 file, 4 bytes
    "a/one.txt":   "one",  // 1 file, 3 bytes
    "a/b/two.txt": "two",  // 1 file, 3 bytes
    "emptydir/":   "",     // 1 directory
  })
  roots := rootsFor(t, parent)

  plan, err := DeleteResolve(roots, []string{src})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  prog := &task.Progression{}
  done := make(chan struct{})
  prog.SetSink(func(bp task.ByteProgress) { /* observe */ })
  go func() {
    _ = DeleteRun(context.Background(), plan, prog)
    close(done)
  }()
  select {
  case <-done:
  case <-time.After(5 * time.Second):
    t.Fatal("DeleteRun timed out")
  }

  snap := prog.Snapshot()
  // Items: src + root.txt + a + a/one.txt + a/b + a/b/two.txt + emptydir = 7
  // (the top-level target is itself removed and progress-counted, so the bar
  // can reach 100%).
  if snap.FilesTotal != 7 {
    t.Errorf("files total = %d, want 7", snap.FilesTotal)
  }
  if snap.FilesDone != 7 {
    t.Errorf("files done = %d, want 7", snap.FilesDone)
  }
  // Bytes: 4 + 3 + 3 = 10.
  if snap.Total != 10 {
    t.Errorf("total bytes = %d, want 10", snap.Total)
  }
  if snap.Done != 10 {
    t.Errorf("done bytes = %d, want 10", snap.Done)
  }
}

func TestDeleteRunMixedMultiplePaths(t *testing.T) {
  parent := t.TempDir()
  writeTreeAt(t, filepath.Join(parent, "a"), map[string]string{"a.txt": "1"})
  writeTreeAt(t, filepath.Join(parent, "b", "sub"), map[string]string{"b.txt": "2"})
  roots := rootsFor(t, parent)

  plan, err := DeleteResolve(roots, []string{filepath.Join(parent, "a"), filepath.Join(parent, "b")})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  if err := DeleteRun(context.Background(), plan, &task.Progression{}); err != nil {
    t.Fatalf("DeleteRun: %v", err)
  }
  if _, err := os.Stat(filepath.Join(parent, "a")); !os.IsNotExist(err) {
    t.Errorf("a still exists after delete: %v", err)
  }
  if _, err := os.Stat(filepath.Join(parent, "b")); !os.IsNotExist(err) {
    t.Errorf("b still exists after delete: %v", err)
  }
}

// TestDeleteRunCancellationPreCancelled verifies a pre-cancelled context returns
// context.Canceled immediately and removes nothing.
func TestDeleteRunCancellationPreCancelled(t *testing.T) {
  src := writeTree(t, map[string]string{"a.txt": "data"})
  roots := rootsFor(t, src)

  plan, err := DeleteResolve(roots, []string{filepath.Join(src, "a.txt")})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  ctx, cancel := context.WithCancel(context.Background())
  cancel() // already cancelled
  err = DeleteRun(ctx, plan, &task.Progression{})
  if !errors.Is(err, context.Canceled) {
    t.Fatalf("DeleteRun err = %v, want context.Canceled", err)
  }
  if _, err := os.Stat(filepath.Join(src, "a.txt")); os.IsNotExist(err) {
    t.Errorf("a.txt was removed despite a pre-cancelled context")
  }
}

// TestDeleteRunCancellationStopsFurtherRemoval verifies a context cancelled mid
// walk aborts the remaining paths: the item that was being processed is removed,
// the next path is left intact, and Run returns context.Canceled.
func TestDeleteRunCancellationStopsFurtherRemoval(t *testing.T) {
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  writeTreeAt(t, src, map[string]string{"a.txt": "data", "b.txt": "data"})
  roots := rootsFor(t, parent)

  plan, err := DeleteResolve(roots, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt")})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  ctx, cancel := context.WithCancel(context.Background())
  prog := &task.Progression{}
  var runErr error
  done := make(chan struct{})
  prog.SetSink(func(bp task.ByteProgress) {
    cancel() // stop as soon as the first item reports progress
  })
  go func() {
    runErr = DeleteRun(ctx, plan, prog)
    close(done)
  }()
  select {
  case <-done:
  case <-time.After(5 * time.Second):
    t.Fatal("DeleteRun timed out")
  }
  if !errors.Is(runErr, context.Canceled) {
    t.Fatalf("DeleteRun err = %v, want context.Canceled", runErr)
  }
  // The first file was in flight (removed); the second must still exist because
  // the walk stopped before reaching it.
  if _, err := os.Stat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
    t.Errorf("a.txt should have been removed mid-walk: %v", err)
  }
  if _, err := os.Stat(filepath.Join(src, "b.txt")); os.IsNotExist(err) {
    t.Errorf("b.txt should still exist after cancellation")
  }
}

// TestDeleteRunPartialFailure verifies that when one entry cannot be deleted
// (its parent directory is made undeletable), the deletable siblings are still
// removed, the returned error aggregates every failure, and the progress counts
// exclude the failed entries. Skipped under root, which bypasses DAC.
func TestDeleteRunPartialFailure(t *testing.T) {
  if os.Geteuid() == 0 {
    t.Skip("root bypasses permission checks")
  }
  parent := t.TempDir()
  src := filepath.Join(parent, "src")
  writeTreeAt(t, src, map[string]string{"good.txt": "ok", "bad/inner.txt": "x"})
  // Remove write permission so the contents (and the directory itself) cannot
  // be deleted. The parent (src) stays writable, so os.Remove(bad) fails on a
  // non-empty directory.
  if err := os.Chmod(filepath.Join(src, "bad"), 0o500); err != nil {
    t.Fatal(err)
  }
  defer os.Chmod(filepath.Join(src, "bad"), 0o755)
  roots := rootsFor(t, parent)

  plan, err := DeleteResolve(roots, []string{filepath.Join(src, "good.txt"), filepath.Join(src, "bad")})
  if err != nil {
    t.Fatalf("DeleteResolve: %v", err)
  }
  prog := &task.Progression{}
  runErr := DeleteRun(context.Background(), plan, prog)

  // The deletable sibling is gone regardless of the failure.
  if _, err := os.Stat(filepath.Join(src, "good.txt")); !os.IsNotExist(err) {
    t.Errorf("good.txt should have been deleted: %v", err)
  }
  // The undeletable subtree remains.
  if _, err := os.Stat(filepath.Join(src, "bad")); err != nil {
    t.Errorf("bad/ should remain after the failed delete: %v", err)
  }

  // The error is the aggregated deleteError listing the failing path.
  var dErr *deleteError
  if !errors.As(runErr, &dErr) {
    t.Fatalf("DeleteRun err = %T (%v), want *deleteError", runErr, runErr)
  }
  // good.txt (1 item) + bad/ (1) + bad/inner.txt (1) = 3 total items; two
  // operations failed (removing inner.txt, then rmdir bad).
  if dErr.total != 3 {
    t.Errorf("aggregated total = %d, want 3", dErr.total)
  }
  if dErr.failed != 2 {
    t.Errorf("aggregated failed = %d, want 2", dErr.failed)
  }
  if !strings.Contains(runErr.Error(), filepath.Join(src, "bad")) {
    t.Errorf("aggregated error %q should list the failing path %q", runErr.Error(), filepath.Join(src, "bad"))
  }

  // Progress reflects only what was actually removed: just good.txt.
  snap := prog.Snapshot()
  if snap.FilesTotal != 3 {
    t.Errorf("files total = %d, want 3", snap.FilesTotal)
  }
  if snap.FilesDone != 1 {
    t.Errorf("files done = %d, want 1 (failed entries are excluded)", snap.FilesDone)
  }
}
