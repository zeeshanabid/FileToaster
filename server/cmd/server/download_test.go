package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"filetoaster/internal/auth"
	"filetoaster/internal/config"
	"filetoaster/internal/download"
	"filetoaster/internal/fs"
)

// downloadEndpoints wires the two download handlers the way serve.go does: each
// behind the auth middleware, sharing one token registry and one roots factory so
// the tests exercise the real protection and the token round-trip.
type downloadEndpoints struct {
	Post http.Handler
	Get  http.Handler
	Reg  *download.Registry
}

func newDownloadEndpoints(t *testing.T, root string) downloadEndpoints {
	t.Helper()
	cfg := &config.Config{
		AllowedPaths:     []config.AllowedPath{{Name: "Files", Path: root}},
		MaxParallelTasks: 4,
	}
	fsCfg := newFSConfig(cfg)
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	reg := download.NewRegistry()
	dlCfg := newDownloadConfig(roots, reg)
	guard := newTestGuard(t)
	return downloadEndpoints{
		Post: guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveDownloads(w, r, dlCfg)
		})),
		Get: guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveDownloadToken(w, r, dlCfg)
		})),
		Reg: reg,
	}
}

// downloadPostBody marshals a download POST body (a single or multiple paths).
func downloadPostBody(paths ...string) *bytes.Reader {
	b, _ := json.Marshal(map[string]any{"paths": paths})
	return bytes.NewReader(b)
}

// downloadTokenReq builds a GET /downloads/{token} request with the path value set
// (as the ServeMux would).
func downloadTokenReq(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/downloads/"+token, nil)
	req.SetPathValue("token", token)
	return req
}

// TestServeDownloadsRequiresAuth verifies the download POST is protected: a missing
// or tampered cookie is rejected with 401, and a valid cookie is accepted.
func TestServeDownloadsRequiresAuth(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "secret.txt"), "payload")
	ep := newDownloadEndpoints(t, root)

	t.Run("no cookie is rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ep.Post.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(filepath.Join(root, "secret.txt"))))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("tampered cookie is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(filepath.Join(root, "secret.txt")))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-cookie"})
		rec := httptest.NewRecorder()
		ep.Post.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid cookie is accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(filepath.Join(root, "secret.txt")))
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Post.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.Token == "" {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		if len(resp.Token) != download.TokenLength*2 {
			t.Fatalf("token length = %d, want %d (fixed)", len(resp.Token), download.TokenLength*2)
		}
	})
}

// TestDownloadTokenRoundTrip verifies a full POST→GET flow: the token streams the
// exact file bytes with the right headers, then the token is consumed (a second
// GET misses) so it cannot be reused.
func TestDownloadTokenRoundTrip(t *testing.T) {
	root := t.TempDir()
	content := []byte("download me — a single file stream")
	writeFile(t, filepath.Join(root, "report.bin"), string(content))
	ep := newDownloadEndpoints(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(filepath.Join(root, "report.bin")))
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Post.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	getReq := downloadTokenReq(resp.Token)
	getReq.AddCookie(signedSession(t, newTestGuard(t)))
	getRec := httptest.NewRecorder()
	ep.Get.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body %s)", getRec.Code, getRec.Body.String())
	}
	if got := getRec.Body.String(); got != string(content) {
		t.Fatalf("downloaded body = %q, want %q", got, content)
	}
	// The streamed file size and a filename attachment are advertised.
	if n, err := strconv.Atoi(getRec.Header().Get("Content-Length")); err != nil || n != len(content) {
		t.Errorf("Content-Length = %q, want %d", getRec.Header().Get("Content-Length"), len(content))
	}
	if cd := getRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "report.bin") {
		t.Errorf("Content-Disposition = %q, want attachment filename=report.bin", cd)
	}
	// The token is consumed: a second GET is a 404, and the registry is empty.
	getReq2 := downloadTokenReq(resp.Token)
	getReq2.AddCookie(signedSession(t, newTestGuard(t)))
	getRec2 := httptest.NewRecorder()
	ep.Get.ServeHTTP(getRec2, getReq2)
	if getRec2.Code != http.StatusNotFound {
		t.Errorf("second GET status = %d, want 404 (token should be consumed)", getRec2.Code)
	}
	if ep.Reg.Size() != 0 {
		t.Errorf("registry size = %d, want 0 (token consumed)", ep.Reg.Size())
	}
}

// TestServeDownloadsRejectsNonFile verifies directories and multi-path selections
// are rejected with 400, and an empty selection too, so a folder is never streamed.
func TestServeDownloadsRejectsNonFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "1")
	writeFile(t, filepath.Join(root, "b.txt"), "2")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	ep := newDownloadEndpoints(t, root)
	cookie := signedSession(t, newTestGuard(t))

	cases := []struct {
		name  string
		paths []string
	}{
		{"directory", []string{filepath.Join(root, "sub")}},
		{"multiple files", []string{filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")}},
		{"empty selection", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(tc.paths...))
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			ep.Post.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestServeDownloadsRejectsEscape verifies a path outside the allowed roots is
// rejected with 403 and no token is issued.
func TestServeDownloadsRejectsEscape(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "inside.txt"), "ok")
	outside := filepath.Join(filepath.Dir(root), "outside")
	writeFile(t, filepath.Join(outside, "outside.txt"), "leak")

	ep := newDownloadEndpoints(t, root)
	req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(filepath.Join(outside, "outside.txt")))
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Post.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if ep.Reg.Size() != 0 {
		t.Errorf("token issued for an out-of-roots file: registry size = %d", ep.Reg.Size())
	}
}

// TestServeDownloadsRejectsTraversal verifies a parent-relative path (../) that
// escapes the roots is rejected with 403 — the POST confinement backstops the
// token design, which otherwise guarantees no path reaches the GET endpoint.
func TestServeDownloadsRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "1")
	outside := filepath.Join(filepath.Dir(root), "outside")
	writeFile(t, filepath.Join(outside, "outside.txt"), "leak")
	ep := newDownloadEndpoints(t, root)
	// Reach the existing outside file through ".." rather than a clean absolute
	// path, so the request exercises the traversal canonicalization too.
	escape := filepath.Join(root, "..", "outside", "outside.txt")

	req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(escape))
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Post.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if ep.Reg.Size() != 0 {
		t.Errorf("token issued for an out-of-roots file: registry size = %d", ep.Reg.Size())
	}
}

// TestServeDownloadTokenRequiresAuthAndUnknown verifies the streaming GET is
// authenticated and that an unknown/absent token is a 404 (no path is revealed).
func TestServeDownloadTokenRequiresAuthAndUnknown(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "1")
	ep := newDownloadEndpoints(t, root)

	t.Run("no cookie is rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ep.Get.ServeHTTP(rec, downloadTokenReq("some-token"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("unknown token is 404 with a cookie", func(t *testing.T) {
		req := downloadTokenReq("unknown-token")
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Get.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
		}
	})
}

// TestServeDownloadTokenIgnoresPathSegments verifies the streaming route takes
// only the single {token} segment: a value carrying path separators still reads
// as one opaque token and therefore misses — a traversal attempt cannot inject a
// filesystem path into the download.
func TestServeDownloadTokenIgnoresPathSegments(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "1")
	ep := newDownloadEndpoints(t, root)

	req := httptest.NewRequest(http.MethodGet, "/downloads/.."+string(filepath.Separator)+".."+string(filepath.Separator)+"passwd", nil)
	req.SetPathValue("token", ".."+string(filepath.Separator)+".."+string(filepath.Separator)+"passwd")
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Get.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (segment injection must not resolve a file)", rec.Code)
	}
}

// TestServeDownloadsRejectsParentTraversal verifies a multi-parent traversal
// ("../../") that climbs out of the allowed roots is rejected with 403 and no
// token is issued. EvalSymlinks cleans the ".." hops and the containment check
// then rejects the resolved out-of-roots path — the POST backstops the token
// design, which otherwise guarantees no client path reaches the GET endpoint.
func TestServeDownloadsRejectsParentTraversal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "1")
	// A file two ".." levels above root, so the path carries a genuine "../../".
	parent := filepath.Dir(filepath.Dir(root))
	outside := filepath.Join(parent, "outside")
	writeFile(t, filepath.Join(outside, "leak.txt"), "leak")
	ep := newDownloadEndpoints(t, root)

	escape := root + "/../../" + filepath.Base(outside) + "/leak.txt"
	req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(escape))
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Post.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if ep.Reg.Size() != 0 {
		t.Errorf("token issued for a traversal path: registry size = %d", ep.Reg.Size())
	}
}

// TestServeDownloadsRejectsSymlinkEscape verifies a symlink inside an allowed
// root that points at a file outside the roots is rejected with 403: Resolve
// canonicalizes through the symlink with EvalSymlinks, and the containment
// check then rejects the resolved out-of-roots target.
func TestServeDownloadsRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "1")
	outside := filepath.Join(filepath.Dir(root), "outside")
	writeFile(t, filepath.Join(outside, "leak.txt"), "leak")
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(filepath.Join(outside, "leak.txt"), link); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}

	ep := newDownloadEndpoints(t, root)
	req := httptest.NewRequest(http.MethodPost, "/api/downloads", downloadPostBody(link))
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Post.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (symlink escape; body %s)", rec.Code, rec.Body.String())
	}
	if ep.Reg.Size() != 0 {
		t.Errorf("token issued for a symlink-escape path: registry size = %d", ep.Reg.Size())
	}
}

// writeFile writes content to path, creating parents as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
