// Package ops holds the fileset operations the task engine runs. Each operation
// is a pure function over resolved paths so it can be unit-tested without the
// network or the pool. Copy is the first operation; others (move, delete, …)
// follow the same shape.
package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// copyBufferSize is the per-file copy buffer. 8 MiB keeps large-file copying
// throughput-bound rather than syscalls-bound without wasting memory per worker.
const copyBufferSize = 8 << 20

// Item is one resolved source→destination copy.
type Item struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// Plan is a validated, resolved set of copies ready to execute.
type Plan struct {
	// Items are the source→destination pairs, in request order.
	Items []Item `json:"items"`
	// Conflict carries the (now unused) client overwrite decision; it is kept
	// for schema compatibility and always serializes to "".
	Conflict string `json:"conflict"`
}

// ErrEmptyPaths, ErrEmptyDest and ErrDestIsFile are the sentinels surfaced by
// Resolve.
var (
	ErrEmptyPaths = errors.New("at least one path is required")
	ErrEmptyDest  = errors.New("a destination path is required")
	ErrDestIsFile = errors.New("destination is a file; copy to a directory")
	// ErrSelfCopy is returned when the destination lies inside the source: a
	// directory cannot be copied into one of its own paths.
	ErrSelfCopy = errors.New("cannot copy a path into one of its own paths")
)

// Resolve validates the request, resolves every source and the destination
// against the allowed roots, and returns a ready-to-execute Plan.
//
// Destination semantics mirror a file manager's paste:
//   - The destination is always treated as a directory: both files and
//     directories are copied into it. A destination that resolves to an existing
//     file is rejected with ErrDestIsFile.
//   - A file keeps its basename when pasted.
//   - A single selected directory is recreated under its own basename inside
//     the destination folder (pkg pasted into /root/ becomes /root/pkg), so its
//     contents never spill into the destination. When that computed target
//     already exists — for example pasting a directory directly into its own
//     parent folder, where the basename lands back on the source path — the
//     copy is renamed ("sub (copy)", …) so the original survives.
//   - When several items are selected each directory is nested under its own
//     basename to avoid collapsing them.
//   - A directory copied into a path inside itself (but not back onto its own
//     parent folder) is rejected with ErrSelfCopy.
//   - If a computed destination already exists, it is renamed (" (copy)", then
//     " (copy) 1", " (copy) 2", …) so existing files are never clobbered. No
//     overwrite prompt is shown.
func Resolve(roots *fs.Roots, paths []string, dest string) (*Plan, error) {
	paths = uniqueTrim(paths)
	if len(paths) == 0 {
		return nil, ErrEmptyPaths
	}
	if strings.TrimSpace(dest) == "" {
		return nil, ErrEmptyDest
	}

	destStat, destStatErr := os.Lstat(dest)
	destPath, err := resolveDestPath(roots, dest, destStat, destStatErr)
	if err != nil {
		return nil, err
	}

	single := len(paths) == 1
	plan := &Plan{}
	for _, p := range paths {
		src, err := roots.Resolve(p)
		if err != nil {
			return nil, err
		}
		srcInfo, statErr := os.Lstat(src)
		if statErr != nil {
			return nil, statErr
		}

		item, err := planItemFor(src, srcInfo, destPath, destStat, destStatErr, single)
		if err != nil {
			return nil, err
		}
		if item.Dst == src {
			// The destination resolved to the source's own path. This happens when
			// a directory is pasted directly into its own parent folder, so its
			// basename lands back on the source. Rather than clobbering the original
			// in place, copy it under a distinguishing name so both the source and
			// its copy survive (e.g. sub copied into its parent becomes sub (copy)).
			item.Dst = withCopyName(item.Dst)
		} else if isInside(item.Dst, src) {
			return nil, fmt.Errorf("%w: %q", ErrSelfCopy, p)
		}
		// Every item targets a concrete path; if one already exists there, rename
		// it (" (copy)", then " (copy) 1", …) instead of clobbering, so no copy
		// ever merges its contents into an existing folder.
		item.Dst = uniqueDest(item.Dst)
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}

// planItemFor computes the destination for one source under a resolved
// destination. Both files and directories keep their basename when pasted into
// the destination folder, so a directory is never collapsed into its target.
// A destination that is an existing file is not a valid paste target.
func planItemFor(src string, srcInfo os.FileInfo, destPath string, destStat os.FileInfo, destStatErr error, single bool) (Item, error) {
	destExists := destStatErr == nil
	destIsDir := destExists && destStat.IsDir()

	switch {
	case !destExists || destIsDir:
		// The destination is a folder (fresh or existing); the selection is
		// pasted into it.
		if srcInfo.IsDir() {
			if !single {
				// Multiple selections: keep each directory under its own name
				// so they don't collapse into one another.
				return Item{Src: src, Dst: filepath.Join(destPath, filepath.Base(src))}, nil
			}
			// A single directory is recreated under its own basename inside the
			// destination folder (pkg pasted into /root/ becomes /root/pkg), so its
			// contents never spill into the destination. Pasted directly into its
			// own parent folder the basename lands back on the source path and is
			// renamed ("sub (copy)") below rather than merging contents into the
			// parent.
			return Item{Src: src, Dst: filepath.Join(destPath, filepath.Base(src))}, nil
		}
		// A file keeps its basename when pasted into the destination folder.
		return Item{Src: src, Dst: filepath.Join(destPath, filepath.Base(src))}, nil
	default:
		// The destination is an existing file: a file is not a valid paste target.
		return Item{}, ErrDestIsFile
	}
}

// resolveDestPath canonicalizes the destination and confines it to the roots.
// A non-existing destination is only cleaned (and containment-checked) rather
// than stat'd, so it can be created on copy.
func resolveDestPath(roots *fs.Roots, dest string, destStat os.FileInfo, destStatErr error) (string, error) {
	switch {
	case destStatErr == nil:
		canon, rerr := roots.Resolve(dest)
		if rerr != nil {
			return "", rerr
		}
		return canon, nil
	case os.IsNotExist(destStatErr):
		clean := filepath.Clean(dest)
		if !roots.Contained(clean) {
			return "", fs.ErrOutsideRoots
		}
		return clean, nil
	default:
		return "", destStatErr
	}
}

// isInside reports whether child equals parent or is nested under it. Used to
// stop a copy from targeting a path inside its own source.
func isInside(child, parent string) bool {
	if child == parent {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// uniqueDest returns base if it does not already exist, otherwise a path with a
// " (copy)" suffix, then " (copy) 1", " (copy) 2", …, that does not exist, so a
// copy never overwrites an existing file or directory.
func uniqueDest(base string) string {
	if _, err := os.Stat(base); err != nil {
		return base
	}
	if _, err := os.Stat(withCopyName(base)); err != nil {
		return withCopyName(base)
	}
	n := 1
	for {
		candidate := numberedCopyName(base, n)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
		n++
	}
}

// withCopyName returns path with " (copy)" inserted before the file extension so
// a copy never clobbers the original at the same path (e.g. a.txt ->
// "a (copy).txt", Makefile -> "Makefile (copy)").
func withCopyName(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return filepath.Join(dir, base[:len(base)-len(ext)]+" (copy)"+ext)
}

// numberedCopyName returns base with " (copy) N" inserted before the extension,
// e.g. a.txt -> "a (copy) 1.txt", Makefile -> "Makefile (copy) 1".
func numberedCopyName(base string, n int) string {
	dir := filepath.Dir(base)
	name := filepath.Base(base)
	ext := filepath.Ext(name)
	return filepath.Join(dir, name[:len(name)-len(ext)]+" (copy) "+strconv.Itoa(n)+ext)
}

// Run executes the plan, reporting progress through prog. Existing targets are
// renamed rather than overwritten (Resolve already picks unique names for every
// item), so no copy overwrites an existing file or directory. It returns a
// context error when cancelled mid-copy.
func Run(ctx context.Context, plan *Plan, prog *task.Progression) error {
	if len(plan.Items) == 0 {
		return errors.New("no items to copy")
	}
	var totalBytes int64
	var files int
	for _, it := range plan.Items {
		size, f, err := statTarget(it.Src)
		if err != nil {
			return err
		}
		totalBytes += size
		files += f
	}
	prog.SetTotal(totalBytes, files)

	for _, it := range plan.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := copyItem(ctx, it.Src, it.Dst, prog); err != nil {
			return err
		}
	}
	return nil
}

// statTarget returns the on-disk byte size and file count of path, recursing into
// directories. It is the upfront measurement that lets the progress bar show a
// real total.
func statTarget(path string) (bytes int64, files int, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	if !info.IsDir() {
		return info.Size(), 1, nil
	}
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
			s, f, err := statTarget(filepath.Join(path, de.Name()))
			if err != nil {
				return 0, 0, err
			}
			bytes += s
			files += f
		} else {
			bytes += i.Size()
			files++
		}
	}
	return bytes, files, nil
}

// copyItem copies a single source (file or directory) to dst.
func copyItem(ctx context.Context, src, dst string, prog *task.Progression) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(ctx, src, dst, prog)
	}
	buf := make([]byte, copyBufferSize)
	return copyFile(ctx, src, dst, info, prog, buf)
}

// copyDir recursively copies dir into dst, recreating the directory structure
// and copying every regular file with progress reporting. dst is already a
// unique path chosen by Resolve, so no child is ever overwritten.
func copyDir(ctx context.Context, src, dst string, prog *task.Progression) error {
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
		switch {
		case de.IsDir():
			if err := copyDir(ctx, s2, d2, prog); err != nil {
				return err
			}
		default:
			if err := copyFile(ctx, s2, d2, i2, prog, buf); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFile copies a single regular file to dst and records one completed file in
// prog. Bytes are reported as each buffer is copied so the progress bar and
// transfer speed update while the file is mid-copy, instead of snapping 0% ->
// 100% only after io.CopyBuffer returns.
func copyFile(ctx context.Context, src, dst string, info os.FileInfo, prog *task.Progression, buf []byte) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), info.Mode().Perm()); err != nil {
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
	prog.AddFile()
	return nil
}

// uniqueTrim returns the trimmed, de-duplicated paths preserving order.
func uniqueTrim(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		c := filepath.Clean(p)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}
