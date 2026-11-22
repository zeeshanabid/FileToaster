// Package ops holds the fileset operations the task engine runs. Each operation
// is a pure function over resolved paths so it can be unit-tested without the
// network or the pool. Decompress extracts a single archive (zip, tar, tar.gz)
// into its own folder. Like compress it never mutates its subject: the archive
// is opened read-only and only read, so the extracted output is always a sibling
// of the archive, never the archive itself.
package ops

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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

// The stdlib archive formats decompress can read (formatZip, formatTargz,
// formatTar) are declared alongside compress's writer set in compress.go; the
// extractor supports exactly the same formats. An unsupported or unknown format
// is rejected with DecompressUnsupportedFormat.

// decompressBufferSize is the per-file stream buffer for the extractor. It
// matches copy's and compress's buffer so extraction stays throughput-bound.
const decompressBufferSize = 8 << 20

// DecompressUnsupportedFormat is the sentinel surfaced when a request names a
// format the stdlib cannot read. Its message lists the accepted formats.
var DecompressUnsupportedFormat = errors.New("this file type is not supported; supported: zip, tar.gz, tar")

// DecompressMultiplePaths is returned when a request names more than one archive;
// extraction applies to a single file only.
var DecompressMultiplePaths = errors.New("decompress operates on a single archive file")

// DecompressArchiveTraversal is the zip-slip guard: an archive entry whose name
// escapes the extraction root ("..", an absolute path, or a joined path outside
// the root). Extraction containment is made impossible by rejecting it.
var DecompressArchiveTraversal = errors.New("archive entry escapes the destination folder")

// DecompressMeta is the serializable surface of a decompress plan shown in the
// task record. Archive is the resolved archive path; Format the detected format;
// ExtractDir the resolved extraction root; Into the requested destination
// ("", extract here, or the archive stem, extract into that folder).
type DecompressMeta struct {
	Format     string `json:"format"`
	Archive    string `json:"archive"`
	ExtractDir string `json:"extractDir"`
	Into       string `json:"into"`
}

// DecompressPlan is a validated, resolved archive and the folder to extract it
// into. It is the opaque spec passed to Run.
type DecompressPlan struct {
	Archive    string `json:"archive"`
	Format     string `json:"format"`
	ExtractDir string `json:"extractDir"`
}

// DecompressResolve validates the request, resolves the single archive against
// the allowed roots, and returns a ready-to-execute Plan.
//
// Extraction destination mirrors a file manager's "extract archive":
//   - `into == ""` extracts the contents directly into the archive's own folder
//     (extract here); `into != ""` extracts into a sibling folder named after
//     that value, created if missing.
//   - An extraction folder that already exists is renumbered (" (1)", " (2)", …)
//     rather than clobbered, so an existing folder is never merged into.
//   - The archive itself is never the extraction root and is opened read-only by
//     DecompressRun, so Resolve never writes or mutates anything.
func DecompressResolve(roots *fs.Roots, paths []string, into string) (*DecompressPlan, error) {
	paths = uniqueTrim(paths)
	if len(paths) == 0 {
		return nil, ErrEmptyPaths
	}
	if len(paths) > 1 {
		return nil, DecompressMultiplePaths
	}

	archive, err := roots.Resolve(paths[0])
	if err != nil {
		return nil, err
	}
	info, statErr := os.Lstat(archive)
	if statErr != nil {
		return nil, statErr
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: %q", DecompressUnsupportedFormat, filepath.Base(archive))
	}

	format := decompressFormat(filepath.Base(archive))
	if format == "" {
		return nil, DecompressUnsupportedFormat
	}

	base := filepath.Dir(archive)
	root := base
	if strings.TrimSpace(into) != "" {
		root = uniqueDir(filepath.Join(base, filepath.Clean(into)))
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
	}
	return &DecompressPlan{Archive: archive, Format: format, ExtractDir: root}, nil
}

// decompressFormat maps a file name to the archive format it denotes, matched on
// the lower-cased base name with precedence: tar.gz / tgz, then zip, then tar.
// Anything else (incl. standalone .gz, .bz2, .xz, .zst, .rar, .7z, no-ext)
// returns "". It is the one source of truth for the "supported" invariant shared
// with the client archive.ts.
func decompressFormat(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"):
		return formatTargz
	case strings.HasSuffix(lower, ".tgz"):
		return formatTargz
	case strings.HasSuffix(lower, ".zip"):
		return formatZip
	case strings.HasSuffix(lower, ".tar"):
		return formatTar
	default:
		return ""
	}
}

// uniqueDir returns base if it does not already exist, otherwise base renumbered
// " (1)", " (2)", … until a free name is found, so an extraction folder is never
// merged into an existing one. Unlike copy's " (copy)" convention it appends to
// the whole name.
func uniqueDir(base string) string {
	if _, err := os.Stat(base); err != nil {
		return base
	}
	n := 1
	for {
		candidate := filepath.Join(filepath.Dir(base), filepath.Base(base)+" ("+strconv.Itoa(n)+")")
		if _, serr := os.Stat(candidate); serr != nil {
			return candidate
		}
		n++
	}
}

// extractTarget resolves an archive entry name to its absolute destination under
// root and reports DecompressArchiveTraversal when the target is not contained
// in root. Absolute names and names built with ".." are rejected before they can
// escape, so extraction containment is impossible.
func extractTarget(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", DecompressArchiveTraversal, name)
	}
	dst := filepath.Join(root, filepath.Clean(name))
	if dst != root && !strings.HasPrefix(dst, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", DecompressArchiveTraversal, name)
	}
	return dst, nil
}

// DecompressRun executes the plan, opening the archive read-only and writing each
// entry into plan.ExtractDir, reporting progress through prog. The archive is
// never modified, renamed, or deleted. It returns a context error when cancelled
// mid-extraction, leaving surviving targets in place.
func DecompressRun(ctx context.Context, plan *DecompressPlan, prog *task.Progression) error {
	switch plan.Format {
	case formatZip:
		return runZip(ctx, plan.Archive, plan.ExtractDir, prog)
	case formatTargz:
		return runTar(ctx, plan.Archive, plan.ExtractDir, prog, true)
	case formatTar:
		return runTar(ctx, plan.Archive, plan.ExtractDir, prog, false)
	default:
		return DecompressUnsupportedFormat
	}
}

// runZip reads the zip archive read-only, measuring exact byte/file totals up
// front from the file headers, then writes each entry. Directory entries create
// their folders; regular entries are streamed with an 8 MiB buffer.
func runZip(ctx context.Context, archive, root string, prog *task.Progression) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return err
	}

	var totalBytes int64
	var filesTotal int
	for _, zf := range zr.File {
		if strings.HasSuffix(zf.Name, "/") {
			continue
		}
		filesTotal++
		totalBytes += int64(zf.UncompressedSize64)
	}
	prog.SetTotal(totalBytes, filesTotal)

	buf := make([]byte, decompressBufferSize)
	for _, zf := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeZipEntry(ctx, zf, root, prog, buf); err != nil {
			return err
		}
	}
	return nil
}

// writeZipEntry writes one zip entry: a directory entry creates its folder, a
// regular entry is streamed to its containment-checked destination.
func writeZipEntry(ctx context.Context, zf *zip.File, root string, prog *task.Progression, buf []byte) error {
	info := zf.FileInfo()
	mode := info.Mode().Perm()

	if strings.HasSuffix(zf.Name, "/") {
		dst, terr := extractTarget(root, zf.Name)
		if terr != nil {
			return terr
		}
		return os.MkdirAll(dst, mode)
	}

	dst, terr := extractTarget(root, zf.Name)
	if terr != nil {
		return terr
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if err := streamTo(ctx, out, rc, prog, buf); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	prog.AddFile()
	return nil
}

// runTar reads a tar or tar.gz archive read-only in two passes: the first counts
// regular-file entries and sums their sizes for accurate totals, the second
// streams each entry. Per-entry cancellation is checked before each entry.
func runTar(ctx context.Context, archive, root string, prog *task.Progression, gzipped bool) error {
	total, files, err := countTarEntries(archive, gzipped)
	if err != nil {
		return err
	}
	prog.SetTotal(total, files)

	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)

	buf := make([]byte, decompressBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case hdr.Typeflag == tar.TypeDir:
			dst, terr := extractTarget(root, hdr.Name)
			if terr != nil {
				return terr
			}
			if err := os.MkdirAll(dst, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		case hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA:
			dst, terr := extractTarget(root, hdr.Name)
			if terr != nil {
				return terr
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if err := streamTo(ctx, out, tr, prog, buf); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Sync(); err != nil {
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			prog.AddFile()
		default:
			// Skip non-regular, non-directory entries (symlinks, devices, …):
			// they carry no safe on-disk target and are not counted.
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return err
			}
		}
	}
}

// countTarEntries performs a read-only pass over the tar (optionally gzip) stream
// returning the number of regular-file entries and their summed sizes.
func countTarEntries(archive string, gzipped bool) (int64, int, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return 0, 0, err
		}
		defer gz.Close()
		r = gz
	}

	var total int64
	var files int
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return total, files, nil
		}
		if err != nil {
			return 0, 0, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		files++
		total += hdr.Size
	}
}
