// Package service wires the task engine to the operations that run inside it.
// It owns the task store, the bounded worker pool, and the SSE hub, and exposes
// a small registry so new operations can be added without touching routing or
// the pool: implement an Op and Register it.
package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"filetoaster/internal/task"
)

// CopyOperation is the operation key registered for copy.
const CopyOperation = "copy"

// MoveOperation is the operation key registered for move.
const MoveOperation = "move"

// DeleteOperation is the operation key registered for delete.
const DeleteOperation = "delete"

// CompressOperation is the operation key registered for compress.
const CompressOperation = "compress"

// DecompressOperation is the operation key registered for decompress.
const DecompressOperation = "decompress"

// TruncateOperation is the operation key registered for truncate.
const TruncateOperation = "truncate"

// SearchOperation is the operation key registered for the recursive search.
const SearchOperation = "search"

// UploadOperation is the operation key registered for the upload finalize step
// (a tracked copy from the staging dir into the target).
const UploadOperation = "upload"

// Op is a pluggable file operation. Prepare validates and resolves the request
// body into an opaque spec and returns its serializable meta (shown in the task
// record); Run executes the prepared spec.
type Op struct {
	// Prepare decodes + validates the request, returning spec and meta.
	Prepare func(body []byte) (spec any, meta []byte, err error)
	// Run executes the prepared spec, reporting progress through prog.
	Run func(ctx context.Context, spec any, prog *task.Progression) error
}

// Service is the task orchestrator: it holds the engine and the registered ops,
// and is safe for concurrent use.
type Service struct {
	store  *task.Store
	pool   *task.Pool
	hub    *task.Hub
	logger *slog.Logger

	mu  sync.RWMutex
	ops map[string]Op
}

// NewService builds a service with a pool that runs at most maxParallel tasks at
// once. The pool and its dispatcher start immediately; shut it down with Close.
// instanceID is a unique identifier for this server instance (e.g., a startup
// timestamp) used to make task IDs globally unique across restarts.
func NewService(maxParallel int, logger *slog.Logger, instanceID string) *Service {
	store := task.NewStore()
	hub := task.NewHub()
	pool := task.NewPool(store, hub, maxParallel, logger, instanceID)
	return &Service{
		store:  store,
		pool:   pool,
		hub:    hub,
		logger: logger,
		ops:    make(map[string]Op),
	}
}

// Register adds or replaces an operation keyed by its name.
func (s *Service) Register(name string, op Op) {
	s.mu.Lock()
	s.ops[name] = op
	s.mu.Unlock()
}

// Store exposes the task store (task snapshots).
func (s *Service) Store() *task.Store { return s.store }

// Hub exposes the SSE hub (client subscriptions).
func (s *Service) Hub() *task.Hub { return s.hub }

// Prepare validates + resolves a request without enqueuing, returning the
// operation spec and its serializable meta.
func (s *Service) Prepare(name string, body []byte) (spec any, meta []byte, err error) {
	s.mu.RLock()
	op, ok := s.ops[name]
	s.mu.RUnlock()
	if !ok {
		return nil, nil, ErrUnknownOperation
	}
	return op.Prepare(body)
}

// Submit prepares and enqueues an operation, returning the created task. If the
// pool is shutting down the task is marked failed and an error is returned.
func (s *Service) Submit(name string, body []byte) (*task.Task, error) {
	spec, meta, err := s.Prepare(name, body)
	if err != nil {
		return nil, err
	}
	op := s.lookup(name)
	run := func(ctx context.Context, prog *task.Progression) error { return op.Run(ctx, spec, prog) }
	t, enqueued := s.pool.Enqueue(name, meta, run)
	if !enqueued {
		return t, task.ErrShuttingDown
	}
	return t, nil
}

func (s *Service) lookup(name string) Op {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ops[name]
}

// Cancel asks a queued or running task to stop.
func (s *Service) Cancel(id string) error { return s.pool.Cancel(id) }

// Snapshot returns all tasks in creation order.
func (s *Service) Snapshot() []*task.Task { return s.store.List() }

// ClearCompleted drops every terminal (finished, failed or cancelled) task from
// the store, returning the removed ids. Running and queued tasks are untouched.
// The in-memory store survives browser refreshes, so removing the records here
// is what keeps a cleared task from reappearing on reload.
func (s *Service) ClearCompleted() []string { return s.store.ClearTerminal() }

// Get returns a single task by id, or nil.
func (s *Service) Get(id string) *task.Task { return s.store.Get(id) }

// Subscribe registers an SSE client, optionally filtered to the given task IDs.
func (s *Service) Subscribe(ids []string) *task.Subscription { return s.hub.Subscribe(ids) }

// Shutdown gracefully stops the pool, draining running tasks, then closes the
// SSE hub so client streams end and the HTTP server can be released promptly.
func (s *Service) Shutdown(ctx context.Context) {
	s.pool.Close(ctx)
	s.hub.Shutdown()
}

// ErrUnknownOperation is returned when submitting an unregistered operation.
var ErrUnknownOperation = errors.New("unknown operation")
