package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filetoaster/internal/auth"
	"filetoaster/internal/config"
	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/protocol"
	"filetoaster/internal/service"
)

// searchEndpoint bundles the auth-wrapped search handler with the service, wired
// the same way serve.go wires them, so tests exercise the real protection and
// task lifecycle. `root` is the single allowed folder the search may reference.
type searchEndpoint struct {
	Handler http.Handler
	Svc     *service.Service
}

func newSearchEndpoint(t *testing.T, root string) searchEndpoint {
	t.Helper()
	cfg := &config.Config{
		AllowedPaths:     []config.AllowedPath{{Name: "Files", Path: root}},
		MaxParallelTasks: 4,
	}
	fsCfg := newFSConfig(cfg)
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	svc := service.NewService(cfg.MaxParallelTasks, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-instance")
	registerSearchOp(svc, roots)
	taskSrv := protocol.NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	guard := newTestGuard(t)
	return searchEndpoint{
		Handler: guard.Middleware(http.HandlerFunc(taskSrv.ServeSearch)),
		Svc:     svc,
	}
}

func searchRequestBody(root, query string) io.Reader {
	b, _ := json.Marshal(ops.SearchRequest{RootPath: root, Query: query})
	return bytes.NewReader(b)
}

// TestServeSearchRequiresAuth verifies the search endpoint is protected by the
// auth middleware: no cookie and a tampered cookie are rejected with 401, and a
// valid signed cookie is accepted with 202 and a task id.
func TestServeSearchRequiresAuth(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("no cookie is rejected", func(t *testing.T) {
		ep := newSearchEndpoint(t, root)
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/search", searchRequestBody(root, "a")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("tampered cookie is rejected", func(t *testing.T) {
		ep := newSearchEndpoint(t, root)
		req := httptest.NewRequest(http.MethodPost, "/api/search", searchRequestBody(root, "a"))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "not-a-real-token"})
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid cookie is accepted", func(t *testing.T) {
		ep := newSearchEndpoint(t, root)
		req := httptest.NewRequest(http.MethodPost, "/api/search", searchRequestBody(root, "a"))
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			TaskID string `json:"taskID"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.TaskID == "" {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
	})
}

// TestServeSearchValidation verifies validation failures map to the right HTTP
// status: a blank root and a filesystem escape are rejected before any task runs.
func TestServeSearchValidation(t *testing.T) {
	root := t.TempDir()

	cases := []struct {
		name string
		body string
		want int
	}{
		{"blank root", `{"query":"a"}`, http.StatusBadRequest},
		{"empty criteria", `{"rootPath":"` + root + `","query":"  "}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep := newSearchEndpoint(t, root)
			req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader([]byte(tc.body)))
			req.AddCookie(signedSession(t, newTestGuard(t)))
			rec := httptest.NewRecorder()
			ep.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	t.Run("escape outside roots is rejected", func(t *testing.T) {
		ep := newSearchEndpoint(t, root)
		req := httptest.NewRequest(http.MethodPost, "/api/search", searchRequestBody("/etc", "passwd"))
		req.AddCookie(signedSession(t, newTestGuard(t)))
		rec := httptest.NewRecorder()
		ep.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
		}
	})
}

// TestServeSearchStreamsResults verifies a submitted search enqueues a "search"
// task and streams its matches to SSE subscribers as search-result events,
// scoped to the created task id.
func TestServeSearchStreamsResults(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "match-one.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ep := newSearchEndpoint(t, root)
	// Subscribe before submitting so no event is missed.
	sub := ep.Svc.Subscribe(nil)
	defer ep.Svc.Hub().Unsubscribe(sub)

	req := httptest.NewRequest(http.MethodPost, "/api/search", searchRequestBody(root, "match"))
	req.AddCookie(signedSession(t, newTestGuard(t)))
	rec := httptest.NewRecorder()
	ep.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		TaskID string `json:"taskID"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.TaskID == "" {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}

	if tsk := ep.Svc.Get(resp.TaskID); tsk == nil || tsk.Type != service.SearchOperation {
		t.Fatalf("task = %+v, want type %q", tsk, service.SearchOperation)
	}

	var names []string
	complete := false
	deadline := time.After(6 * time.Second)
	for !complete {
		select {
		case ev := <-sub.Events():
			if ev == nil {
				t.Fatal("event channel closed before completion")
			}
			if ev.TaskID != resp.TaskID {
				continue
			}
			switch ev.Name {
			case "search-result":
				var r ops.SearchResult
				if err := json.Unmarshal(ev.Data, &r); err != nil {
					t.Fatalf("search-result payload: %v", err)
				}
				names = append(names, r.Name)
			case "search-complete":
				complete = true
			}
		case <-deadline:
			t.Fatalf("search did not complete (results so far: %v)", names)
		}
	}

	if len(names) != 1 || names[0] != "match-one.txt" {
		t.Fatalf("results = %v, want [match-one.txt]", names)
	}
}
