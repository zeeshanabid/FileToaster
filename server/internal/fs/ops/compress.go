// Package ops holds the fileset operations the task engine runs. Each operation
// is a pure function over resolved paths so it can be unit-tested without the
// network or the pool. Copy, move and delete are the fileset operations; compress
// follows the same shape but never mutates its sources — it only opens them
// read-only and streams their bytes into a brand-new archive written in place.
package ops

import (
	"archive/tar"
	"archive/zip"
	"compress/flate"
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

// supportedArchiveFormats are the stdlib archive formats compress offers. An
// unsupported or unknown format is rejected with ErrUnsupportedFormat.
const (
	formatZip   = "zip"
	formatTargz = "targz"
	formatTar   = "tar"
)

// compressBufferSize is the per-file stream buffer for the archive writer. It
// matches copy's buffer so large-file compression stays throughput-bound.
const compressBufferSize = 8 << 20

// CompressConfig holds configuration for the compress operation.
type CompressConfig struct {
	// BufferSize is the per-file compress buffer size in bytes.
	BufferSize int64
	// CompressionLevel is the compression level (1-9, 0 = no compression).
	// Only used for zip and targz formats.
	CompressionLevel int
}

// DefaultCompressConfig returns the default compress configuration.
func DefaultCompressConfig() *CompressConfig {
	return &CompressConfig{
		BufferSize:       compressBufferSize,
		CompressionLevel: 6, // default: balanced
	}
}

// CompressArchive is the sentinel surfaced when a request names a format the
// stdlib cannot write. Its message lists the accepted formats for the UI.
var CompressArchiveFormatUnsupported = errors.New("unsupported format: supported: zip, tar.gz, tar")

// CompressErrEmptyPaths is the sentinel surfaced when no path is selected. It
// reuses copy's ErrEmptyPaths, which is shared in the ops package.

// CompressErrDifferentDirectories is returned when sources live in different
// top-level folders, so no single in-place archive name can be derived.
var CompressErrDifferentDirectories = errors.New("sources are in different folders")

// CompressErrOverlappingSelection is returned when the selection contains a path
// nested inside another selected path: one source would be walked while another
// archives it.
var CompressErrOverlappingSelection = errors.New("selection includes a path inside another selected path")

// CompressMeta is the serializable surface of a compress plan shown in the task
// record and label. Dest is the resolved, non-colliding archive path; Format is
// the selected archive format; Source is the number of selected sources.
type CompressMeta struct {
	Format string `json:"format"`
	Dest   string `json:"dest"`
	Source int    `json:"source"`
}

// ResolvedSource is one source selected for compression: its canonical path,
// whether it is a directory, and the Prefix its entries are rooted at inside the
// archive ("" for a file, the directory's basename plus a slash for a directory,
// so an extract restores the structure in place).
type ResolvedSource struct {
	Path   string `json:"path"`
	IsDir  bool   `json:"isDir"`
	Prefix string `json:"prefix"`
}

// CompressPlan is a validated, resolved set of sources and the single archive to
// write for them. It is the opaque spec passed to Run.
type CompressPlan struct {
	Sources          []ResolvedSource `json:"sources"`
	Dest             string           `json:"dest"`
	Format           string           `json:"format"`
	CompressionLevel int              `json:"compressionLevel"`
}

// CompressResolve validates the request, resolves every source against the
// allowed roots, and returns a ready-to-execute Plan for a single archive.
//
// Archive location and naming mirror a file manager's "pack into archive":
//   - The archive is written in place, in the selection's own directory, with no
//     destination picker.
//   - A single source (file or directory) is named after its own stem:
//     foo/bar.txt + zip -> foo/bar.zip; foo/pkg/ + zip -> foo/pkg.zip.
//   - Several sources are named after their longest common directory's stem:
//     foo/a.txt + foo/b.txt -> foo/foo.zip; foo/a.txt + foo/sub/b.txt ->
//     foo/foo.zip. Sources in different top-level folders are rejected with
//     CompressErrDifferentDirectories.
//   - A file keeps its basename flat inside the archive; a directory's entries
//     are rooted at the directory's basename (pkg/sub/x.txt), so extraction
//     restores the tree in place.
//   - If the target already exists it is renamed (" (1)", " (2)", …) before the
//     extension, so an existing archive is never overwritten.
//
// Sources are opened read-only by CompressRun; Resolve never writes or mutates
// anything. A source nested inside another selected source is rejected.
func CompressResolve(roots *fs.Roots, paths []string, format string, compressionLevel int) (*CompressPlan, error) {
	paths = uniqueTrim(paths)
	if len(paths) == 0 {
		return nil, ErrEmptyPaths
	}
	format = strings.TrimSpace(format)
	if !isSupportedArchiveFormat(format) {
		return nil, CompressArchiveFormatUnsupported
	}

	// Clamp compression level to valid range (0-9)
	if compressionLevel < 0 {
		compressionLevel = 0
	}
	if compressionLevel > 9 {
		compressionLevel = 9
	}

	resolved := make([]ResolvedSource, 0, len(paths))
	resolvedPaths := make([]string, 0, len(paths))
	for _, p := range paths {
		src, err := roots.Resolve(p)
		if err != nil {
			return nil, err
		}
		info, statErr := os.Lstat(src)
		if statErr != nil {
			return nil, statErr
		}
		prefix := ""
		if info.IsDir() {
			prefix = filepath.Base(src) + string(filepath.Separator)
		}
		resolved = append(resolved, ResolvedSource{Path: src, IsDir: info.IsDir(), Prefix: prefix})
		resolvedPaths = append(resolvedPaths, src)
	}

	// Reject a selection that contains a path inside another selected path:
	// archiving the outer source would stream the inner one's bytes twice.
	for i := 0; i < len(resolved); i++ {
		for j := 0; j < len(resolved); j++ {
			if i == j {
				continue
			}
			if isInside(resolved[i].Path, resolved[j].Path) {
				return nil, fmt.Errorf("%w", CompressErrOverlappingSelection)
			}
		}
	}

	dest, err := computeArchiveName(roots, resolvedPaths, format)
	if err != nil {
		return nil, err
	}

	plan := &CompressPlan{Dest: dest, Format: format, CompressionLevel: compressionLevel}
	for _, src := range resolved {
		plan.Sources = append(plan.Sources, src)
	}
	return plan, nil
}

// computeArchiveName derives the single in-place archive path for the resolved
// sources and format: the source's own stem for one source, otherwise the
// longest common parent directory's stem. The returned path is confined to the
// roots and, if it already exists, is renumbered (" (1)", …) so it never
// overwrites an existing archive.
func computeArchiveName(roots *fs.Roots, sources []string, format string) (string, error) {
	ext := archiveExt(format)
	var base string
	if len(sources) == 1 {
		src := sources[0]
		base = filepath.Join(filepath.Dir(src), stripExt(filepath.Base(src))+ext)
	} else {
		common := commonParentDir(sources)
		// Several sources share no single containing folder (e.g. they sit in
		// different allowed roots, or only under the filesystem root). Naming an
		// archive after such a parent is nonsensical, so reject rather than write
		// a root-named archive.
		if common == "" || common == string(filepath.Separator) || !roots.Contained(common) {
			return "", fmt.Errorf("%w", CompressErrDifferentDirectories)
		}
		base = filepath.Join(common, stripExt(filepath.Base(common))+ext)
	}
	return uniqueArchiveName(base, stripExt(filepath.Base(base)), ext), nil
}

// uniqueArchiveName returns base if it does not exist, otherwise base's stem
// renumbered " (1)", " (2)", … before its extension until a free name is found,
// so an archive is never overwritten.
func uniqueArchiveName(base, stem, ext string) string {
	if _, err := os.Stat(base); err != nil {
		return base
	}
	n := 1
	for {
		candidate := filepath.Join(filepath.Dir(base), stem+" ("+strconv.Itoa(n)+")"+ext)
		if _, serr := os.Stat(candidate); serr != nil {
			return candidate
		}
		n++
	}
}

// archiveExt maps a supported format name to the archive file extension.
func archiveExt(format string) string {
	switch format {
	case formatZip:
		return ".zip"
	case formatTargz:
		return ".tar.gz"
	case formatTar:
		return ".tar"
	}
	return ""
}

// isSupportedArchiveFormat reports whether format is one the stdlib can write.
func isSupportedArchiveFormat(format string) bool {
	switch format {
	case formatZip, formatTargz, formatTar:
		return true
	default:
		return false
	}
}

// stripExt removes a single file extension from name via filepath.Ext.
func stripExt(name string) string {
	if name == "" {
		return ""
	}
	return name[:len(name)-len(filepath.Ext(name))]
}

// commonParentDir returns the deepest directory that contains every path (the
// longest common path prefix). For sources in different top-level folders it
// returns the filesystem root ("/"), which the caller rejects.
func commonParentDir(paths []string) string {
	abs := strings.HasPrefix(filepath.Clean(paths[0]), string(filepath.Separator))
	parts := strings.Split(filepath.Clean(paths[0]), string(filepath.Separator))
	for _, p := range paths[1:] {
		cur := strings.Split(filepath.Clean(p), string(filepath.Separator))
		limit := len(cur)
		if len(parts) < limit {
			limit = len(parts)
		}
		k := 0
		for k < limit && parts[k] == cur[k] {
			k++
		}
		parts = parts[:k]
	}
	joined := filepath.Join(parts...)
	// filepath.Join drops a leading empty component, collapsing an absolute path
	// to a relative one; reattach the root separator so containment checks work.
	if abs && joined != string(filepath.Separator) {
		joined = string(filepath.Separator) + joined
	}
	return joined
}

// CompressRun executes the plan, writing a single archive at plan.Dest from the
// resolved sources and reporting progress through prog. Sources are opened
// read-only and only streamed: they are never modified, renamed, or deleted.
// The destination is excluded from its own walk (the directory-in-place case),
// and a cancelled context is returned directly so the pool marks the task
// cancelled.
func CompressRun(ctx context.Context, plan *CompressPlan, prog *task.Progression, cfg *CompressConfig) error {
	var totalBytes int64
	var filesTotal int
	for _, src := range plan.Sources {
		size, files, err := statTarget(src.Path)
		if err != nil {
			return err
		}
		totalBytes += size
		filesTotal += files
	}
	prog.SetTotal(totalBytes, filesTotal)

	f, err := os.OpenFile(plan.Dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	archive := newArchive(plan, f)
	defer f.Close()

	buf := make([]byte, cfg.BufferSize)
	for _, src := range plan.Sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := archive.walk(ctx, src.Path, src.Prefix, plan.Dest, prog, buf); err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	return archive.close()
}

// archive wraps the writer for one plan.Format and streams regular files into it.
type archive struct {
	format           string
	f                *os.File
	zw               *zip.Writer
	gw               *gzip.Writer
	tw               *tar.Writer
	compressionLevel int
}

// newArchive opens the writer for plan.Format: a zip writer, a gzip-wrapped tar
// writer, or a plain tar writer.
func newArchive(plan *CompressPlan, f *os.File) *archive {
	a := &archive{format: plan.Format, f: f, compressionLevel: plan.CompressionLevel}
	switch plan.Format {
	case formatZip:
		a.zw = zip.NewWriter(f)
		// Register a custom compressor with the specified compression level
		if a.compressionLevel >= 0 && a.compressionLevel <= 9 {
			a.zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
				return flate.NewWriter(out, a.compressionLevel)
			})
		}
	case formatTargz:
		var err error
		a.gw, err = gzip.NewWriterLevel(f, a.compressionLevel)
		if err != nil {
			// Fallback to default compression if level is invalid
			a.gw, _ = gzip.NewWriterLevel(f, gzip.DefaultCompression)
		}
		a.tw = tar.NewWriter(a.gw)
	case formatTar:
		a.tw = tar.NewWriter(f)
	}
	return a
}

// walk archives src and, for directories, recurses into each child rooted at the
// supplied prefix (so entries are relative to the source's basename). It skips
// the destination archive itself and returns a context error on cancellation.
func (a *archive) walk(ctx context.Context, src, prefix, dest string, prog *task.Progression, buf []byte) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return a.archiveFile(ctx, prefix+filepath.Base(src), info, src, prog, buf)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, de := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := filepath.Join(src, de.Name())
		if child == dest {
			// The archive is being written in place; excluding it stops a
			// directory from archiving its own output.
			continue
		}
		i2, err := de.Info()
		if err != nil {
			continue
		}
		if de.IsDir() {
			// A directory contributes a basename-rooted prefix for its children.
			childPrefix := prefix + de.Name() + string(filepath.Separator)
			if err := a.walk(ctx, child, childPrefix, dest, prog, buf); err != nil {
				return err
			}
			continue
		}
		// A regular file contributes a flat entry rooted at the source's prefix.
		if err := a.archiveFile(ctx, prefix+de.Name(), i2, child, prog, buf); err != nil {
			return err
		}
	}
	return nil
}

// archiveFile records one regular file — its header (with mode, mod time, size)
// and its bytes — into the archive, reporting bytes as each chunk streams. The
// file is opened read-only and never altered.
func (a *archive) archiveFile(ctx context.Context, name string, info os.FileInfo, src string, prog *task.Progression, buf []byte) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	switch a.format {
	case formatZip:
		fh, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		fh.Name = name
		fh.Method = zip.Deflate
		w, err := a.zw.CreateHeader(fh)
		if err != nil {
			return err
		}
		if err := streamTo(ctx, w, in, prog, buf); err != nil {
			return err
		}
		prog.AddFile()
		return nil
	default: // tar and tar.gz
		hdr := &tar.Header{
			Name:    name,
			Mode:    int64(info.Mode().Perm()),
			ModTime: info.ModTime(),
			Size:    info.Size(),
			Format:  tar.FormatGNU,
		}
		if err := a.tw.WriteHeader(hdr); err != nil {
			return err
		}
		if err := streamTo(ctx, a.tw, in, prog, buf); err != nil {
			return err
		}
		prog.AddFile()
		return nil
	}
}

// close finalizes the archive: it closes the tar writer before the gzip writer
// (gzip/tar.gz) so the trailer is written, then syncs and closes the file.
func (a *archive) close() error {
	var firstErr error
	switch a.format {
	case formatZip:
		firstErr = a.zw.Close()
	case formatTargz:
		if err := a.tw.Close(); err != nil {
			return err
		}
		firstErr = a.gw.Close()
	case formatTar:
		firstErr = a.tw.Close()
	}
	if firstErr != nil {
		return firstErr
	}
	if err := a.f.Sync(); err != nil {
		return err
	}
	return a.f.Close()
}

// streamTo copies r to w in chunks, reporting bytes as each chunk is written,
// and returns a context error if the context is cancelled mid-stream.
func streamTo(ctx context.Context, w io.Writer, r io.Reader, prog *task.Progression, buf []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			prog.AddBytes(int64(n))
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
}
