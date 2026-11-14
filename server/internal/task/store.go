package task

import (
  "sort"
  "sync"
  "time"
)

// Store is the in-memory, thread-safe registry of tasks. It is the single source
// of truth for task state: the pool updates it as work progresses and the SSE
// hub publishes its snapshots to clients. It survives browser refreshes and is
// cleared only by a server restart (the store is rebuilt on each startup).
type Store struct {
  mu    sync.RWMutex
  tasks map[string]*Task
}

// NewStore returns an empty Store.
func NewStore() *Store {
  return &Store{tasks: make(map[string]*Task)}
}

// Add inserts or replaces a task.
func (s *Store) Add(t *Task) {
  s.mu.Lock()
  t.Updated = time.Now().UTC()
  s.tasks[t.ID] = t
  s.mu.Unlock()
}

// Get returns a copy of the task with the given id, or nil when absent.
func (s *Store) Get(id string) *Task {
  s.mu.RLock()
  t, ok := s.tasks[id]
  s.mu.RUnlock()
  if !ok {
    return nil
  }
  return copyTask(t)
}

// SetStatus updates the status (and optional error message) of a task, keeping
// progress at 100% once it is finished.
func (s *Store) SetStatus(id string, status Status, errMsg string) {
  s.mu.Lock()
  t, ok := s.tasks[id]
  if !ok {
    s.mu.Unlock()
    return
  }
  t.Status = status
  t.Error = errMsg
  if status == StatusFinished {
    t.Progress = 100
  } else if status == StatusRunning || status == StatusQueued {
    // Preserve the running byte progress already recorded.
    t.Progress = t.Bytes.Percent()
  }
  t.Updated = time.Now().UTC()
  s.mu.Unlock()
}

// SetBytes updates the byte/file progress and re-derives the percent.
func (s *Store) SetBytes(id string, bp ByteProgress) {
  s.mu.Lock()
  t, ok := s.tasks[id]
  if !ok {
    s.mu.Unlock()
    return
  }
  t.Bytes = bp
  if !t.Status.IsTerminal() {
    t.Progress = bp.Percent()
  }
  t.Updated = time.Now().UTC()
  s.mu.Unlock()
}

// SetMeta replaces a task's operation metadata.
func (s *Store) SetMeta(id string, meta []byte) {
  s.mu.Lock()
  t, ok := s.tasks[id]
  if !ok {
    s.mu.Unlock()
    return
  }
  t.Meta = append([]byte(nil), meta...)
  t.Updated = time.Now().UTC()
  s.mu.Unlock()
}

// List returns every task sorted by creation time, oldest first.
func (s *Store) List() []*Task {
  s.mu.RLock()
  out := make([]*Task, 0, len(s.tasks))
  for _, t := range s.tasks {
    out = append(out, copyTask(t))
  }
  s.mu.RUnlock()
  sort.Slice(out, func(i, j int) bool {
    if out[i].Created.Equal(out[j].Created) {
      return out[i].ID < out[j].ID
    }
    return out[i].Created.Before(out[j].Created)
  })
  return out
}

// copyTask returns a shallow copy of a task for safe return to callers that must
// not mutate the stored record.
func copyTask(t *Task) *Task {
  cp := *t
  if len(t.Meta) > 0 {
    cp.Meta = append([]byte(nil), t.Meta...)
  }
  return &cp
}
