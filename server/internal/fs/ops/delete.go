package ops

import (
  "context"
  "errors"
  "fmt"
  "os"
  "path/filepath"
  "strings"

  "filetoaster/internal/fs"
  "filetoaster/internal/task"
)

// DeleteErrEmptyPaths and DeleteErrRootPath are the sentinels surfaced by
// DeleteResolve. They are distinct from copy's ErrEmptyPaths (a differently
// shaped operation) to avoid name reuse in the shared ops package.
var (
  DeleteErrEmptyPaths = errors.New("at least one path is required")
  // DeleteErrRootPath is returned when a resolved path equals one of the
  // configured allowed roots: defense-in-depth against deleting the served
  // folder itself.
  DeleteErrRootPath = errors.New("cannot delete an allowed folder")
)

// DeletePlan is a validated, resolved set of paths ready to be deleted
// recursively. It is the delete operation's analog of copy's Plan but carries
// only the paths (there is no destination, so no conflict handling).
type DeletePlan struct {
  // Paths are the resolved source paths, in request order.
  Paths []string `json:"paths"`
}

// DeleteResolve validates the request, resolves every path against the allowed
// roots, and returns a ready-to-execute plan. Every path is canonicalized and
// confined to the roots (a traversal/symlink/absolute escape returns
// fs.ErrOutsideRoots); a path that resolves to one of the allowed roots is
// rejected with DeleteErrRootPath.
func DeleteResolve(roots *fs.Roots, paths []string) (*DeletePlan, error) {
  paths = uniqueTrim(paths)
  if len(paths) == 0 {
    return nil, DeleteErrEmptyPaths
  }
  allowed := roots.Allowed()
  plan := &DeletePlan{}
  for _, p := range paths {
    resolved, err := roots.Resolve(p)
    if err != nil {
      return nil, err
    }
    if isAllowedRoot(resolved, allowed) {
      return nil, fmt.Errorf("%w: %q", DeleteErrRootPath, p)
    }
    plan.Paths = append(plan.Paths, resolved)
  }
  return plan, nil
}

// isAllowedRoot reports whether resolved equals any configured allowed root.
func isAllowedRoot(resolved string, allowed []string) bool {
  for _, r := range allowed {
    if resolved == r {
      return true
    }
  }
  return false
}

// Run executes the plan, removing every path recursively and reporting progress
// through prog. The progress total is measured up front with an upfront stat
// walk (countTree) so the bar shows a real files+bytes total; the recursive walk
// reports each entry as it is removed so the bar advances incrementally.
//
// Failures are captured, not fatal: if one entry cannot be deleted the rest of
// the selection is still removed and every failure is returned together as an
// aggregated deleteError (task → failed). A cancelled context (ctx.Err) aborts
// the whole walk and is returned immediately, shadowing any captured errors
// (task → cancelled).
func DeleteRun(ctx context.Context, plan *DeletePlan, prog *task.Progression) error {
  var totalBytes int64
  var itemsTotal int
  for _, p := range plan.Paths {
    bytes, items, err := countTree(p)
    if err != nil {
      return err
    }
    totalBytes += bytes
    itemsTotal += items
  }
  prog.SetTotal(totalBytes, itemsTotal)

  var errs []error
  for _, p := range plan.Paths {
    if ctx.Err() != nil {
      return ctx.Err()
    }
    if err := deleteItem(ctx, p, prog, &errs); err != nil {
      return err
    }
  }
  if len(errs) == 0 {
    return nil
  }
  return &deleteError{total: itemsTotal, failed: len(errs), lines: errorLines(errs)}
}

// countTree returns the on-disk byte size (sum of file sizes) and the recursive
// count of files + directories under path. It is the upfront measurement that
// lets the progress bar show a real total and makes the counter reflect
// "files and directories".
func countTree(path string) (bytes int64, items int, err error) {
  info, err := os.Lstat(path)
  if err != nil {
    return 0, 0, err
  }
  if !info.IsDir() {
    return info.Size(), 1, nil
  }
  items = 1 // the directory itself
  entries, err := os.ReadDir(path)
  if err != nil {
    return 0, 0, err
  }
  for _, de := range entries {
    i, err := de.Info()
    if err != nil {
      continue
    }
    if de.IsDir() {
      s, f, err := countTree(filepath.Join(path, de.Name()))
      if err != nil {
        return 0, 0, err
      }
      bytes += s
      items += f
    } else {
      bytes += i.Size()
      items++
    }
  }
  return bytes, items, nil
}

// deleteItem removes a single path (file or directory), reporting progress per
// entry and capturing per-entry failures into errs. It returns a non-nil error
// only when the context is cancelled, so the cancellation short-circuits the
// whole walk; ordinary failures are recorded into errs and the walk continues.
func deleteItem(ctx context.Context, path string, prog *task.Progression, errs *[]error) error {
  if ctx.Err() != nil {
    return ctx.Err()
  }
  info, err := os.Lstat(path)
  if err != nil {
    *errs = append(*errs, fmt.Errorf("%s: %w", path, err))
    return nil
  }
  if info.IsDir() {
    return deleteDir(ctx, path, prog, errs)
  }
  if err := os.RemoveAll(path); err != nil {
    *errs = append(*errs, fmt.Errorf("%s: %w", path, err))
    return nil
  }
  prog.AddBytes(info.Size())
  prog.AddFile()
  return nil
}

// deleteDir recursively removes dir's children, then removes dir itself.
// Nested per-entry failures are captured and the walk over the remaining
// siblings continues; the directory is only counted (AddFile) once it is removed.
func deleteDir(ctx context.Context, dir string, prog *task.Progression, errs *[]error) error {
  if ctx.Err() != nil {
    return ctx.Err()
  }
  entries, err := os.ReadDir(dir)
  if err != nil {
    *errs = append(*errs, fmt.Errorf("%s: %w", dir, err))
    return nil
  }
  for _, de := range entries {
    if ctx.Err() != nil {
      return ctx.Err()
    }
    if err := deleteItem(ctx, filepath.Join(dir, de.Name()), prog, errs); err != nil {
      return err
    }
  }
  if err := os.Remove(dir); err != nil {
    *errs = append(*errs, fmt.Errorf("%s: %w", dir, err))
    return nil
  }
  prog.AddFile()
  return nil
}

// deleteError aggregates every per-entry failure of a Run so the task reports
// all of them at once. It needs no errors.Is matching: a Run error bypasses the
// HTTP status mapping and lands verbatim in the task's Error field.
type deleteError struct {
  total  int
  failed int
  lines  []string
}

func (e *deleteError) Error() string {
  var b strings.Builder
  fmt.Fprintf(&b, "failed to delete %d of %d items:", e.failed, e.total)
  for _, l := range e.lines {
    b.WriteByte('\n')
    b.WriteString(l)
  }
  return b.String()
}

// errorLines renders each captured error as a "path: reason" line, preserving
// the order in which the failures were recorded.
func errorLines(errs []error) []string {
  lines := make([]string, 0, len(errs))
  for _, err := range errs {
    lines = append(lines, err.Error())
  }
  return lines
}
