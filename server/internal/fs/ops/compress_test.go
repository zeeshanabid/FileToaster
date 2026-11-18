package ops

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// --- Resolve: naming ---------------------------------------------------------

// TestCompressResolveSingleFileReplacesExtension verifies a single file is named
// after its own stem, replacing the extension (bar.txt -> bar.zip) with a flat
// archive entry.
func TestCompressResolveSingleFileReplacesExtension(t *testing.T) {
	src := writeTree(t, map[string]string{"bar.txt": "hello"})
	roots := rootsFor(t, src)

	plan, err := CompressResolve(roots, []string{filepath.Join(src, "bar.txt")}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if plan.Dest != filepath.Join(src, "bar.zip") {
		t.Errorf("Dest = %q, want %q", plan.Dest, filepath.Join(src, "bar.zip"))
	}
	if len(plan.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(plan.Sources))
	}
	if plan.Sources[0].Prefix != "" {
		t.Errorf("file prefix = %q, want empty (flat entry)", plan.Sources[0].Prefix)
	}
}

// TestCompressResolveSingleDirectoryReplacesExtension verifies a single directory
// is named after its own stem (pkg/ -> pkg.zip), rooted at its basename inside
// the archive.
func TestCompressResolveSingleDirectoryReplacesExtension(t *testing.T) {
	src := writeTree(t, map[string]string{"pkg/inner.txt": "x", "pkg/sub/deep.txt": "y"})
	roots := rootsFor(t, src)

	plan, err := CompressResolve(roots, []string{filepath.Join(src, "pkg")}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if plan.Dest != filepath.Join(src, "pkg.zip") {
		t.Errorf("Dest = %q, want %q", plan.Dest, filepath.Join(src, "pkg.zip"))
	}
	if plan.Sources[0].Prefix != "pkg/" {
		t.Errorf("dir prefix = %q, want %q", plan.Sources[0].Prefix, "pkg/")
	}
}

// TestCompressResolveMultipleFilesShareParent verifies several files in one folder
// are named after that folder (foo/a.txt + foo/b.txt -> foo/foo.zip).
func TestCompressResolveMultipleFilesShareParent(t *testing.T) {
	parent := t.TempDir()
	writeTreeAt(t, filepath.Join(parent, "foo"), map[string]string{"a.txt": "1", "b.txt": "2"})
	roots := rootsFor(t, parent)

	plan, err := CompressResolve(roots, []string{
		filepath.Join(parent, "foo", "a.txt"),
		filepath.Join(parent, "foo", "b.txt"),
	}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if plan.Dest != filepath.Join(parent, "foo", "foo.zip") {
		t.Errorf("Dest = %q, want %q", plan.Dest, filepath.Join(parent, "foo", "foo.zip"))
	}
}

// TestCompressResolveNestedMultipleFilesShareParent verifies the common parent
// is the deepest shared directory even when files sit at different depths
// (foo/a.txt + foo/sub/b.txt -> foo/foo.zip).
func TestCompressResolveNestedMultipleFilesShareParent(t *testing.T) {
	parent := t.TempDir()
	writeTreeAt(t, filepath.Join(parent, "foo"), map[string]string{"a.txt": "1", "sub/b.txt": "2"})
	roots := rootsFor(t, parent)

	plan, err := CompressResolve(roots, []string{
		filepath.Join(parent, "foo", "a.txt"),
		filepath.Join(parent, "foo", "sub", "b.txt"),
	}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if plan.Dest != filepath.Join(parent, "foo", "foo.zip") {
		t.Errorf("Dest = %q, want %q", plan.Dest, filepath.Join(parent, "foo", "foo.zip"))
	}
}

// TestCompressResolveConflictNumbering verifies a colliding target is renumbered
// " (1)", " (2)", … before the extension, and never overwrites an existing file.
func TestCompressResolveConflictNumbering(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "x"})
	// The target (a.zip) and its first numbered variant already sit beside a.txt.
	for _, name := range []string{"a.zip", "a (1).zip"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	roots := rootsFor(t, parent)

	plan, err := CompressResolve(roots, []string{filepath.Join(src, "a.txt")}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if plan.Dest != filepath.Join(src, "a (2).zip") {
		t.Fatalf("Dest = %q, want %q", plan.Dest, filepath.Join(src, "a (2).zip"))
	}
}

// TestCompressResolveNeverOverwrites verifies a pre-existing archive at the target
// name is left untouched: CompressRun writes the new archive at the renumbered
// path and the original survives byte-identical.
func TestCompressResolveNeverOverwrites(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "new"})
	if err := os.WriteFile(filepath.Join(src, "a.zip"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	plan, err := CompressResolve(roots, []string{filepath.Join(src, "a.txt")}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if err := CompressRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("CompressRun: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "a.zip")); string(b) != "old" {
		t.Errorf("pre-existing a.zip = %q, want old (must not be overwritten)", b)
	}
	files, err := extractZip(filepath.Join(src, "a (1).zip"))
	if err != nil {
		t.Fatalf("extractZip a (1).zip: %v", err)
	}
	if b := files["a.txt"]; string(b) != "new" {
		t.Errorf("a (1).zip entry a.txt = %q, want new", b)
	}
}

// TestCompressResolveDifferentDirectories verifies sources in different allowed
// roots (no single folder to hold the archive) are rejected.
func TestCompressResolveDifferentDirectories(t *testing.T) {
	parent := t.TempDir()
	r1 := filepath.Join(parent, "R1")
	r2 := filepath.Join(parent, "R2")
	writeTreeAt(t, r1, map[string]string{"a.txt": "1"})
	writeTreeAt(t, r2, map[string]string{"b.txt": "2"})
	roots := rootsFor(t, r1, r2)

	_, err := CompressResolve(roots, []string{
		filepath.Join(r1, "a.txt"),
		filepath.Join(r2, "b.txt"),
	}, "zip")
	if !errors.Is(err, CompressErrDifferentDirectories) {
		t.Fatalf("err = %v, want CompressErrDifferentDirectories", err)
	}
}

// TestCompressResolveOverlappingSelection verifies a source nested inside another
// selected source is rejected.
func TestCompressResolveOverlappingSelection(t *testing.T) {
	src := writeTree(t, map[string]string{"sub/inner.txt": "x"})
	roots := rootsFor(t, src)

	_, err := CompressResolve(roots, []string{
		filepath.Join(src, "sub"),
		filepath.Join(src, "sub", "inner.txt"),
	}, "zip")
	if !errors.Is(err, CompressErrOverlappingSelection) {
		t.Fatalf("err = %v, want CompressErrOverlappingSelection", err)
	}
}

// TestCompressResolveUnsupportedFormat verifies an unknown format is rejected with
// the human "supported: zip, tar.gz, tar" message rather than a panic.
func TestCompressResolveUnsupportedFormat(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "x"})
	roots := rootsFor(t, src)

	_, err := CompressResolve(roots, []string{filepath.Join(src, "a.txt")}, "bz2")
	if !errors.Is(err, CompressArchiveFormatUnsupported) {
		t.Fatalf("err = %v, want CompressArchiveFormatUnsupported", err)
	}
}

// TestCompressResolveEmptyPaths verifies an empty selection is rejected.
func TestCompressResolveEmptyPaths(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "x"})
	_, err := CompressResolve(rootsFor(t, src), nil, "zip")
	if !errors.Is(err, ErrEmptyPaths) {
		t.Fatalf("err = %v, want ErrEmptyPaths", err)
	}
}

// TestCompressResolveOutsideRoots verifies a traversal escape is rejected.
func TestCompressResolveOutsideRoots(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "x"})
	_, err := CompressResolve(rootsFor(t, src), []string{"/etc/passwd"}, "zip")
	if !errors.Is(err, fs.ErrOutsideRoots) {
		t.Fatalf("err = %v, want fs.ErrOutsideRoots", err)
	}
}

// --- Resolve + Run: extractable archives ------------------------------------

type tarEntry struct {
	content []byte
	mode    os.FileMode
}

// extractZip reads a zip archive into a map of entry name -> content bytes.
func extractZip(path string) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(zr.File))
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		out[zf.Name] = b
	}
	return out, nil
}

// extractTar reads a tar or tar.gz archive into a map of entry name -> entry.
func extractTar(path string, compressed bool) (map[string]tarEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if compressed {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	out := make(map[string]tarEntry)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		out[hdr.Name] = tarEntry{content: b, mode: os.FileMode(hdr.Mode)}
	}
	return out, nil
}

// TestCompressRunFormatsExtractable verifies each supported format yields an
// archive that re-extracts with the §3 internal-path rules: files flat at their
// basename, a directory rooted at its basename, and file permissions preserved.
func TestCompressRunFormatsExtractable(t *testing.T) {
	for _, format := range []string{"zip", "targz", "tar"} {
		t.Run(format, func(t *testing.T) {
			parent := t.TempDir()
			pkg := filepath.Join(parent, "pkg")
			if err := os.MkdirAll(filepath.Join(pkg, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkg, "inner.txt"), []byte("inner"), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkg, "sub", "deep.txt"), []byte("deep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(parent, "top.txt"), []byte("top"), 0o644); err != nil {
				t.Fatal(err)
			}
			roots := rootsFor(t, parent)

			plan, err := CompressResolve(roots, []string{
				filepath.Join(pkg),
				filepath.Join(parent, "top.txt"),
			}, format)
			if err != nil {
				t.Fatalf("CompressResolve: %v", err)
			}
			if err := CompressRun(context.Background(), plan, &task.Progression{}); err != nil {
				t.Fatalf("CompressRun: %v", err)
			}

			var names map[string][]byte
			if format == "zip" {
				names, err = extractZip(plan.Dest)
			} else {
				var entries map[string]tarEntry
				if format == "targz" {
					entries, err = extractTar(plan.Dest, true)
				} else {
					entries, err = extractTar(plan.Dest, false)
				}
				if err != nil {
					t.Fatalf("extract: %v", err)
				}
				names = make(map[string][]byte, len(entries))
				for k, v := range entries {
					names[k] = v.content
				}
				// Permissions are preserved on the directory-rooted entries.
				if got := entries["pkg/inner.txt"].mode; got != 0o640 {
					t.Errorf("inner.txt mode = %o, want 640", got)
				}
			}
			if err != nil {
				t.Fatalf("extract: %v", err)
			}

			// Files are flat at their basename; the directory is basename-rooted.
			assertEntry(t, names, filepath.Join("top.txt"), []byte("top"))
			assertEntry(t, names, filepath.Join("pkg", "inner.txt"), []byte("inner"))
			assertEntry(t, names, filepath.Join("pkg", "sub", "deep.txt"), []byte("deep"))
		})
	}
}

// assertEntry checks the archive contains name with the expected content.
func assertEntry(t *testing.T, entries map[string][]byte, name string, want []byte) {
	t.Helper()
	got, ok := entries[name]
	if !ok {
		t.Fatalf("archive missing entry %q in %v", name, keys(entries))
	}
	if string(got) != string(want) {
		t.Errorf("entry %q = %q, want %q", name, got, want)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCompressRunRecursiveDirectory verifies a selected directory is included
// recursively.
func TestCompressRunRecursiveDirectory(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{
		"root.txt":    "root",
		"a/one.txt":   "one",
		"a/b/two.txt": "two",
	})
	roots := rootsFor(t, parent)

	plan, err := CompressResolve(roots, []string{src}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if err := CompressRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("CompressRun: %v", err)
	}

	entries, err := extractZip(plan.Dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	// Rooted at the directory's basename, preserving structure.
	assertEntry(t, entries, filepath.Join("src", "root.txt"), []byte("root"))
	assertEntry(t, entries, filepath.Join("src", "a", "one.txt"), []byte("one"))
	assertEntry(t, entries, filepath.Join("src", "a", "b", "two.txt"), []byte("two"))
}

// TestCompressRunWalkSkipsDestination verifies the directory-in-place walk
// excludes the destination archive itself: a file sitting at the resolved dest
// path is never streamed into the archive being written. The Plan is constructed
// directly to place the dest inside the walked directory, the one case Resolve's
// overlap guard cannot reach.
func TestCompressRunWalkSkipsDestination(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	writeTreeAt(t, root, map[string]string{"a.txt": "hello", "out.zip": "PREEXISTING"})

	plan := &CompressPlan{
		Format: "zip",
		Dest:   filepath.Join(root, "out.zip"),
		Sources: []ResolvedSource{
			{Path: root, IsDir: true, Prefix: "root/"},
		},
	}
	if err := CompressRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("CompressRun: %v", err)
	}

	// The pre-existing destination file is never streamed into the archive as an
	// entry (otherwise the run would read the file it is writing), and the rest
	// of the source still archives.
	entries, err := extractZip(plan.Dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, ok := entries["root/a.txt"]; !ok {
		t.Errorf("archive missing entry root/a.txt in %v", keys(entries))
	}
	if _, ok := entries["root/out.zip"]; ok {
		t.Errorf("archive must exclude its own destination root/out.zip")
	}
}

// --- Run: progress, cancellation, immutability -------------------------------

// TestCompressRunProgressCounts verifies the byte/file totals are measured
// recursively up front and reported incrementally as bytes stream.
func TestCompressRunProgressCounts(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{
		"root.txt":    "root", // 4 bytes
		"a/one.txt":   "one",  // 3 bytes
		"a/b/two.txt": "two",  // 3 bytes
	})
	roots := rootsFor(t, parent)

	plan, err := CompressResolve(roots, []string{src}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	prog := &task.Progression{}
	done := make(chan struct{})
	go func() {
		_ = CompressRun(context.Background(), plan, prog)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CompressRun timed out")
	}

	snap := prog.Snapshot()
	if snap.FilesTotal != 3 || snap.FilesDone != 3 {
		t.Errorf("files = %d/%d, want 3/3", snap.FilesDone, snap.FilesTotal)
	}
	if snap.Total != 10 || snap.Done != 10 {
		t.Errorf("bytes = %d/%d, want 10/10", snap.Done, snap.Total)
	}
}

// TestCompressRunPreCancelled verifies a pre-cancelled context returns
// context.Canceled and leaves the source untouched.
func TestCompressRunPreCancelled(t *testing.T) {
	src := writeTree(t, map[string]string{"a.txt": "data"})
	roots := rootsFor(t, src)

	plan, err := CompressResolve(roots, []string{filepath.Join(src, "a.txt")}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CompressRun(ctx, plan, &task.Progression{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompressRun err = %v, want context.Canceled", err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "a.txt")); string(b) != "data" {
		t.Errorf("source changed under a cancelled run: %q", b)
	}
}

// TestCompressRunMidRunCancellation verifies cancelling after the first file
// reports stops the walk, returns context.Canceled, and leaves surviving sources
// intact.
func TestCompressRunMidRunCancellation(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	writeTreeAt(t, src, map[string]string{"a.txt": "data-a", "b.txt": "data-b"})
	roots := rootsFor(t, src)

	plan, err := CompressResolve(roots, []string{
		filepath.Join(src, "a.txt"),
		filepath.Join(src, "b.txt"),
	}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prog := &task.Progression{}
	var runErr error
	done := make(chan struct{})
	prog.SetSink(func(bp task.ByteProgress) {
		cancel() // stop as soon as the first chunk streams
	})
	go func() {
		runErr = CompressRun(ctx, plan, prog)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CompressRun timed out")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("CompressRun err = %v, want context.Canceled", runErr)
	}
	// The second source is never opened/modified.
	if b, _ := os.ReadFile(filepath.Join(src, "b.txt")); string(b) != "data-b" {
		t.Errorf("b.txt changed by a cancelled run: %q", b)
	}
}

// TestCompressRunOriginalsReadonly verifies sources are opened read-only: after
// the archive is written each source still exists at the same path with
// byte-identical content, mode, and mod time.
func TestCompressRunOriginalsReadonly(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, "a.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(file, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	origInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, src)

	plan, err := CompressResolve(roots, []string{file}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if err := CompressRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("CompressRun: %v", err)
	}

	newInfo, err := os.Stat(file)
	if err != nil {
		t.Fatalf("source vanished after compress: %v", err)
	}
	if b, _ := os.ReadFile(file); string(b) != "hello" {
		t.Errorf("source content = %q, want hello (unchanged)", b)
	}
	if newInfo.Mode() != origInfo.Mode() {
		t.Errorf("source mode = %o, want %o (must not be modified)", newInfo.Mode(), origInfo.Mode())
	}
	if !newInfo.ModTime().Equal(origInfo.ModTime()) {
		t.Errorf("source mod time = %v, want %v (must not be modified)", newInfo.ModTime(), origInfo.ModTime())
	}
}

// TestCompressRunLeavesOriginalsAndCreatesOnlyArchive verifies a completed run
// keeps every selected source byte-identical and adds exactly one new file — the
// archive — without renaming or removing anything else in the tree.
func TestCompressRunLeavesOriginalsAndCreatesOnlyArchive(t *testing.T) {
	parent := t.TempDir()
	writeTreeAt(t, filepath.Join(parent, "pkg"), map[string]string{
		"inner.txt":    "inner",
		"sub/deep.txt": "deep",
	})
	if err := os.WriteFile(filepath.Join(parent, "top.txt"), []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := rootsFor(t, parent)

	beforePresent, beforeContent := snapshotTree(t, parent)

	plan, err := CompressResolve(roots, []string{
		filepath.Join(parent, "pkg"),
		filepath.Join(parent, "top.txt"),
	}, "zip")
	if err != nil {
		t.Fatalf("CompressResolve: %v", err)
	}
	if err := CompressRun(context.Background(), plan, &task.Progression{}); err != nil {
		t.Fatalf("CompressRun: %v", err)
	}

	afterPresent, _ := snapshotTree(t, parent)

	// Exactly one new file appeared: the archive at plan.Dest.
	created := []string{}
	for path := range afterPresent {
		if _, ok := beforePresent[path]; !ok {
			created = append(created, path)
		}
	}
	expected, relErr := filepath.Rel(parent, plan.Dest)
	if relErr != nil {
		t.Fatalf("filepath.Rel: %v", relErr)
	}
	if len(created) != 1 || created[0] != expected {
		t.Errorf("added files = %v, want exactly [%q]", created, expected)
	}

	// Every original still exists and is byte-identical.
	for path, want := range beforeContent {
		if !afterPresent[path] {
			t.Errorf("original %q vanished after compress", path)
			continue
		}
		got, err := os.ReadFile(filepath.Join(parent, path))
		if err != nil {
			t.Fatalf("reading original %q: %v", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("original %q content = %q, want %q (must not be modified)", path, got, want)
		}
	}
}

// snapshotTree records, for every file under root, its path relative to root
// (OS separators) and its bytes. Directories themselves are not recorded.
func snapshotTree(t *testing.T, root string) (present map[string]bool, content map[string][]byte) {
	t.Helper()
	present = map[string]bool{}
	content = map[string][]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		present[rel] = true
		content[rel] = b
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotTree(%q): %v", root, err)
	}
	return present, content
}
