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
//
// mu serializes sends with the channel teardown in closeStreams so a producer can
// never send on a channel that has just been closed, and done is closed before
// the channels so a producer blocked on a full event buffer is released when the
// subscriber goes away (the connection closed or the server is shutting down).
type Subscription struct {
	id     int
	out    chan *Task
	events chan *Event
	ids    map[string]struct{}
	closed atomic.Bool // set once the channels have been closed
	done   chan struct{}
	mu     sync.Mutex
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
		done:   make(chan struct{}),
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
		s.closeStreams()
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

// closeStreams tears the subscription down exactly once. It closes done first
// (releasing any producer blocked on a full event buffer), then takes mu before
// closing the data channels so a concurrent Publish/PublishEvent either completes
// its send before the close or observes the closed flag and skips it. Closing the
// channels only after unclose() has published the flag is what makes a send on a
// just-closed channel impossible.
func (s *Subscription) closeStreams() {
	if !s.unclose() {
		return
	}
	close(s.done)
	s.mu.Lock()
	close(s.out)
	close(s.events)
	s.mu.Unlock()
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
// it. An empty Subscription filter means "all tasks". Task snapshots are
// best-effort: a full outbound buffer drops the update rather than blocking the
// pool (the next progress tick or the terminal snapshot supersedes it).
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
		s.publishTask(t)
	}
}

// publishTask makes one non-blocking attempt to deliver a task snapshot. The
// closed check and the send are serialized with closeStreams via mu so a send
// can never race a channel close.
func (s *Subscription) publishTask(t *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return
	}
	select {
	case s.out <- t:
	default:
		// Slow consumer: drop this snapshot rather than block the pool. It is
		// always superseded by a later progress tick or the terminal snapshot.
	}
}

// PublishEvent fans a non-task event out to every Subscription whose filter
// includes the event's task ID (an empty filter means "all tasks"). Unlike
// Publish, event delivery is reliable: if a subscriber's event buffer is full
// the producer waits for space (or for the subscriber to go away) instead of
// dropping the event. Dropping here silently loses streamed search matches,
// which made consecutive identical searches return different result counts.
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
		s.publishEvent(e)
	}
}

// publishEvent delivers one event, blocking while the buffer is full so no
// event is lost. It is released when the subscriber closes (done) or the channel
// has room; the closed check and the send are serialized with closeStreams so the
// send can never race a channel close.
func (s *Subscription) publishEvent(e *Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return
	}
	select {
	case s.events <- e:
	case <-s.done:
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
