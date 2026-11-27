package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filetoaster/internal/fs"
	"filetoaster/internal/fs/ops"
	"filetoaster/internal/service"
	"filetoaster/internal/task"
)

// newSearchProtocolServer builds a protocol server with the search operation
// wired the same way cmd/server wires it, rooted at the given allowed folder.
func newSearchProtocolServer(root string) (*Server, *service.Service) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.NewService(4, logger, "test-instance")
	roots := func() *fs.Roots { return fs.NewRoots(root) }
	svc.Register(service.SearchOperation, service.Op{
		Prepare: func(body []byte) (any, []byte, error) {
			var req ops.SearchRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, nil, err
			}
			plan, err := ops.SearchResolve(roots(), req)
			if err != nil {
				return nil, nil, err
			}
			meta, _ := json.Marshal(plan)
			return plan, meta, nil
		},
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			return ops.SearchRun(ctx, spec.(*ops.SearchPlan), prog)
		},
	})
	return NewServer(svc, logger), svc
}

// TestServeEventsStreamsSearchResults verifies the SSE endpoint emits
// "search-result" events (wrapped in the {taskID, data} envelope) while a search
// task runs, then a "search-complete" event.
func TestServeEventsStreamsSearchResults(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _ := newSearchProtocolServer(root)

	events := httptest.NewServer(http.HandlerFunc(srv.ServeEvents))
	defer events.Close()

	resp, err := http.Get(events.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	// Submit the search via the service (equivalent to POST /api/search).
	body, _ := json.Marshal(ops.SearchRequest{RootPath: root, Query: "target"})
	if _, err := srv.svc.Submit(service.SearchOperation, body); err != nil {
		t.Fatalf("submit: %v", err)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventName, data string
	sawResult := false
	sawComplete := false
	deadline := time.Now().Add(6 * time.Second)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "":
			if eventName == "search-result" {
				var env struct {
					TaskID string          `json:"taskID"`
					Data   json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal([]byte(data), &env); err != nil {
					t.Fatalf("envelope decode: %v (data %s)", err, data)
				}
				if env.TaskID == "" {
					t.Fatal("search-result envelope missing taskID")
				}
				var r ops.SearchResult
				if err := json.Unmarshal(env.Data, &r); err != nil || r.Name != "target.txt" {
					t.Fatalf("result decode: %v name=%q", err, r.Name)
				}
				sawResult = true
			}
			if eventName == "search-complete" {
				sawComplete = true
			}
			eventName, data = "", ""
		}
		if sawResult && sawComplete {
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}
	if !sawResult {
		t.Fatal("no search-result event received")
	}
	if !sawComplete {
		t.Fatal("no search-complete event received")
	}
}
