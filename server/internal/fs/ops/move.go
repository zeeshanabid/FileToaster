package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// MoveErrEmptyPaths, MoveErrEmptyDest, MoveErrSelfMove and MoveErrOverlapping
// are the sentinels surfaced by MoveResolve. They are prefixed ErrMove to avoid
// clashing with copy's differently shaped errors in the shared ops package.
var (
	ErrMoveEmptyPaths = errors.New("at least one path is required")
	ErrMoveEmptyDest  = errors.New("a destination path is required")
	// ErrSelfMove is returned when a source resolves onto its own path (moving a
	// file onto itself) or a directory is moved into one of its own paths.
	ErrSelfMove = errors.New("cannot move a path onto itself or into one of its own paths")
	// ErrOverlappingSources is returned when the selection includes a path inside
	// another selected path: the first move would relocate the second's source.
	ErrOverlappingSources = errors.New("selection includes a path inside another selected path")
)

// MoveItem is one resolved source→destination move. Size is the recursive
// on-disk byte count of the source, measured up front so the progress bar shows
// a real total. It is not serialized to the task meta (marked "-").
type MoveItem struct {
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Size int64  `json:"-"`
}

// MovePlan is a validated, resolved set of moves ready to execute.
type MovePlan struct {
	// Items are the source→destination pairs, in request order.
	Items []MoveItem `json:"items"`
}

// MoveResolve validates the request, resolves every source and the destination
// against the allowed roots, and returns a ready-to-execute plan.
//
// Destination semantics are an in-place overwrite (a file manager's cut/paste):
//   - The destination is always treated as a directory: files and directories
//     are moved into it. A file keeps its basename when pasted.
//   - A destination that resolves to a path inside a source is rejected with
//     ErrSelfMove, as is a destination equal to the source (moving a file onto
//     itself, e.g. pasting a file into its own folder).
//   - Collisions are never renamed: the computed dst is the overwrite target as
//     is, and MoveRun replaces any existing item of the same name.
//   - A selection that contains a path inside another selected path is rejected
//     with ErrOverlappingSources (moving a parent relocates its descendants).
func MoveResolve(roots *fs.Roots, paths []string, dest string) (*MovePlan, error) {
	paths = uniqueTrim(paths)
	if len(paths) == 0 {
		return nil, ErrMoveEmptyPaths
	}
	if strings.TrimSpace(dest) == "" {
		return nil, ErrMoveEmptyDest
	}

	destStat, destStatErr := os.Lstat(dest)
	destPath, err := resolveDestPath(roots, dest, destStat, destStatErr)
	if err != nil {
		return nil, err
	}

	// Resolve every source up front so the overlapping-guard can compare the
	// resolved paths before any destination is computed.
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		src, err := roots.Resolve(p)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(src); err != nil {
			return nil, err
		}
		resolved = append(resolved, src)
	}

	// Defensive: moving a parent and one of its descendants together would have
	// the second move start from a source the first move already relocated.
	for i := 0; i < len(resolved); i++ {
		for j := 0; j < len(resolved); j++ {
			if i == j {
				continue
			}
			if isInside(resolved[i], resolved[j]) {
				return nil, fmt.Errorf("%w", ErrOverlappingSources)
			}
		}
	}

	plan := &MovePlan{}
	for k, src := range resolved {
		dst := filepath.Join(destPath, filepath.Base(src))
		if dst == src {
			return nil, fmt.Errorf("%w: %q", ErrSelfMove, paths[k])
		}
		if isInside(dst, src) {
			return nil, fmt.Errorf("%w: %q", ErrSelfMove, paths[k])
		}
		plan.Items = append(plan.Items, MoveItem{Src: src, Dst: dst})
	}
	return plan, nil
}

// MoveRun executes the plan, moving every item recursively and reporting
// progress through prog. The progress total is measured up front with an
// upfront statTarget walk so the bar shows a real byte total; FilesTotal is the
// number of top-level selected items (each completes with a single AddFile).
//
// Failures are captured, not fatal: if one item cannot be moved the rest of the
// selection still moves and every failure is returned together as an aggregated
// moveError (task → failed). A cancelled context (ctx.Err) aborts the whole walk
// and is returned immediately, shadowing any captured errors (task → cancelled).
//
// Same-filesystem moves use a native rename-overwrite (a directory destination
// is replaced with a temp-swap); a cross-filesystem source is copied to the
// destination and then the source removed.
func MoveRun(ctx context.Context, plan *MovePlan, prog *task.Progression) error {
	filesTotal := len(plan.Items)
	var totalBytes int64
	for _, it := range plan.Items {
		size, _, err := statTarget(it.Src)
		if err != nil {
			return err
		}
		it.Size = size
		totalBytes += size
	}
	prog.SetTotal(totalBytes, filesTotal)

	var errs []error
	for _, it := range plan.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := moveItem(ctx, it.Src, it.Dst, destDirOf(it.Dst), it.Size, prog, &errs); err != nil {
			return err
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &moveError{total: filesTotal, failed: len(errs), lines: errorLines(errs)}
}

// destDirOf returns the parent of the computed destination, i.e. the resolved
// destination folder the moved items were pasted into. It is the peer path used
// to decide whether source and destination share a filesystem.
func destDirOf(dst string) string {
	return filepath.Dir(dst)
}

// moveItem dispatches a single move by filesystem: a cross-filesystem (or
// undetermined-device) source is relocated with copy-then-delete, a same-fs
// source with a rename-overwrite. It returns a non-nil error only on context
// cancellation, so cancellation short-circuits the whole walk; ordinary
// failures are recorded into errs and the walk continues.
func moveItem(ctx context.Context, src, dst, dest string, size int64, prog *task.Progression, errs *[]error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	same, derr := sameDevice(src, dest)
	if derr != nil || !same {
		copyThenDelete(ctx, src, dst, prog, errs)
		return nil
	}
	return renameOverwrite(ctx, src, dst, size, prog, errs)
}

// renameOverwrite moves src onto dst in place when they share a filesystem. A
// directory destination is replaced with a temp-swap (dirSwap); a file or absent
// destination is overwritten with a single atomic rename. On any failure that
// leaves src intact it falls back to copyThenDelete and records only the fallback
// outcome, so a partially failed task can still relocate an item.
func renameOverwrite(ctx context.Context, src, dst string, size int64, prog *task.Progression, errs *[]error) error {
	info, lerr := os.Lstat(dst)
	if lerr != nil {
		if !os.IsNotExist(lerr) {
			*errs = append(*errs, fmt.Errorf("%s -> %s: %w", src, dst, lerr))
			return nil
		}
		// dst is absent: create the target with an atomic rename.
	} else if info.IsDir() {
		if err := dirSwap(src, dst, size, prog); err != nil {
			*errs = append(*errs, fmt.Errorf("%s -> %s: %w", src, dst, err))
			return nil
		}
		return nil
	}
	if err := os.Rename(src, dst); err != nil {
		// rename is atomic, so src is intact; copy can still relocate it.
		copyThenDelete(ctx, src, dst, prog, errs)
		return nil
	}
	prog.AddBytes(size)
	prog.AddFile()
	return nil
}

// dirSwap replaces an existing destination directory with src without ever left
// dst missing mid-operation: src is moved aside to a hidden temp file in dst's
// parent, the old dst is removed, then src is renamed into place. On failure the
// temp is left in place (recoverable: it still holds src). On success it reports
// the item's bytes and file count.
func dirSwap(src, dst string, size int64, prog *task.Progression) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".movetmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file; remove it so the name is free for the rename.
	if err := os.Remove(tmpName); err != nil {
		return err
	}
	if err := os.Rename(src, tmpName); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		// RemoveAll failed (e.g., read-only directory): roll back by renaming
		// the temp back to the original source location so src is intact.
		_ = os.Rename(tmpName, src)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	prog.AddBytes(size)
	prog.AddFile()
	return nil
}

// copyThenDelete relocates src across filesystems: it recursively copies src to
// dst reporting bytes only (the item-level AddFile stays correct), then removes
// the source. If dst already exists (file or directory) it is cleared first so
// the copy does not merge into or collide with it. A mid-copy failure records
// the error and leaves the source intact; a post-copy source-removal failure
// records the error too.
func copyThenDelete(ctx context.Context, src, dst string, prog *task.Progression, errs *[]error) error {
	if _, err := os.Lstat(dst); err == nil {
		if err := os.RemoveAll(dst); err != nil {
			*errs = append(*errs, fmt.Errorf("%s: %w", src, err))
			return nil
		}
	}
	var copyErr error
	info, lerr := os.Lstat(src)
	if lerr != nil {
		copyErr = lerr
	} else if info.IsDir() {
		copyErr = copyDirBytes(ctx, src, dst, prog)
	} else {
		copyErr = copyFileBytes(ctx, src, dst, info, prog, make([]byte, copyBufferSize))
	}
	if copyErr != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", src, copyErr))
		return nil
	}
	prog.AddFile()
	if err := os.RemoveAll(src); err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", src, err))
	}
	return nil
}

// copyDirBytes recursively copies dir into dst, recreating the structure and
// copying every regular file with byte progress only (no AddFile): the item's
// completion is reported once by the caller so the per-item counter stays exact.
func copyDirBytes(ctx context.Context, src, dst string, prog *task.Progression) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	buf := make([]byte, copyBufferSize)
	for _, de := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s2 := filepath.Join(src, de.Name())
		d2 := filepath.Join(dst, de.Name())
		i2, err := de.Info()
		if err != nil {
			continue
		}
		if de.IsDir() {
			if err := copyDirBytes(ctx, s2, d2, prog); err != nil {
				return err
			}
			continue
		}
		if err := copyFileBytes(ctx, s2, d2, i2, prog, buf); err != nil {
			return err
		}
	}
	return nil
}

// copyFileBytes copies a single regular file reporting bytes only (no AddFile).
func copyFileBytes(ctx context.Context, src, dst string, info os.FileInfo, prog *task.Progression, buf []byte) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			prog.AddBytes(int64(n))
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return rerr
		}
	}
	_ = out.Sync()
	return nil
}

// sameDevice reports whether a and b live on the same filesystem by comparing
// their device numbers. It relies on the unix Sys().(*syscall.Stat_t), matching
// the codebase's unix-only assumptions; a stat failure is returned so the caller
// can fall back to copy-then-delete.
func sameDevice(a, b string) (bool, error) {
	ai, aerr := os.Lstat(a)
	if aerr != nil {
		return false, aerr
	}
	bi, berr := os.Lstat(b)
	if berr != nil {
		return false, berr
	}
	aiSys, ok1 := ai.Sys().(*syscall.Stat_t)
	biSys, ok2 := bi.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false, fmt.Errorf("cannot compare device of %q and %q", a, b)
	}
	return aiSys.Dev == biSys.Dev, nil
}

// moveError aggregates every per-item failure of MoveRun so the task reports all
// of them at once. It needs no errors.Is matching: a Run error bypasses the HTTP
// status mapping and lands verbatim in the task's Error field.
type moveError struct {
	total  int
	failed int
	lines  []string
}

func (e *moveError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "failed to move %d of %d items:", e.failed, e.total)
	for _, l := range e.lines {
		b.WriteByte('\n')
		b.WriteString(l)
	}
	return b.String()
}
