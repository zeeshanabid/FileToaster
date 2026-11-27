package ops

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

// collectSearch runs a plan and returns the streamed results plus whether the
// terminal "search-complete" event was emitted. A fresh sink captures the
// events, mirroring how the pool wires a Progression for a real task.
func collectSearch(t *testing.T, ctx context.Context, plan *SearchPlan) ([]SearchResult, bool) {
	t.Helper()
	prog := &task.Progression{}
	var results []SearchResult
	complete := false
	prog.SetEventSink(func(name string, data []byte) {
		switch name {
		case "search-result":
			var r SearchResult
			if err := json.Unmarshal(data, &r); err != nil {
				t.Errorf("search-result payload: %v", err)
				return
			}
			results = append(results, r)
		case "search-complete":
			complete = true
		}
	})
	if err := SearchRun(ctx, plan, prog); err != nil {
		t.Fatalf("SearchRun: %v", err)
	}
	return results, complete
}

func names(results []SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return out
}

func hasName(results []SearchResult, name string) bool {
	for _, r := range results {
		if r.Name == name {
			return true
		}
	}
	return false
}

// --- Resolve ---------------------------------------------------------------

func TestSearchResolveValidation(t *testing.T) {
	root := writeTree(t, map[string]string{"a.txt": "x"})
	roots := rootsFor(t, root)

	cases := []struct {
		name string
		req  SearchRequest
		want error
	}{
		{"empty root", SearchRequest{Query: "a"}, SearchErrEmptyRoot},
		{"empty criteria", SearchRequest{RootPath: root}, SearchErrEmptyCriteria},
		{"outside roots", SearchRequest{RootPath: "/etc", Query: "a"}, fs.ErrOutsideRoots},
		{"invalid size range", SearchRequest{RootPath: root, Query: "a", MinSize: 10, MaxSize: 5}, SearchErrInvalidSizeRange},
		{"invalid date", SearchRequest{RootPath: root, Query: "a", ModifiedAfter: "not-a-date"}, SearchErrInvalidDate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SearchResolve(roots, tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSearchResolveRootMustBeDirectory(t *testing.T) {
	root := writeTree(t, map[string]string{"a.txt": "x"})
	_, err := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: filepath.Join(root, "a.txt"), Query: "a"})
	if !errors.Is(err, SearchErrNotDirectory) {
		t.Fatalf("err = %v, want SearchErrNotDirectory", err)
	}
}

func TestSearchResolveNormalizesFilters(t *testing.T) {
	root := writeTree(t, map[string]string{"a.txt": "x"})
	plan, err := SearchResolve(rootsFor(t, root), SearchRequest{
		RootPath:   root,
		Query:      "  A  ",
		MimeTypes:  []string{" IMAGE/* ", "image/*"},
		Extensions: []string{".TXT", "txt", " .Log "},
		MinSize:    -5,
	})
	if err != nil {
		t.Fatalf("SearchResolve: %v", err)
	}
	if plan.Query != "A" {
		t.Errorf("query = %q, want A", plan.Query)
	}
	if len(plan.MimeTypes) != 1 || plan.MimeTypes[0] != "image/*" {
		t.Errorf("mimeTypes = %v", plan.MimeTypes)
	}
	if len(plan.Extensions) != 2 || plan.Extensions[0] != "txt" || plan.Extensions[1] != "log" {
		t.Errorf("extensions = %v", plan.Extensions)
	}
	if plan.MinSize != 0 {
		t.Errorf("minSize = %d, want 0 (negative clamped)", plan.MinSize)
	}
}

// --- Run: name matching -----------------------------------------------------

func TestSearchRunCaseInsensitiveSubstring(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Hello.txt":       "x",
		"HELLO-world.txt": "x",
		"other.txt":       "x",
	})
	plan, err := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	results, complete := collectSearch(t, context.Background(), plan)
	if !complete {
		t.Fatal("search-complete not emitted")
	}
	if len(results) != 2 {
		t.Fatalf("results = %v, want 2 matches", names(results))
	}
	if !hasName(results, "Hello.txt") || !hasName(results, "HELLO-world.txt") {
		t.Fatalf("results = %v", names(results))
	}
}

func TestSearchRunDirectoryMatchesByQuery(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Photos/vacation.jpg": "x",
		"notes.txt":           "x",
	})
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "photo"})
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 1 || results[0].Type != "dir" || results[0].Name != "Photos" {
		t.Fatalf("results = %v, want the Photos directory", results)
	}
}

func TestSearchRunFileFilterExcludesDirectories(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Photos/notes.txt": "x",
		"notes.txt":        "x",
	})
	// A size filter is active, so the Photos directory (which also matches the
	// name "notes" indirectly? no) must be excluded even if its name matched.
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "notes", MinSize: 0, MaxSize: 0, Extensions: []string{"txt"}})
	results, _ := collectSearch(t, context.Background(), plan)
	for _, r := range results {
		if r.Type == "dir" {
			t.Fatalf("directory %q returned despite a file filter", r.Name)
		}
	}
}

// --- Run: extension, MIME, size, date filters -------------------------------

func TestSearchRunExtensionFilter(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.txt": "x",
		"b.log": "x",
		"c.MD":  "x",
	})
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "a", Extensions: []string{".TXT"}})
	results, _ := collectSearch(t, context.Background(), plan)
	// Query "a" matches only names containing "a"; among those only a.txt is a
	// .txt file. a.txt contains "a", so it matches.
	if !hasName(results, "a.txt") || hasName(results, "b.log") {
		t.Fatalf("results = %v", names(results))
	}
}

func TestSearchRunMimePrefixFilter(t *testing.T) {
	root := writeTree(t, map[string]string{
		"photo.jpg":  "x",
		"report.pdf": "x",
		"notes.txt":  "x",
	})
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "", MimeTypes: []string{"image/*"}})
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 1 || results[0].Name != "photo.jpg" {
		t.Fatalf("results = %v, want only photo.jpg", names(results))
	}
	if results[0].MimeType != "image/jpeg" {
		t.Errorf("mimeType = %q, want image/jpeg", results[0].MimeType)
	}
}

func TestSearchRunMimeExactFilter(t *testing.T) {
	root := writeTree(t, map[string]string{"doc.pdf": "x", "img.png": "x"})
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, MimeTypes: []string{"application/pdf"}})
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 1 || results[0].Name != "doc.pdf" {
		t.Fatalf("results = %v, want only doc.pdf", names(results))
	}
}

func TestSearchRunSizeFilter(t *testing.T) {
	root := writeTree(t, map[string]string{
		"small.txt": "12345",      // 5 bytes
		"big.txt":   "1234567890", // 10 bytes
	})
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, MinSize: 6})
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 1 || results[0].Name != "big.txt" {
		t.Fatalf("results = %v, want only big.txt", names(results))
	}
}

func TestSearchRunModifiedDateFilter(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old.txt")
	newer := filepath.Join(root, "new.txt")
	if err := os.WriteFile(old, []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	plan, err := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, ModifiedAfter: "2022-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 1 || results[0].Name != "new.txt" {
		t.Fatalf("results = %v, want only new.txt", names(results))
	}
}

func TestSearchRunCombinedFiltersAndLogic(t *testing.T) {
	root := writeTree(t, map[string]string{
		"note-log.txt":   "1234567890", // 10 bytes, .txt, has note
		"note-log.md":    "1234567890", // .md, excluded by extension
		"note-short.txt": "1",          // too small
		"other-log.txt":  "1234567890", // name missing "note"
	})
	plan, err := SearchResolve(rootsFor(t, root), SearchRequest{
		RootPath:   root,
		Query:      "note",
		Extensions: []string{"txt"},
		MinSize:    5,
	})
	if err != nil {
		t.Fatal(err)
	}
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 1 || results[0].Name != "note-log.txt" {
		t.Fatalf("results = %v, want only note-log.txt", names(results))
	}
}

func TestSearchRunRecursesIntoSubdirectories(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/b/c/deep-target.txt": "x",
		"top-target.txt":        "x",
	})
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "target"})
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 2 {
		t.Fatalf("results = %v, want 2", names(results))
	}
}

func TestSearchRunDoesNotFollowSymlinkedDirectories(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	plan, err := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	results, _ := collectSearch(t, context.Background(), plan)
	if len(results) != 0 {
		t.Fatalf("results = %v, want none (symlinked dir must not be followed)", names(results))
	}
}

func TestSearchRunCancellation(t *testing.T) {
	root := writeTree(t, map[string]string{"a.txt": "x", "b.txt": "y"})
	plan, _ := SearchResolve(rootsFor(t, root), SearchRequest{RootPath: root, Query: "txt"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SearchRun(ctx, plan, &task.Progression{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
