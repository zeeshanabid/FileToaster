package task

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitFor polls cond until it returns true or the timeout elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestPoolBoundedConcurrency(t *testing.T) {
	store := NewStore()
	hub := NewHub()
	pool := NewPool(store, hub, 2, testLogger())
	defer pool.Close()

	var cur, maxRun int32
	release := make(chan struct{})
	runner := func(ctx context.Context, prog *Progression) error {
		c := atomic.AddInt32(&cur, 1)
		for {
			m := atomic.LoadInt32(&maxRun)
			if c > m && !atomic.CompareAndSwapInt32(&maxRun, m, c) {
				continue
			}
			break
		}
		<-release
		atomic.AddInt32(&cur, -1)
		return nil
	}

	const n = 4
	for i := 0; i < n; i++ {
		pool.Enqueue("t", nil, runner)
	}

	// At most `capacity` may run at once.
	waitFor(t, func() bool { return atomic.LoadInt32(&maxRun) == 2 })
	waitFor(t, func() bool { return pool.QueuedCount() == 2 })
	if got := atomic.LoadInt32(&cur); got != 2 {
		t.Fatalf("concurrent = %d, want 2", got)
	}
	close(release)

	// Wait for all tasks to reach a terminal state.
	waitFor(t, func() bool {
		done := 0
		for _, s := range store.List() {
			if s.Status.IsTerminal() {
				done++
			}
		}
		return done == n
	})
	if got := atomic.LoadInt32(&maxRun); got != 2 {
		t.Fatalf("maxRun = %d, want 2", got)
	}
}

func TestPoolQueueFIFOCompletion(t *testing.T) {
	store := NewStore()
	hub := NewHub()
	pool := NewPool(store, hub, 1, testLogger())

	firstDone := make(chan struct{})
	var order []string
	var mu sync.Mutex
	release := make(chan struct{})

	runners := []Runner{
		func(ctx context.Context, prog *Progression) error {
			<-release
			mu.Lock()
			order = append(order, "a")
			mu.Unlock()
			close(firstDone)
			return nil
		},
		func(ctx context.Context, prog *Progression) error {
			mu.Lock()
			order = append(order, "b")
			mu.Unlock()
			return nil
		},
	}
	t1, _ := pool.Enqueue("t", nil, runners[0])
	t2, _ := pool.Enqueue("t", nil, runners[1])

	// Let the first task run; the second must stay queued behind it.
	waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusRunning })
	if store.Get(t2.ID).Status != StatusQueued {
		t.Fatalf("task2 status = %q, want queued", store.Get(t2.ID).Status)
	}

	close(release)
	<-firstDone
	waitFor(t, func() bool { return store.Get(t2.ID).Status == StatusFinished })

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("execution order = %v, want [a b]", order)
	}
	pool.Close()
}

func TestPoolProgressReported(t *testing.T) {
	store := NewStore()
	hub := NewHub()
	pool := NewPool(store, hub, 1, testLogger())
	defer pool.Close()

	runner := func(ctx context.Context, prog *Progression) error {
		prog.SetTotal(100, 0)
		prog.AddBytes(60)
		return nil
	}
	t1, _ := pool.Enqueue("t", nil, runner)

	waitFor(t, func() bool {
		st := store.Get(t1.ID)
		return st != nil && st.Status == StatusFinished && st.Bytes.Done == 60 && st.Progress == 100
	})
}

func TestPoolCancelRunning(t *testing.T) {
	store := NewStore()
	hub := NewHub()
	pool := NewPool(store, hub, 1, testLogger())
	defer pool.Close()

	block := make(chan struct{})
	runner := func(ctx context.Context, prog *Progression) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
			return nil
		}
	}
	t1, _ := pool.Enqueue("t", nil, runner)
	waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusRunning })

	if err := pool.Cancel(t1.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusCancelled })
}

func TestPoolCancelQueued(t *testing.T) {
	store := NewStore()
	hub := NewHub()
	pool := NewPool(store, hub, 1, testLogger())
	defer pool.Close()

	release := make(chan struct{})
	var ran int32
	runner := func(ctx context.Context, prog *Progression) error {
		atomic.AddInt32(&ran, 1)
		<-release
		return nil
	}
	t1, _ := pool.Enqueue("t", nil, runner)
	t2, _ := pool.Enqueue("t", nil, runner)
	waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusRunning })

	if err := pool.Cancel(t2.ID); err != nil {
		t.Fatalf("Cancel queued: %v", err)
	}
	close(release)
	waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusFinished })
	pool.Close()

	if got := atomic.LoadInt32(&ran); got != 1 {
		t.Fatalf("runners executed = %d, want 1", got)
	}
	if store.Get(t2.ID).Status != StatusCancelled {
		t.Fatalf("queued status = %q, want cancelled", store.Get(t2.ID).Status)
	}
}

func TestPoolCancelUnknown(t *testing.T) {
	pool := NewPool(NewStore(), NewHub(), 1, testLogger())
	defer pool.Close()
	if err := pool.Cancel("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel unknown err = %v, want ErrNotFound", err)
	}
}

func TestPoolShutdownDrainsAndCancelsQueued(t *testing.T) {
	store := NewStore()
	hub := NewHub()
	pool := NewPool(store, hub, 1, testLogger())

	release := make(chan struct{})
	runner := func(ctx context.Context, prog *Progression) error {
		<-release
		return nil
	}
	t1, _ := pool.Enqueue("t", nil, runner)
	t2, _ := pool.Enqueue("t", nil, runner)
	waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusRunning })

	go func() { pool.Close() }()
	// The queued task is marked cancelled during shutdown.
	waitFor(t, func() bool { return store.Get(t2.ID).Status == StatusCancelled })
	close(release)
	// Running task finishes.
	waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusFinished })
}
