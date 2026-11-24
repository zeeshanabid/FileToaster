package upload

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// FinalizeMeta is carried in the finalize task's Meta field so the task label
// can count the uploaded files. It is opaque to the engine; the client's
// taskLabel reads files (and phase) off it.
type FinalizeMeta struct {
	Staging string `json:"staging"`
	Dest    string `json:"dest"`
	Phase   string `json:"phase"`
	Files   int    `json:"files"`
}

// finalizeBufferSize is the per-file copy buffer during finalize (staging →
// target). 8 MiB keeps large-file copies throughput-bound without wasting
// memory per worker.
const finalizeBufferSize = 8 << 20

// FinalizeConfig holds configuration for the finalize operation.
type FinalizeConfig struct {
	// BufferSize is the per-file finalize buffer size in bytes.
	BufferSize int64
}

// DefaultFinalizeConfig returns the default finalize configuration.
func DefaultFinalizeConfig() *FinalizeConfig {
	return &FinalizeConfig{
		BufferSize: finalizeBufferSize,
	}
}

// Finalize copies or moves the staged tree at stagingRoot into dest, recreating empty
// directories and giving each copied file a unique destination (a colliding
// nested file is renamed " (copy)", " (copy) 1", …) so nothing at the target is
// clobbered. dest must already be confined to the allowed roots (checked in the
// op's Prepare). Progress is reported through prog.
// Uses fast rename when staging and dest are on same filesystem; falls back to
// copy-then-delete across filesystems. On success the staging dir is removed
// (consumed); on failure it is kept so the copy can be retried (the age sweep
// later reclaims it). A ctx cancellation aborts mid-copy.
func Finalize(ctx context.Context, stagingRoot, dest string, prog *task.Progression, cfg *FinalizeConfig) error {
	// Check if we can use fast rename (same filesystem)
	sameDev, _ := sameDevice(stagingRoot, dest)

	// Collect all files and empty dirs to process
	var files []string
	var emptyDirs []string
	err := filepath.WalkDir(stagingRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stagingRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			// Check if empty
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				emptyDirs = append(emptyDirs, path)
			}
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}

	// Calculate totals
	var totalBytes int64
	for _, item := range files {
		info, err := os.Stat(item)
		if err != nil {
			return err
		}
		totalBytes += info.Size()
	}
	prog.SetTotal(totalBytes, len(files))

	// Process each file
	for _, src := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(stagingRoot, src)
		if err != nil {
			return err
		}
		target := filepath.Clean(filepath.Join(dest, rel))
		if target != dest && !strings.HasPrefix(target, dest+string(filepath.Separator)) {
			return fs.ErrOutsideRoots
		}

		dst := uniqueDest(target)

		if sameDev {
			// Fast path: atomic rename
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Rename(src, dst); err != nil {
				return err
			}
			info, _ := os.Stat(dst)
			prog.AddBytes(info.Size())
			prog.AddFile()
		} else {
			// Cross-filesystem: copy then delete source
			if err := copyFileTo(ctx, src, dst, prog, cfg); err != nil {
				return err
			}
			// Only delete source after successful copy
			if err := os.Remove(src); err != nil {
				return err
			}
			prog.AddFile()
		}
	}

	// Recreate empty directories at destination
	for _, src := range emptyDirs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(stagingRoot, src)
		if err != nil {
			return err
		}
		target := filepath.Clean(filepath.Join(dest, rel))
		if target != dest && !strings.HasPrefix(target, dest+string(filepath.Separator)) {
			return fs.ErrOutsideRoots
		}
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
			return err
		}
	}

	// Remove empty staging directories
	return removeEmptyDirs(stagingRoot)
}

// sameDevice reports whether a and b live on the same filesystem.
// It uses a cross-platform approach: attempt to create a hard link between
// the two directories. If it succeeds, they're on the same filesystem.
// If it fails with EXDEV (cross-device link), they're on different filesystems.
// Falls back to assuming different devices on error.
func sameDevice(a, b string) (bool, error) {
	// Ensure both paths exist and are directories
	ai, aerr := os.Lstat(a)
	if aerr != nil {
		return false, aerr
	}
	bi, berr := os.Lstat(b)
	if berr != nil {
		return false, berr
	}
	if !ai.IsDir() {
		a = filepath.Dir(a)
	}
	if !bi.IsDir() {
		b = filepath.Dir(b)
	}

	// Create a temp file in a
	tmp, err := os.CreateTemp(a, ".samedevice-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	tmp.Close()

	// Try to hard link it into b
	linkName := filepath.Join(b, filepath.Base(tmpName))
	err = os.Link(tmpName, linkName)
	if err == nil {
		// Success: same filesystem
		os.Remove(linkName)
		os.Remove(tmpName)
		return true, nil
	}

	// Check if it's a cross-device error
	if errors.Is(err, os.ErrInvalid) || errors.Is(err, syscall.EXDEV) {
		os.Remove(tmpName)
		return false, nil
	}

	// Other error: clean up and return error
	os.Remove(tmpName)
	return false, err
}

// removeEmptyDirs removes empty directories from the bottom up.
func removeEmptyDirs(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Remove in reverse order (deepest first) so parent dirs become empty after children are removed
	for i := len(dirs) - 1; i >= 0; i-- {
		entries, err := os.ReadDir(dirs[i])
		if err != nil {
			// Directory might have been removed already
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if len(entries) == 0 {
			os.Remove(dirs[i])
		}
	}
	return nil
}

// copyFileTo copies src to dst, reporting bytes as each buffer is transferred.
func copyFileTo(ctx context.Context, src, dst string, prog *task.Progression, cfg *FinalizeConfig) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, cfg.BufferSize)
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
	if syncErr := out.Sync(); syncErr != nil {
		out.Close()
		return syncErr
	}
	return out.Close()
}

// uniqueDest returns base if it does not already exist, otherwise a path with a
// " (copy)" suffix, then " (copy) 1", " (copy) 2", …, that does not exist, so a
// finalized file never overwrites an existing file at the target.
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

func withCopyName(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return filepath.Join(dir, base[:len(base)-len(ext)]+" (copy)"+ext)
}

func numberedCopyName(base string, n int) string {
	dir := filepath.Dir(base)
	name := filepath.Base(base)
	ext := filepath.Ext(name)
	return filepath.Join(dir, name[:len(name)-len(ext)]+" (copy) "+strconv.Itoa(n)+ext)
}
