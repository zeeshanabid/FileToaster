package task

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Event is a non-task SSE message streamed alongside the task deltas (for
// example a single search match). TaskID scopes the event to the task that
// produced it, so a subscriber filtered to a set of task IDs only receives the
// events of the tasks it cares about; Name is the SSE event name; Data is the
// JSON-encoded payload.
type Event struct {
	TaskID string
	Name   string
	Data   []byte
}

// Subscription is one client's SSE stream. ids, when non-empty, filters the
// stream to those task IDs; an empty set delivers every task. out carries task
// snapshots to be written to the connection; events carries non-task events
// (e.g. streamed search results).
type Subscription struct {
	id     int
	out    chan *Task
	events chan *Event
	ids    map[string]struct{}
	closed atomic.Bool // set once the channels have been closed
}

// Hub is a small SSE pub/sub for task deltas. Clients subscribe (optionally
// filtering to a set of task IDs); the pool publishes every state change. Slow
// clients are never blocked: a full outbound buffer drops the event.
type Hub struct {
	mu      sync.Mutex
	nextID  int
	subs    map[int]*Subscription
	closing bool
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]*Subscription)}
}

// Subscribe registers a new client. ids filters the stream; pass nil/empty to
// receive all tasks. The caller must Unsubscribe when the stream ends. If the
// hub is shutting down the new stream is closed immediately so the handler
// exits instead of blocking the server shutdown.
func (h *Hub) Subscribe(ids []string) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Subscription{
		id:     h.nextID,
		out:    make(chan *Task, 64),
		events: make(chan *Event, 256),
	}
	h.nextID++
	if len(ids) > 0 {
		s.ids = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			s.ids[id] = struct{}{}
		}
	}
	if h.closing {
		// Shutdown already started: hand back a closed stream so the handler
		// loop returns without holding the connection open.
		s.closed.Store(true)
		close(s.out)
		close(s.events)
		return s
	}
	h.subs[s.id] = s
	return s
}

// Out returns the read-only channel of task snapshots delivered to this
// subscription. The protocol layer reads from it to write SSE events.
func (s *Subscription) Out() <-chan *Task { return s.out }

// Events returns the read-only channel of non-task events (e.g. streamed search
// results) delivered to this subscription. The protocol layer reads from it
// alongside Out to write non-task SSE events.
func (s *Subscription) Events() <-chan *Event { return s.events }

// unclose marks the subscription closed and reports whether the caller owns the
// (previously un-closed) channels, so close happens exactly once.
func (s *Subscription) unclose() bool {
	return s.closed.CompareAndSwap(false, true)
}

// closeStreams closes both channels exactly once.
func (s *Subscription) closeStreams() {
	if s.unclose() {
		close(s.out)
		close(s.events)
	}
}

// Unsubscribe removes a client and closes its channels. It is safe to call after
// Shutdown has already closed the channels.
func (h *Hub) Unsubscribe(s *Subscription) {
	if s == nil {
		return
	}
	h.mu.Lock()
	if cur, ok := h.subs[s.id]; ok && cur == s {
		delete(h.subs, s.id)
	}
	h.mu.Unlock()
	s.closeStreams()
}

// Shutdown closes every active subscription so SSE handlers stop and their
// connections close, and it blocks new subscriptions. Call it as part of server
// shutdown so http.Server.Shutdown can release the (otherwise never-idle) SSE
// connections quickly.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closing = true
	for id, s := range h.subs {
		s.closeStreams()
		delete(h.subs, id)
	}
}

// Publish fans a task snapshot out to every Subscription that is interested in
// it. An empty Subscription filter means "all tasks".
func (h *Hub) Publish(t *Task) {
	h.mu.Lock()
	targets := make([]*Subscription, 0, len(h.subs))
	for _, s := range h.subs {
		targets = append(targets, s)
	}
	h.mu.Unlock()

	for _, s := range targets {
		if s.ids != nil {
			if _, ok := s.ids[t.ID]; !ok {
				continue
			}
		}
		select {
		case s.out <- t:
		default:
			// Slow consumer: drop this update rather than block the pool.
		}
	}
}

// PublishEvent fans a non-task event out to every Subscription whose filter
// includes the event's task ID (an empty filter means "all tasks"). Like
// Publish, a slow consumer's event is dropped rather than blocking the producer.
func (h *Hub) PublishEvent(e *Event) {
	if e == nil {
		return
	}
	h.mu.Lock()
	targets := make([]*Subscription, 0, len(h.subs))
	for _, s := range h.subs {
		targets = append(targets, s)
	}
	h.mu.Unlock()

	for _, s := range targets {
		if s.ids != nil {
			if _, ok := s.ids[e.TaskID]; !ok {
				continue
			}
		}
		select {
		case s.events <- e:
		default:
			// Slow consumer: drop this event rather than block the pool.
		}
	}
}

// Active reports how many clients are currently subscribed (test helper).
func (h *Hub) Active() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// IDs returns the subscribed client IDs sorted ascending (test helper).
func (h *Hub) IDs() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]int, 0, len(h.subs))
	for id := range h.subs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
