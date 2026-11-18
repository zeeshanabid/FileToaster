package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"filetoaster/internal/config"
)

// buildInfoFixture creates a nested tree and returns an fsConfig referencing it:
//
//	root
//	  a.txt           (50 bytes apparent)
//	  b.txt           (70 bytes apparent)
//	  sub/            (dir)
//	    c.txt         (30 bytes apparent)
//	    deep/         (dir)
//	      d.txt       (10 bytes apparent)
//
// So `root` has apparent recursive size 160, 4 files and 2 sub-directories. Its
// reported size is the on-disk footprint (allocated blocks, du-style).
func buildInfoFixture(t *testing.T) fsConfig {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel string, n int) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("a.txt", 50)
	mustWrite("b.txt", 70)
	mustWrite(filepath.Join("sub", "c.txt"), 30)
	mustWrite(filepath.Join("sub", "deep", "d.txt"), 10)
	return fsConfig{AllowedPaths: []config.AllowedPath{{Name: "Files", Path: root}}}
}

// blockUsage returns the on-disk size (allocated 512-byte blocks, du-style) of p.
func blockUsage(p string) int64 {
	info, err := os.Lstat(p)
	if err != nil {
		panic(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size()
	}
	return int64(st.Blocks) * 512
}

func infoBody(paths ...string) *bytes.Reader {
	b, _ := json.Marshal(fsInfoRequest{Paths: paths})
	return bytes.NewReader(b)
}

func decodeInfo(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Items
}

// findItem returns the item whose path equals target, or nil.
func findItem(items []map[string]any, target string) map[string]any {
	for _, it := range items {
		if it["path"] == target {
			return it
		}
	}
	return nil
}

func TestServeFsInfoDirRecursive(t *testing.T) {
	cfg := buildInfoFixture(t)
	root := cfg.AllowedPaths[0].Path
	rec := httptest.NewRecorder()
	serveFsInfo(rec, httptest.NewRequest(http.MethodPost, "/api/fs/info", infoBody(root)), cfg)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	items := decodeInfo(t, rec)
	it := findItem(items, filepath.Clean(root))
	if it == nil {
		t.Fatal("root item missing")
	}
	if it["type"] != "dir" {
		t.Errorf("type = %v, want dir", it["type"])
	}
	// On-disk footprint: each directory's own blocks plus the allocated blocks of
	// every file beneath it (du-style), not the apparent 160 bytes.
	want := blockUsage(root) + blockUsage(filepath.Join(root, "sub")) +
		blockUsage(filepath.Join(root, "sub", "deep"))
	for _, f := range []string{"a.txt", "b.txt", "sub/c.txt", "sub/deep/d.txt"} {
		want += blockUsage(filepath.Join(root, f))
	}
	if it["size"].(float64) != float64(want) {
		t.Errorf("size = %v, want on-disk %v", it["size"], want)
	}
	if it["fileCount"].(float64) != 4 {
		t.Errorf("fileCount = %v, want 4", it["fileCount"])
	}
	if it["dirCount"].(float64) != 2 {
		t.Errorf("dirCount = %v, want 2", it["dirCount"])
	}
	perms, _ := it["perms"].(string)
	if !strings.HasPrefix(perms, "d") {
		t.Errorf("perms = %q, want to start with 'd'", perms)
	}
	oct, _ := it["permsOctal"].(string)
	if oct != "0755" {
		t.Errorf("permsOctal = %q, want 0755", oct)
	}
}

func TestServeFsInfoFile(t *testing.T) {
	cfg := buildInfoFixture(t)
	a := filepath.Join(cfg.AllowedPaths[0].Path, "a.txt")
	rec := httptest.NewRecorder()
	serveFsInfo(rec, httptest.NewRequest(http.MethodPost, "/api/fs/info", infoBody(a)), cfg)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	items := decodeInfo(t, rec)
	it := findItem(items, filepath.Clean(a))
	if it == nil {
		t.Fatal("a.txt item missing")
	}
	if it["type"] != "file" {
		t.Errorf("type = %v, want file", it["type"])
	}
	// A file reports its allocated footprint, so a 50-byte file counts a full
	// block rather than its 50 apparent bytes.
	want := blockUsage(a)
	if it["size"].(float64) != float64(want) {
		t.Errorf("size = %v, want on-disk %v", it["size"], want)
	}
	if it["fileCount"].(float64) != 1 {
		t.Errorf("fileCount = %v, want 1", it["fileCount"])
	}
	if it["dirCount"].(float64) != 0 {
		t.Errorf("dirCount = %v, want 0", it["dirCount"])
	}
	perms, _ := it["perms"].(string)
	if !strings.HasPrefix(perms, "-") {
		t.Errorf("perms = %q, want to start with '-'", perms)
	}
}

func TestServeFsInfoMultiple(t *testing.T) {
	cfg := buildInfoFixture(t)
	root := filepath.Clean(cfg.AllowedPaths[0].Path)
	a := filepath.Join(root, "a.txt")
	rec := httptest.NewRecorder()
	serveFsInfo(rec, httptest.NewRequest(http.MethodPost, "/api/fs/info", infoBody(root, a)), cfg)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	items := decodeInfo(t, rec)
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
}

func TestServeFsInfoSkipsUnresolvable(t *testing.T) {
	cfg := buildInfoFixture(t)
	root := filepath.Clean(cfg.AllowedPaths[0].Path)
	// A path that exists but lies outside the allowed roots is skipped silently.
	outside := filepath.Join(filepath.Dir(root), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	serveFsInfo(rec, httptest.NewRequest(http.MethodPost, "/api/fs/info", infoBody(root, outside)), cfg)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	items := decodeInfo(t, rec)
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1 (outside skipped)", len(items))
	}
}

func TestServeFsInfoValidates(t *testing.T) {
	cfg := buildInfoFixture(t)
	for _, tc := range []struct {
		name     string
		body     io.Reader
		wantCode int
	}{{
		name:     "empty paths",
		body:     bytes.NewReader([]byte(`{"paths":[]}`)),
		wantCode: http.StatusBadRequest,
	}, {
		name:     "malformed json",
		body:     bytes.NewReader([]byte(`not-json`)),
		wantCode: http.StatusBadRequest,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			serveFsInfo(rec, httptest.NewRequest(http.MethodPost, "/api/fs/info", tc.body), cfg)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}
