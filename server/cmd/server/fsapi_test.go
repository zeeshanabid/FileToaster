package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filetoaster/internal/auth"
	"filetoaster/internal/config"
)

// buildTestConfig creates a temp dir with a.txt (50 bytes) and sub/c.txt (70
// bytes) and returns an fsConfig referencing it.
func buildTestConfig(t *testing.T) fsConfig {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "c.txt"), make([]byte, 70), 0o644); err != nil {
		t.Fatal(err)
	}
	return fsConfig{AllowedPaths: []config.AllowedPath{{Name: "Files", Path: root}}}
}

// listingBody marshals a fsListRequest into a JSON request body.
func listingBody(path string) *bytes.Reader {
	b, _ := json.Marshal(fsListRequest{Path: path})
	return bytes.NewReader(b)
}

// newTestGuard builds a Guard with a known secret and password so tests can sign
// and verify session cookies.
func newTestGuard(t *testing.T) *auth.Guard {
	t.Helper()
	hash, err := auth.HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	guard, err := auth.NewGuard("test-secret-value", auth.DefaultUser, hash)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

// signedSession returns an httpOnly session cookie for the guard, as if the
// client had logged in.
func signedSession(t *testing.T, guard *auth.Guard) *http.Cookie {
	t.Helper()
	token, err := auth.Issue(guard.Secret, guard.User, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: token}
}

// fsListHandler serves both directory-listing endpoints the way serve.go wires
// them (each behind the auth middleware), so tests exercise the same protection.
type fsListHandler struct {
	cfg fsConfig
}

func (h *fsListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/fs/roots":
		serveRoots(w, r, h.cfg)
	default:
		serveFsList(w, r, h.cfg)
	}
}

func TestServeRoots(t *testing.T) {
	cfg := buildTestConfig(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/fs/roots", nil)
	serveRoots(rec, req, cfg)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		AllowedPaths []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"allowed_paths"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.AllowedPaths) != 1 {
		t.Fatalf("allowed_paths len = %d, want 1", len(body.AllowedPaths))
	}
	if body.AllowedPaths[0].Name != "Files" {
		t.Errorf("name = %q, want Files", body.AllowedPaths[0].Name)
	}
	if body.AllowedPaths[0].Path != filepath.Clean(cfg.AllowedPaths[0].Path) {
		t.Errorf("served path = %q, want %q", body.AllowedPaths[0].Path, filepath.Clean(cfg.AllowedPaths[0].Path))
	}
}

// TestServeRootsResolvesSymlink verifies a symlinked allowed path (e.g. /bin)
// is served at its resolved target (e.g. /usr/bin), so the UI opens the
// directory actually referenced and containment stays consistent.
func TestServeRootsResolvesSymlink(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	cfg := fsConfig{AllowedPaths: []config.AllowedPath{{Name: "Bin", Path: alias}}}

	rec := httptest.NewRecorder()
	serveRoots(rec, httptest.NewRequest(http.MethodGet, "/api/fs/roots", nil), cfg)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		AllowedPaths []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"allowed_paths"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.AllowedPaths) != 1 {
		t.Fatalf("allowed_paths len = %d, want 1", len(body.AllowedPaths))
	}
	if body.AllowedPaths[0].Name != "Bin" {
		t.Errorf("name = %q, want Bin", body.AllowedPaths[0].Name)
	}
	if body.AllowedPaths[0].Path != root {
		t.Errorf("served path = %q, want resolved %q", body.AllowedPaths[0].Path, root)
	}
}

func TestServeFsList(t *testing.T) {
	cfg := buildTestConfig(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fs", listingBody(cfg.AllowedPaths[0].Path))
	serveFsList(rec, req, cfg)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Path != filepath.Clean(cfg.AllowedPaths[0].Path) {
		t.Errorf("path = %q", body.Path)
	}
	if len(body.Entries) != 2 {
		t.Errorf("entries len = %d, want 2 (a.txt + sub)", len(body.Entries))
	}
}

func TestServeFsListRequiresPath(t *testing.T) {
	cfg := buildTestConfig(t)
	for _, tc := range []struct {
		name string
		body io.Reader
	}{{
		name: "empty body",
		body: http.NoBody,
	}, {
		name: "empty path field",
		body: bytes.NewReader([]byte(`{"path":""}`)),
	}, {
		name: "malformed json",
		body: bytes.NewReader([]byte(`not-json`)),
	}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			serveFsList(rec, httptest.NewRequest(http.MethodPost, "/api/fs", tc.body), cfg)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestServeFsListRejectsEscape(t *testing.T) {
	cfg := buildTestConfig(t)
	// A sibling directory that exists but lies outside the root must be rejected
	// with 403 (not 200, which would leak its contents).
	outside := filepath.Join(filepath.Dir(cfg.AllowedPaths[0].Path), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fs", listingBody(outside))
	serveFsList(rec, req, cfg)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestFsEndpointsRequireAuth verifies both directory-listing endpoints are
// protected by the auth middleware: requests without a valid session cookie are
// rejected with 401, and requests with an unexpired signed cookie succeed.
func TestFsEndpointsRequireAuth(t *testing.T) {
	guard := newTestGuard(t)
	cfg := buildTestConfig(t)
	routes := map[string]io.Reader{
		"/api/fs":       listingBody(cfg.AllowedPaths[0].Path),
		"/api/fs/roots": http.NoBody,
	}

	t.Run("no cookie is rejected", func(t *testing.T) {
		handler := guard.Middleware(&fsListHandler{cfg: cfg})
		for route, body := range routes {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, route, body))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s: status = %d, want 401 (body %s)", route, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("valid cookie is accepted", func(t *testing.T) {
		handler := guard.Middleware(&fsListHandler{cfg: cfg})
		for route, body := range routes {
			req := httptest.NewRequest(http.MethodPost, route, body)
			req.AddCookie(signedSession(t, guard))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200 (body %s)", route, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("tampered cookie is rejected", func(t *testing.T) {
		handler := guard.Middleware(&fsListHandler{cfg: cfg})
		req := httptest.NewRequest(http.MethodPost, "/api/fs", listingBody(cfg.AllowedPaths[0].Path))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})
}

// mkdirBody marshals a fsMkdirRequest into a JSON request body.
func mkdirBody(parent, name string) *bytes.Reader {
	b, _ := json.Marshal(fsMkdirRequest{Parent: parent, Name: name})
	return bytes.NewReader(b)
}

func TestServeFsMkdir(t *testing.T) {
	cfg := buildTestConfig(t)
	rec := httptest.NewRecorder()
	serveFsMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", mkdirBody(cfg.AllowedPaths[0].Path, "created")), cfg)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Path != filepath.Clean(filepath.Join(cfg.AllowedPaths[0].Path, "created")) {
		t.Errorf("path = %q", body.Path)
	}
	// The new directory is visible in a fresh listing of the parent.
	listRec := httptest.NewRecorder()
	serveFsList(listRec, httptest.NewRequest(http.MethodPost, "/api/fs", listingBody(cfg.AllowedPaths[0].Path)), cfg)
	if listRec.Code != http.StatusOK {
		t.Fatalf("listing status = %d", listRec.Code)
	}
	var listed struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range listed.Entries {
		if e.Name == "created" && e.Type == "dir" {
			found = true
		}
	}
	if !found {
		t.Errorf("created directory not listed in parent")
	}
}

func TestServeFsMkdirValidates(t *testing.T) {
	cfg := buildTestConfig(t)
	for _, tc := range []struct {
		name     string
		body     io.Reader
		wantCode int
	}{{
		name:     "missing fields",
		body:     bytes.NewReader([]byte(`{}`)),
		wantCode: http.StatusBadRequest,
	}, {
		name:     "empty name",
		body:     mkdirBody(cfg.AllowedPaths[0].Path, "  "),
		wantCode: http.StatusBadRequest,
	}, {
		name:     "conflict",
		body:     mkdirBody(cfg.AllowedPaths[0].Path, "sub"),
		wantCode: http.StatusConflict,
	}, {
		name:     "escape outside roots",
		body:     mkdirBody(filepath.Join(filepath.Dir(cfg.AllowedPaths[0].Path), "outside"), "x"),
		wantCode: http.StatusForbidden,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "escape outside roots" {
				// The outside parent must exist so Resolve reaches the containment
				// check (an unknown path fails resolution with 400 instead).
				if err := os.MkdirAll(filepath.Join(filepath.Dir(cfg.AllowedPaths[0].Path), "outside"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			rec := httptest.NewRecorder()
			serveFsMkdir(rec, httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", tc.body), cfg)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestServeFsMkdirRequiresAuth(t *testing.T) {
	guard := newTestGuard(t)
	cfg := buildTestConfig(t)
	t.Run("no cookie is rejected", func(t *testing.T) {
		handler := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveFsMkdir(w, r, cfg)
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", mkdirBody(cfg.AllowedPaths[0].Path, "x")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("valid cookie is accepted", func(t *testing.T) {
		handler := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveFsMkdir(w, r, cfg)
		}))
		req := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", mkdirBody(cfg.AllowedPaths[0].Path, "x"))
		req.AddCookie(signedSession(t, guard))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Errorf("status = %d, want 201", rec.Code)
		}
	})
}

// validatePathBody marshals a fsValidatePathRequest into a JSON request body.
func validatePathBody(path string) *bytes.Reader {
	b, _ := json.Marshal(fsValidatePathRequest{Path: path})
	return bytes.NewReader(b)
}

func TestServeFsValidatePath(t *testing.T) {
	cfg := buildTestConfig(t)
	guard := newTestGuard(t)
	cookie := signedSession(t, guard)
	handler := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFsValidatePath(w, r, cfg)
	}))

	root := cfg.AllowedPaths[0].Path
	sub := filepath.Join(root, "sub")

	t.Run("validates existing directory within root", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/fs/validate-path", validatePathBody(sub))
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Valid bool   `json:"valid"`
			Path  string `json:"path"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Valid {
			t.Error("valid = false, want true")
		}
		if body.Path != sub {
			t.Errorf("path = %q, want %q", body.Path, sub)
		}
	})

	t.Run("validates root directory itself", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/fs/validate-path", validatePathBody(root))
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Valid bool   `json:"valid"`
			Path  string `json:"path"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Valid {
			t.Error("valid = false, want true")
		}
		if body.Path != root {
			t.Errorf("path = %q, want %q", body.Path, root)
		}
	})

	t.Run("rejects path outside roots", func(t *testing.T) {
		outside := filepath.Join(filepath.Dir(root), "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/fs/validate-path", validatePathBody(outside))
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("rejects non-existent path", func(t *testing.T) {
		nonexistent := filepath.Join(root, "does-not-exist")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/fs/validate-path", validatePathBody(nonexistent))
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		// EvalSymlinks fails for non-existent paths, resulting in 400
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("rejects file path (not a directory)", func(t *testing.T) {
		filePath := filepath.Join(root, "a.txt")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/fs/validate-path", validatePathBody(filePath))
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("requires auth", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/fs/validate-path", validatePathBody(root))
		// No cookie
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("requires path field", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/fs/validate-path", bytes.NewReader([]byte(`{}`)))
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}
