package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"filetoaster/internal/task"
)

func newTestService() *Service {
	return NewService(1, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// blockingOp returns an Op whose Run blocks until its context is cancelled.
func blockingOp() Op {
	return Op{
		Prepare: func(body []byte) (any, []byte, error) { return nil, nil, nil },
		Run: func(ctx context.Context, spec any, prog *task.Progression) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
}

// noopOp returns an Op whose Run completes immediately.
func noopOp() Op {
	return Op{
		Prepare: func(body []byte) (any, []byte, error) { return nil, nil, nil },
		Run:     func(ctx context.Context, spec any, prog *task.Progression) error { return nil },
	}
}

func waitForState(t *testing.T, svc *Service, id string, want task.Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := svc.Get(id); s != nil && s.Status == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	got := task.Status("<nil>")
	if s := svc.Get(id); s != nil {
		got = s.Status
	}
	t.Fatalf("task %s status = %q, want %q", id, got, want)
}

// TestServiceShutdownDrainsAndForceCancels covers Service.Shutdown(ctx): the
// running task drains, and if it does not finish before the deadline it is
// force-cancelled and Shutdown returns promptly.
func TestServiceShutdownDrainsAndForceCancels(t *testing.T) {
	svc := newTestService()
	svc.Register("block", blockingOp())

	t1, err := svc.Submit("block", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitForState(t, svc, t1.ID, task.StatusRunning)

	shutdownDone := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		svc.Shutdown(ctx)
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not honour the timeout")
	}
	waitForState(t, svc, t1.ID, task.StatusCancelled)
}

// TestServiceSubmitAfterShutdown covers the documented contract that new
// operations are rejected once the pool has shut down.
func TestServiceSubmitAfterShutdown(t *testing.T) {
	svc := newTestService()
	svc.Register("noop", noopOp())

	if _, err := svc.Submit("noop", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	svc.Shutdown(context.Background())

	_, err := svc.Submit("noop", nil)
	if !errors.Is(err, task.ErrShuttingDown) {
		t.Fatalf("Submit after shutdown err = %v, want ErrShuttingDown", err)
	}
}

// TestServiceShutdownClosesHub verifies the SSE hub is closed as part of
// service shutdown so the HTTP server can release its (never-idle) SSE streams.
func TestServiceShutdownClosesHub(t *testing.T) {
	svc := newTestService()
	svc.Hub().Subscribe(nil)
	if svc.Hub().Active() != 1 {
		t.Fatalf("active = %d, want 1", svc.Hub().Active())
	}
	svc.Shutdown(context.Background())
	if svc.Hub().Active() != 0 {
		t.Fatalf("hub still active after shutdown: %d", svc.Hub().Active())
	}
}
