// Package ops holds the fileset operations the task engine runs. Each operation
// is a pure function over resolved paths so it can be unit-tested without the
// network or the pool. Truncate follows the same shape but only modifies file
// sizes without changing file descriptors.
package ops

import (
	"context"
	"errors"
	"fmt"
	"os"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// truncateBufferSize is the buffer size used when rewriting file content.
// 8 MiB keeps large-file operations throughput-bound rather than syscalls-bound.
const truncateBufferSize = 8 << 20

// TruncateConfig holds configuration for the truncate operation.
type TruncateConfig struct {
	// BufferSize is the per-file buffer size in bytes.
	BufferSize int64
}

// DefaultTruncateConfig returns the default truncate configuration.
func DefaultTruncateConfig() *TruncateConfig {
	return &TruncateConfig{
		BufferSize: truncateBufferSize,
	}
}

// TruncateErrEmptyPaths is the sentinel surfaced when no path is selected.
var TruncateErrEmptyPaths = errors.New("at least one path is required")

// TruncateErrDirectorySelected is the sentinel surfaced when a directory is in the selection.
var TruncateErrDirectorySelected = errors.New("truncate operation only supports files, not directories")

// TruncateErrInvalidSize is the sentinel surfaced when the target size is invalid.
var TruncateErrInvalidSize = errors.New("target size must be a non-negative integer")

// TruncatePlan is a validated, resolved set of files ready to be truncated.
type TruncatePlan struct {
	// Items are the source files and their target sizes, in request order.
	Items []TruncateItem `json:"items"`
}

// TruncateItem is one resolved file with its target size.
type TruncateItem struct {
	Src string `json:"src"`
	// Size is the target size in bytes.
	Size int64 `json:"size"`
}

// TruncateResolve validates the request, resolves every path against the allowed
// roots, and returns a ready-to-execute plan. Only files are allowed (not directories).
func TruncateResolve(roots *fs.Roots, paths []string, size int64) (*TruncatePlan, error) {
	paths = uniqueTrim(paths)
	if len(paths) == 0 {
		return nil, TruncateErrEmptyPaths
	}
	if size < 0 {
		return nil, TruncateErrInvalidSize
	}

	plan := &TruncatePlan{}
	for _, p := range paths {
		resolved, err := roots.Resolve(p)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(resolved)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%w: %q", TruncateErrDirectorySelected, p)
		}
		plan.Items = append(plan.Items, TruncateItem{Src: resolved, Size: size})
	}
	return plan, nil
}

// TruncateRun executes the plan, truncating every file to the target size and
// reporting progress through prog. The progress total is the number of files.
// Uses the fastest method available: ftruncate on Unix, fallback to traditional
// file rewrite on other systems.
func TruncateRun(ctx context.Context, plan *TruncatePlan, prog *task.Progression, cfg *TruncateConfig) error {
	if len(plan.Items) == 0 {
		return errors.New("no items to truncate")
	}
	prog.SetTotal(int64(len(plan.Items)), len(plan.Items))

	for _, it := range plan.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := truncateFile(ctx, it.Src, it.Size, prog, cfg); err != nil {
			return err
		}
	}
	return nil
}

// truncateFile truncates a single file to the target size.
// Uses os.Truncate (which uses ftruncate on Unix) for the fastest method.
// Falls back to traditional file rewrite if the fast method fails.
func truncateFile(ctx context.Context, path string, size int64, prog *task.Progression, cfg *TruncateConfig) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Try the fastest method first: os.Truncate (uses ftruncate on Unix)
	// This does not change the file descriptor - it only changes the file size.
	err := os.Truncate(path, size)
	if err == nil {
		prog.AddBytes(size)
		prog.AddFile()
		return nil
	}

	// Fallback: traditional file rewrite (for systems where truncate may not work as expected)
	// This is rarely needed but provides compatibility.
	return truncateFileFallback(ctx, path, size, prog, cfg)
}

// truncateFileFallback rewrites the file content to achieve the target size.
// This is the fallback method when os.Truncate is not available or fails.
func truncateFileFallback(ctx context.Context, path string, size int64, prog *task.Progression, cfg *TruncateConfig) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	// If target size is larger than current, we need to extend with zeros
	// If target size is smaller, we truncate
	if size >= info.Size() {
		// Extend file with zeros
		return extendFile(ctx, path, size, info, prog, cfg)
	}

	// Truncate: open file and truncate at the target size
	file, err := os.OpenFile(path, os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer file.Close()

	if err := file.Truncate(size); err != nil {
		return err
	}

	prog.AddBytes(size)
	prog.AddFile()
	return nil
}

// extendFile extends a file to the target size by writing zeros.
func extendFile(ctx context.Context, path string, targetSize int64, info os.FileInfo, prog *task.Progression, cfg *TruncateConfig) error {
	file, err := os.OpenFile(path, os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer file.Close()

	// Seek to the end of the current file
	currentSize := info.Size()
	if _, err := file.Seek(currentSize, 0); err != nil {
		return err
	}

	// Write zeros to extend
	bytesToWrite := targetSize - currentSize
	buf := make([]byte, cfg.BufferSize)
	written := int64(0)

	for written < bytesToWrite {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		chunk := int64(len(buf))
		if written+chunk > bytesToWrite {
			chunk = bytesToWrite - written
		}
		if _, err := file.Write(buf[:chunk]); err != nil {
			return err
		}
		written += chunk
		prog.AddBytes(chunk)
	}

	prog.AddFile()
	return nil
}
