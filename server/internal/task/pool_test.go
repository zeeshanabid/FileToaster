package task

import (
  "bytes"
  "context"
  "encoding/json"
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
  defer pool.Close(context.Background())

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
  pool.Close(context.Background())
}

func TestPoolProgressReported(t *testing.T) {
  store := NewStore()
  hub := NewHub()
  pool := NewPool(store, hub, 1, testLogger())
  defer pool.Close(context.Background())

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
  defer pool.Close(context.Background())

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
  defer pool.Close(context.Background())

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
  pool.Close(context.Background())

  if got := atomic.LoadInt32(&ran); got != 1 {
    t.Fatalf("runners executed = %d, want 1", got)
  }
  if store.Get(t2.ID).Status != StatusCancelled {
    t.Fatalf("queued status = %q, want cancelled", store.Get(t2.ID).Status)
  }
}

func TestPoolCancelUnknown(t *testing.T) {
  pool := NewPool(NewStore(), NewHub(), 1, testLogger())
  defer pool.Close(context.Background())
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

  go func() { pool.Close(context.Background()) }()
  // The queued task is marked cancelled during shutdown.
  waitFor(t, func() bool { return store.Get(t2.ID).Status == StatusCancelled })
  close(release)
  // Running task finishes.
  waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusFinished })
}

// TestPoolShutdownRejectsAndFinishesExisting verifies the shutdown contract at
// the pool level: a submission made while a shutdown is in flight is rejected
// (and never started), while the already-running task is allowed to finish.
func TestPoolShutdownRejectsAndFinishesExisting(t *testing.T) {
  store := NewStore()
  hub := NewHub()
  pool := NewPool(store, hub, 1, testLogger())

  release := make(chan struct{})
  var ranTasks, existingFinished int32
  runner := func(ctx context.Context, prog *Progression) error {
    atomic.AddInt32(&ranTasks, 1)
    select {
    case <-ctx.Done():
      return ctx.Err()
    case <-release:
      atomic.AddInt32(&existingFinished, 1)
      return nil
    }
  }
  t1, _ := pool.Enqueue("t", nil, runner)
  waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusRunning })

  shutdownDone := make(chan struct{})
  go func() {
    pool.Close(context.Background())
    close(shutdownDone)
  }()

  // A new operation submitted while shutdown is in flight must never run: it is
  // either rejected outright or accepted-then-cancelled before it can start,
  // regardless of the race with Close's shutdown flag.
  t2, _ := pool.Enqueue("t", nil, runner)
  waitFor(t, func() bool { return store.Get(t2.ID).Status.IsTerminal() })

  close(release)
  waitFor(t, func() bool { return atomic.LoadInt32(&existingFinished) == 1 })
  waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusFinished })
  <-shutdownDone

  if got := atomic.LoadInt32(&ranTasks); got != 1 {
    t.Fatalf("runners started = %d, want 1 (only the pre-existing task)", got)
  }
}

// TestPoolRejectsAfterFullShutdown verifies that a submission after the pool has
// fully drained is rejected and marked failed with ErrShuttingDown.
func TestPoolRejectsAfterFullShutdown(t *testing.T) {
  store := NewStore()
  hub := NewHub()
  pool := NewPool(store, hub, 1, testLogger())
  pool.Close(context.Background())

  var ran int32
  runner := func(ctx context.Context, prog *Progression) error {
    atomic.AddInt32(&ran, 1)
    return nil
  }
  t2, enqueued := pool.Enqueue("t", nil, runner)
  if enqueued {
    t.Fatal("task accepted after shutdown, want rejected")
  }
  if got := atomic.LoadInt32(&ran); got != 0 {
    t.Fatalf("runner started = %d, want 0", got)
  }
  waitFor(t, func() bool { return store.Get(t2.ID).Status == StatusFailed })
  if got := store.Get(t2.ID).Error; got != ErrShuttingDown.Error() {
    t.Fatalf("rejected task error = %q, want %q", got, ErrShuttingDown.Error())
  }
}

// TestPoolGracefulDrainCompletesBeforeTimeout verifies Close returns as soon as
// the running tasks finish, without waiting out the (much larger) deadline.
func TestPoolGracefulDrainCompletesBeforeTimeout(t *testing.T) {
  store := NewStore()
  hub := NewHub()
  pool := NewPool(store, hub, 1, testLogger())

  t1, _ := pool.Enqueue("t", nil, func(ctx context.Context, prog *Progression) error { return nil })
  // Let the dispatcher start the task so Close cannot mistake it for a
  // queued-but-not-started task (which it would cancel).
  waitFor(t, func() bool {
    s := store.Get(t1.ID).Status
    return s == StatusRunning || s == StatusFinished
  })

  shutdownDone := make(chan struct{})
  go func() {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    pool.Close(ctx)
    close(shutdownDone)
  }()

  select {
  case <-shutdownDone:
  case <-time.After(5 * time.Second):
    t.Fatal("Close blocked past task completion; should have drained promptly")
  }
  waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusFinished })
}

// TestPoolTimeoutForceCancelsRunning verifies that once the shutdown deadline
// passes, running tasks are force-cancelled and Close returns promptly.
func TestPoolTimeoutForceCancelsRunning(t *testing.T) {
  store := NewStore()
  hub := NewHub()
  pool := NewPool(store, hub, 1, testLogger())

  var observedCancel int32
  runner := func(ctx context.Context, prog *Progression) error {
    <-ctx.Done()
    atomic.AddInt32(&observedCancel, 1)
    return ctx.Err()
  }
  t1, _ := pool.Enqueue("t", nil, runner)
  waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusRunning })

  ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
  defer cancel()
  done := make(chan struct{})
  go func() {
    pool.Close(ctx)
    close(done)
  }()

  select {
  case <-done:
  case <-time.After(3 * time.Second):
    t.Fatal("Close did not honour the shutdown timeout")
  }
  waitFor(t, func() bool { return store.Get(t1.ID).Status == StatusCancelled })
  if got := atomic.LoadInt32(&observedCancel); got != 1 {
    t.Fatalf("runner observed context cancellation = %d, want 1", got)
  }
}

// TestPoolEmptyPoolExitsGracefully verifies Close returns immediately for an
// empty pool, even with a deadline shorter than any task could possibly run.
func TestPoolEmptyPoolExitsGracefully(t *testing.T) {
  store := NewStore()
  hub := NewHub()
  pool := NewPool(store, hub, 1, testLogger())

  ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
  defer cancel()
  done := make(chan struct{})
  go func() {
    pool.Close(ctx)
    close(done)
  }()

  select {
  case <-done:
  case <-time.After(3 * time.Second):
    t.Fatal("Close hung on an empty pool")
  }
  if got := pool.RunningCount(); got != 0 {
    t.Fatalf("running = %d, want 0", got)
  }
  if got := pool.QueuedCount(); got != 0 {
    t.Fatalf("queued = %d, want 0", got)
  }
}

// TestPoolEnqueueSetsMeta verifies that the operation metadata passed to
// Enqueue is stored on the task record and published via the Hub so clients
// receive it over SSE.
func TestPoolEnqueueSetsMeta(t *testing.T) {
  store := NewStore()
  hub := NewHub()
  pool := NewPool(store, hub, 1, testLogger())
  defer pool.Close(context.Background())

  meta := json.RawMessage(`{"dest":"/data/archive.zip","format":"zip"}`)
  t1, _ := pool.Enqueue("compress", meta, func(ctx context.Context, prog *Progression) error {
    return nil
  })

  // The task returned by Enqueue should have the meta set.
  if !bytes.Equal(store.Get(t1.ID).Meta, meta) {
    t.Fatalf("task meta = %s, want %s", store.Get(t1.ID).Meta, meta)
  }

  // The Hub should publish the task with meta for SSE subscribers.
  sub := hub.Subscribe(nil)
  waitFor(t, func() bool {
    st := store.Get(t1.ID)
    return st != nil && st.Status == StatusFinished
  })

  var published *Task
  select {
  case published = <-sub.Out():
  case <-time.After(2 * time.Second):
    t.Fatal("no publish received")
  }
  hub.Unsubscribe(sub)

  if published == nil {
    t.Fatal("published task is nil")
  }
  if !bytes.Equal(published.Meta, meta) {
    t.Fatalf("published meta = %s, want %s", published.Meta, meta)
  }
}
