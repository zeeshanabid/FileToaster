package upload

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// Finalize copies the staged tree at stagingRoot into dest, recreating empty
// directories and giving each copied file a unique destination (a colliding
// nested file is renamed " (copy)", " (copy) 1", …) so nothing at the target is
// clobbered. dest must already be confined to the allowed roots (checked in the
// op's Prepare). Progress is reported through prog. On success the staging dir is
// removed (consumed); on failure it is kept so the copy can be retried (the age
// sweep later reclaims it). A ctx cancellation aborts mid-copy.
func Finalize(ctx context.Context, stagingRoot, dest string, prog *task.Progression) error {
	totalBytes, fileCount, err := countStaging(stagingRoot)
	if err != nil {
		return err
	}
	prog.SetTotal(totalBytes, fileCount)

	err = filepath.WalkDir(stagingRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(stagingRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Clean(filepath.Join(dest, rel))
		// rel is a clean descent under stagingRoot, so target is always within
		// dest; this guard makes that invariant explicit and defensive.
		if target != dest && !strings.HasPrefix(target, dest+string(filepath.Separator)) {
			return fs.ErrOutsideRoots
		}
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFileTo(ctx, path, uniqueDest(target), prog)
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(stagingRoot)
}

// countStaging walks stagingRoot and returns the total byte size and file count
// so the progress bar has a real total up front.
func countStaging(root string) (int64, int, error) {
	var bytes int64
	var files int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files, err
}

// copyFileTo copies src to dst, reporting bytes as each buffer is transferred,
// then recording one completed file.
func copyFileTo(ctx context.Context, src, dst string, prog *task.Progression) error {
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
	buf := make([]byte, finalizeBufferSize)
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
