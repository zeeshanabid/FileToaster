package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"filetoaster/internal/protocol"
	"filetoaster/internal/service"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestShutdownClosesLiveSSEStream proves the graceful-shutdown delay is fixed:
// with an active SSE subscription, Service.Shutdown must return promptly instead
// of blocking on the never-idle SSE connection (which previously hung until the
// HTTP shutdown deadline expired). It also verifies the client's stream tears
// down shortly after.
func TestShutdownClosesLiveSSEStream(t *testing.T) {
	svc := service.NewService(1, discardLogger())
	taskSrv := protocol.NewServer(svc, discardLogger())

	mux := http.NewServeMux()
	mux.Handle("GET /api/events", http.HandlerFunc(taskSrv.ServeEvents))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := srv.Client()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("open SSE connection: %v", err)
	}
	defer resp.Body.Close()

	// Let the handler register its subscription before shutting down.
	time.Sleep(100 * time.Millisecond)

	shutdownDone := make(chan struct{})
	go func() {
		svc.Shutdown(context.Background())
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown hung on an active SSE connection")
	}

	done := make(chan error, 1)
	go func() {
		_, readErr := io.Copy(io.Discard, resp.Body)
		done <- readErr
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE stream did not close after shutdown")
	}
}
