package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Runner executes a single task's work. It receives a context that is cancelled
// when the task is cancelled (or when the pool shuts down) and a Progression to
// report byte/file progress through. A non-nil error marks the task failed; a
// context cancellation marks it cancelled.
type Runner func(ctx context.Context, prog *Progression) error

// ErrShuttingDown is returned when work is submitted after shutdown begins.
var ErrShuttingDown = errors.New("server is shutting down; new tasks are rejected")

// ErrNotFound is returned when cancelling a task that is not known to the pool.
var ErrNotFound = errors.New("task not found")

type job struct {
	id   string
	typ  string
	meta json.RawMessage
	run  Runner
}

// Pool runs runners behind a bounded worker pool with an in-memory overflow
// queue. When fewer than `capacity` workers are free a job starts immediately;
// otherwise it waits in FIFO order. It also supports cancelling queued or
// running work and draining gracefully on shutdown.
type Pool struct {
	store    *Store
	hub      *Hub
	capacity int
	logger   *slog.Logger

	mu         sync.Mutex
	running    int
	queued     []job
	cancels    map[string]context.CancelFunc
	nextSeq    int
	shutdown   atomic.Bool
	instanceID string

	notify chan struct{} // signals the dispatcher loop to re-check
	wg     sync.WaitGroup
}

// NewPool builds a pool that runs at most capacity runners at once, updating the
// store and publishing to the hub as work progresses. The dispatcher loop runs
// in its own goroutine; close it with Close.
// instanceID is a unique identifier for this server instance (e.g., a startup
// timestamp) used to make task IDs globally unique across restarts.
func NewPool(store *Store, hub *Hub, capacity int, logger *slog.Logger, instanceID string) *Pool {
	if capacity < 1 {
		capacity = 1
	}
	p := &Pool{
		store:      store,
		hub:        hub,
		capacity:   capacity,
		logger:     logger,
		cancels:    make(map[string]context.CancelFunc),
		notify:     make(chan struct{}, 1),
		instanceID: instanceID,
	}
	go p.loop()
	return p
}

// Capacity reports the maximum number of concurrently running tasks.
func (p *Pool) Capacity() int { return p.capacity }

// QueuedCount reports how many tasks are waiting for a free worker.
func (p *Pool) QueuedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queued)
}

// RunningCount reports how many tasks are currently executing.
func (p *Pool) RunningCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Pool) nextID() string {
	p.nextSeq++
	return fmt.Sprintf("%s-task-%d", p.instanceID, p.nextSeq)
}

// Enqueue creates a queued task record, publishes it, and hands the runner to
// the pool. It returns the task record and whether the task was accepted (false
// when shutdown has started). A submitted task is never left dangling: if
// shutdown rejected it, the record is marked failed.
func (p *Pool) Enqueue(typ string, meta json.RawMessage, run Runner) (*Task, bool) {
	id := p.nextID()
	now := time.Now().UTC()
	t := &Task{
		ID:      id,
		Type:    typ,
		Status:  StatusQueued,
		Created: now,
		Updated: now,
		Meta:    meta,
	}
	p.store.Add(t)
	p.hub.Publish(t)

	p.mu.Lock()
	if p.shutdown.Load() {
		p.mu.Unlock()
		t.Status = StatusFailed
		t.Error = ErrShuttingDown.Error()
		p.store.Add(t)
		p.hub.Publish(t)
		return t, false
	}
	p.queued = append(p.queued, job{id: id, typ: typ, meta: meta, run: run})
	p.mu.Unlock()
	p.wake()
	return t, true
}

// wake nudges the dispatcher loop so it re-evaluates whether it can start more
// work. It is safe to call even when the loop is idle or shutting down.
func (p *Pool) wake() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// loop is the dispatcher: it starts as many queued jobs as the capacity allows
// and waits for a wake (a new job, a freed slot, or shutdown) before re-checking.
func (p *Pool) loop() {
	for {
		p.mu.Lock()
		started := false
		for p.running < p.capacity && len(p.queued) > 0 && !p.shutdown.Load() {
			j := p.queued[0]
			p.queued = p.queued[1:]
			p.running++
			started = true
			p.mu.Unlock()
			p.start(j)
			p.mu.Lock()
		}
		drain := p.shutdown.Load() && len(p.queued) == 0 && p.running == 0
		p.mu.Unlock()

		if drain {
			return
		}
		if !started {
			// Nothing to start now; wait for a wake instead of busy-looping.
			<-p.notify
		}
	}
}

// start launches one job in its own goroutine. p.mu must be held when calling so
// the WaitGroup add happens before any concurrent Wait.
func (p *Pool) start(j job) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.execute(j)
		p.mu.Lock()
		p.running--
		p.mu.Unlock()
		p.wake()
	}()
}

// execute runs a single job to completion, updating the store and hub with the
// running, final, and (per file) intermediate progress.
func (p *Pool) execute(j job) {
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancels[j.id] = cancel
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.cancels, j.id)
		p.mu.Unlock()
		cancel()
	}()

	prog := &Progression{}
	prog.SetSink(func(bp ByteProgress) {
		p.store.SetBytes(j.id, bp)
		if t := p.store.Get(j.id); t != nil {
			p.hub.Publish(t)
		}
	})
	// Non-task events (e.g. a streamed search match) are forwarded to the SSE
	// hub scoped to this task's id so subscribers only see their own events.
	prog.SetEventSink(func(name string, data []byte) {
		p.hub.PublishEvent(&Event{TaskID: j.id, Name: name, Data: data})
	})

	t := p.store.Get(j.id)
	p.store.SetStatus(j.id, StatusRunning, "")
	if t != nil {
		p.hub.Publish(t)
	}

	err := j.run(ctx, prog)
	bp := prog.Snapshot()
	p.store.SetBytes(j.id, bp)

	switch {
	case err == nil:
		p.store.SetStatus(j.id, StatusFinished, "")
	case context.Cause(ctx) != nil || ctx.Err() != nil:
		// Cancelled via the per-task context (either the user cancelled it or
		// shutdown tore it down).
		p.store.SetStatus(j.id, StatusCancelled, "cancelled")
	default:
		p.store.SetStatus(j.id, StatusFailed, err.Error())
		p.logger.Error("task failed", "id", j.id, "type", j.typ, "error", err.Error())
	}
	p.hub.Publish(p.store.Get(j.id))
}

// Cancel asks a running task to stop (its runner's context is cancelled) or, if
// it is still queued, removes it from the queue. It returns whether the task was
// known. A task already terminal is not re-cancelled.
func (p *Pool) Cancel(id string) error {
	p.mu.Lock()
	if cancel, ok := p.cancels[id]; ok {
		p.mu.Unlock()
		cancel()
		return nil
	}
	for i, j := range p.queued {
		if j.id == id {
			p.queued = append(p.queued[:i], p.queued[i+1:]...)
			p.mu.Unlock()
			p.store.SetStatus(id, StatusCancelled, "cancelled")
			p.hub.Publish(p.store.Get(id))
			return nil
		}
	}
	p.mu.Unlock()
	return ErrNotFound
}

// Close begins graceful shutdown: it stops accepting new work, drains the
// running tasks, then releases the pool. Queued-but-not-started tasks are marked
// cancelled. Close blocks until all running tasks have finished; ctx bounds how
// long it waits and, once the deadline passes, Cancel is called on every running
// task so they stop, then Close waits briefly for them to observe it before
// returning. A ctx that is already Done (or an empty pool) returns immediately.
func (p *Pool) Close(ctx context.Context) {
	p.mu.Lock()
	if p.shutdown.Load() {
		// Already shutting down; still wait for any in-flight work to finish.
		p.mu.Unlock()
		p.wg.Wait()
		return
	}
	queued := p.queued
	p.queued = nil
	p.shutdown.Store(true)
	p.mu.Unlock()

	for _, j := range queued {
		p.store.SetStatus(j.id, StatusCancelled, "cancelled")
		p.hub.Publish(p.store.Get(j.id))
	}
	// Wake the dispatcher so it observes shutdown and exits once running tasks drain.
	p.wake()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		p.wg.Wait()
	}()

	select {
	case <-drained:
		// Every running task finished within the grace period.
	case <-ctx.Done():
		// Grace period elapsed: force-cancel running tasks and wait for them to
		// observe cancellation before giving up.
		p.mu.Lock()
		for _, cancel := range p.cancels {
			cancel()
		}
		p.mu.Unlock()
		p.logger.Warn("shutdown timeout reached; cancelling running tasks", "seconds", ctxDeadlineSeconds(ctx))
		select {
		case <-drained:
		case <-ctx.Done():
			p.logger.Warn("a task did not stop in time during shutdown")
		}
	}
	p.logger.Info("task pool drained")
}

// ctxDeadlineSeconds reports the whole-second portion of a context's remaining
// time, used only for logging. It returns 0 when the context has no deadline or
// has already expired.
func ctxDeadlineSeconds(ctx context.Context) int64 {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	return int64(remaining/time.Second) + 1
}
