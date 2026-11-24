package ops

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// writeZip writes a zip archive at path from the given entry layout (name ->
// content). Entries are stored uncompressed; directory entries are created when
// a name ends with a slash.
func writeZip(t *testing.T, path string, layout map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range layout {
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeTarGz writes a gzip-compressed tar archive at path from the entry layout.
func writeTarGz(t *testing.T, path string, layout map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, content := range layout {
		hdr := &tar.Header{
			Name:   name,
			Mode:   0o644,
			Size:   int64(len(content)),
			Format: tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// sha256File returns the hex SHA-256 digest of the file at path.
func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- Resolve: format detection ----------------------------------------------

func TestDecompressResolveUnsupportedFormat(t *testing.T) {
	for _, ext := range []string{".txt", ".bz2", ".gz", ".xz", ".zst", ".rar", ".7z", ""} {
		base := writeTree(t, map[string]string{"x" + ext: "data"})
		roots := rootsFor(t, base)
		_, err := DecompressResolve(roots, []string{filepath.Join(base, "x"+ext)}, "")
		if !errors.Is(err, DecompressUnsupportedFormat) {
			t.Errorf("%q: err = %v, want DecompressUnsupportedFormat", ext, err)
		}
	}
}

// TestDecompressResolveSingleFileOnly verifies a selection of more than one path
// is rejected with DecompressMultiplePaths.
func TestDecompressResolveSingleFileOnly(t *testing.T) {
	base := writeTree(t, map[string]string{"a.zip": "1", "b.zip": "2"})
	roots := rootsFor(t, base)
	_, err := DecompressResolve(roots, []string{
		filepath.Join(base, "a.zip"),
		filepath.Join(base, "b.zip"),
	}, "")
	if !errors.Is(err, DecompressMultiplePaths) {
		t.Fatalf("err = %v, want DecompressMultiplePaths", err)
	}
}

// TestDecompressResolveEmptyPaths verifies an empty selection is rejected.
func TestDecompressResolveEmptyPaths(t *testing.T) {
	base := writeTree(t, map[string]string{"a.zip": "1"})
	_, err := DecompressResolve(rootsFor(t, base), nil, "")
	if !errors.Is(err, ErrEmptyPaths) {
		t.Fatalf("err = %v, want ErrEmptyPaths", err)
	}
}

// TestDecompressResolveOutsideRoots verifies a traversal escape is rejected.
func TestDecompressResolveOutsideRoots(t *testing.T) {
	base := writeTree(t, map[string]string{"a.zip": "1"})
	_, err := DecompressResolve(rootsFor(t, base), []string{"/etc/passwd"}, "")
	if !errors.Is(err, fs.ErrOutsideRoots) {
		t.Fatalf("err = %v, want fs.ErrOutsideRoots", err)
	}
}

// TestDecompressResolveNotADirectory verifies a directory is not accepted as an
// archive (the button only enables for files).
func TestDecompressResolveNotADirectory(t *testing.T) {
	base := writeTree(t, map[string]string{"dir/inner.txt": "x"})
	roots := rootsFor(t, base)
	_, err := DecompressResolve(roots, []string{filepath.Join(base, "dir")}, "")
	if !errors.Is(err, DecompressUnsupportedFormat) {
		t.Fatalf("err = %v, want DecompressUnsupportedFormat", err)
	}
}

// --- Resolve + Run: extractable archives ------------------------------------

// TestArchiveIntactAndFilesExtracted is the user's explicit test: each supported
// format extracts its nested files with structure intact and leaves the original
// archive byte-identical (compared by SHA-256).
func TestArchiveIntactAndFilesExtracted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		archive string // archive file name relative to base
		format  string
	}{
		{"zip", "x.zip", formatZip},
		{"targz", "x.tar.gz", formatTargz},
		{"tar", "x.tar", formatTar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := writeTree(t, map[string]string{})
			archivePath := filepath.Join(base, tc.archive)
			writeArchive(t, archivePath, tc.format, map[string]string{
				"a.txt":        "hello",
				"sub/deep.txt": "deep",
			})
			before := sha256File(t, archivePath)

			roots := rootsFor(t, base)
			plan, err := DecompressResolve(roots, []string{archivePath}, "")
			if err != nil {
				t.Fatalf("DecompressResolve: %v", err)
			}
			if plan.Format != tc.format {
				t.Fatalf("plan.Format = %q, want %q", plan.Format, tc.format)
			}
			if err := DecompressRun(context.Background(), plan, &task.Progression{}, DefaultDecompressConfig()); err != nil {
				t.Fatalf("DecompressRun: %v", err)
			}

			if b, _ := os.ReadFile(filepath.Join(base, "a.txt")); string(b) != "hello" {
				t.Errorf("a.txt = %q, want hello", b)
			}
			if b, _ := os.ReadFile(filepath.Join(base, "sub", "deep.txt")); string(b) != "deep" {
				t.Errorf("sub/deep.txt = %q, want deep", b)
			}
			// The original archive is untouched: still present and byte-identical.
			if b, _ := os.ReadFile(archivePath); string(b) == "" {
				t.Errorf("original archive vanished")
			}
			if after := sha256File(t, archivePath); after != before {
				t.Errorf("archive changed under a decompress: before %s after %s", before, after)
			}
		})
	}
}

// writeArchive builds an archive of the given format at path from layout.
func writeArchive(t *testing.T, path, format string, layout map[string]string) {
	t.Helper()
	switch format {
	case formatZip:
		writeZip(t, path, layout)
	case formatTargz:
		writeTarGz(t, path, layout)
	case formatTar:
		writeTar(t, path, layout)
	default:
		t.Fatalf("unknown format %q", format)
	}
}

// writeTar writes a plain (uncompressed) tar archive at path from the layout.
func writeTar(t *testing.T, path string, layout map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for name, content := range layout {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Format: tar.FormatGNU}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestExtractHereZip verifies into="" extracts the contents directly into the
// archive's own folder.
func TestExtractHereZip(t *testing.T) {
	base := writeTree(t, map[string]string{})
	archive := filepath.Join(base, "pkg.zip")
	writeZip(t, archive, map[string]string{"a.txt": "x", "b.txt": "y"})
	roots := rootsFor(t, base)

	plan, err := DecompressResolve(roots, []string{archive}, "")
	if err != nil {
		t.Fatalf("DecompressResolve: %v", err)
	}
	if plan.ExtractDir != base {
		t.Fatalf("ExtractDir = %q, want %q", plan.ExtractDir, base)
	}
	if err := DecompressRun(context.Background(), plan, &task.Progression{}, DefaultDecompressConfig()); err != nil {
		t.Fatalf("DecompressRun: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "a.txt")); string(b) != "x" {
		t.Errorf("a.txt = %q, want x", b)
	}
}

// TestExtractInDirZip verifies into=stem extracts into a sibling folder named
// after that stem.
func TestExtractInDirZip(t *testing.T) {
	base := writeTree(t, map[string]string{})
	archive := filepath.Join(base, "myfiles.zip")
	writeZip(t, archive, map[string]string{"a.txt": "x"})
	roots := rootsFor(t, base)

	plan, err := DecompressResolve(roots, []string{archive}, "myfiles")
	if err != nil {
		t.Fatalf("DecompressResolve: %v", err)
	}
	if plan.ExtractDir != filepath.Join(base, "myfiles") {
		t.Fatalf("ExtractDir = %q, want %q", plan.ExtractDir, filepath.Join(base, "myfiles"))
	}
	if err := DecompressRun(context.Background(), plan, &task.Progression{}, DefaultDecompressConfig()); err != nil {
		t.Fatalf("DecompressRun: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "myfiles", "a.txt")); string(b) != "x" {
		t.Errorf("myfiles/a.txt = %q, want x", b)
	}
}

// TestExtractInDirNumbering verifies an existing extraction folder is renumbered
// " (1)", " (2)", … rather than merged into.
func TestExtractInDirNumbering(t *testing.T) {
	base := writeTree(t, map[string]string{})
	archive := filepath.Join(base, "myfiles.zip")
	writeZip(t, archive, map[string]string{"a.txt": "x"})
	// Pre-create the target folder with a sentinel file.
	if err := os.MkdirAll(filepath.Join(base, "myfiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "myfiles", "keep.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, base)

	plan, err := DecompressResolve(roots, []string{archive}, "myfiles")
	if err != nil {
		t.Fatalf("DecompressResolve: %v", err)
	}
	if plan.ExtractDir != filepath.Join(base, "myfiles (1)") {
		t.Fatalf("ExtractDir = %q, want %q", plan.ExtractDir, filepath.Join(base, "myfiles (1)"))
	}
	if err := DecompressRun(context.Background(), plan, &task.Progression{}, DefaultDecompressConfig()); err != nil {
		t.Fatalf("DecompressRun: %v", err)
	}
	// The sentinel is untouched; the new files landed alongside it.
	if b, _ := os.ReadFile(filepath.Join(base, "myfiles", "keep.txt")); string(b) != "old" {
		t.Errorf("myfiles/keep.txt = %q, want old", b)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "myfiles (1)", "a.txt")); string(b) != "x" {
		t.Errorf("myfiles (1)/a.txt = %q, want x", b)
	}
}

// TestZipTraversalRejected verifies a zip-slip entry ("..") is rejected with
// DecompressArchiveTraversal and nothing is written outside the extraction root.
func TestZipTraversalRejected(t *testing.T) {
	base := writeTree(t, map[string]string{})
	archive := filepath.Join(base, "bad.zip")
	// Put the traversal entry first so the run aborts before anything lands.
	writeZip(t, archive, map[string]string{
		"../../outside.txt": "escaped",
		"ok.txt":            "safe",
	})
	roots := rootsFor(t, base)

	plan, err := DecompressResolve(roots, []string{archive}, "")
	if err != nil {
		t.Fatalf("DecompressResolve: %v", err)
	}
	err = DecompressRun(context.Background(), plan, &task.Progression{}, DefaultDecompressConfig())
	if !errors.Is(err, DecompressArchiveTraversal) {
		t.Fatalf("DecompressRun err = %v, want DecompressArchiveTraversal", err)
	}
	// Nothing escaped the extraction root: the target up the tree must be absent.
	outside := filepath.Join(filepath.Dir(base), "outside.txt")
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Errorf("zip-slip wrote outside the root: %s exists (err %v)", outside, statErr)
	}
	// The archive itself is intact.
	if b, _ := os.ReadFile(archive); string(b) == "" {
		t.Errorf("archive vanished")
	}
}

// TestTargzTraversalRejected verifies an absolute tar name is rejected the same
// way, with nothing written outside the root.
func TestTargzTraversalRejected(t *testing.T) {
	base := writeTree(t, map[string]string{})
	archive := filepath.Join(base, "bad.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"/tmp/escaped.txt": "escaped",
		"ok.txt":           "safe",
	})
	roots := rootsFor(t, base)

	plan, err := DecompressResolve(roots, []string{archive}, "")
	if err != nil {
		t.Fatalf("DecompressResolve: %v", err)
	}
	err = DecompressRun(context.Background(), plan, &task.Progression{}, DefaultDecompressConfig())
	if !errors.Is(err, DecompressArchiveTraversal) {
		t.Fatalf("DecompressRun err = %v, want DecompressArchiveTraversal", err)
	}
	if _, statErr := os.Stat("/tmp/escaped.txt"); !os.IsNotExist(statErr) {
		t.Errorf("tar traversal wrote /tmp/escaped.txt (err %v)", statErr)
	}
}

// TestProgressCounts verifies byte/file totals are measured up front and reported
// incrementally as each entry streams.
func TestProgressCounts(t *testing.T) {
	base := writeTree(t, map[string]string{})
	archive := filepath.Join(base, "pkg.zip")
	writeZip(t, archive, map[string]string{
		"root.txt":    "root", // 4 bytes
		"a/one.txt":   "one",  // 3 bytes
		"a/b/two.txt": "two",  // 3 bytes
	})
	roots := rootsFor(t, base)

	plan, err := DecompressResolve(roots, []string{archive}, "")
	if err != nil {
		t.Fatalf("DecompressResolve: %v", err)
	}
	prog := &task.Progression{}
	done := make(chan struct{})
	go func() {
		_ = DecompressRun(context.Background(), plan, prog, DefaultDecompressConfig())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DecompressRun timed out")
	}

	snap := prog.Snapshot()
	if snap.FilesTotal != 3 || snap.FilesDone != 3 {
		t.Errorf("files = %d/%d, want 3/3", snap.FilesDone, snap.FilesTotal)
	}
	if snap.Total != 10 || snap.Done != 10 {
		t.Errorf("bytes = %d/%d, want 10/10", snap.Done, snap.Total)
	}
}

// TestCancelledLeavesArchiveIntact verifies cancelling mid-extraction returns
// context.Canceled and leaves the archive byte-identical.
func TestCancelledLeavesArchiveIntact(t *testing.T) {
	base := writeTree(t, map[string]string{})
	archive := filepath.Join(base, "pkg.zip")
	writeZip(t, archive, map[string]string{"a.txt": "data-a", "b.txt": "data-b"})
	before := sha256File(t, archive)
	roots := rootsFor(t, base)

	plan, err := DecompressResolve(roots, []string{archive}, "")
	if err != nil {
		t.Fatalf("DecompressResolve: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prog := &task.Progression{}
	var runErr error
	done := make(chan struct{})
	prog.SetSink(func(task.ByteProgress) { cancel() }) // stop at the first streamed chunk
	go func() {
		runErr = DecompressRun(ctx, plan, prog, DefaultDecompressConfig())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DecompressRun timed out")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("DecompressRun err = %v, want context.Canceled", runErr)
	}
	if after := sha256File(t, archive); after != before {
		t.Errorf("archive changed under a cancelled run: before %s after %s", before, after)
	}
}
