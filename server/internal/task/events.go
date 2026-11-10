package task

import (
	"sort"
	"sync"
)

// Subscription is one client's SSE stream. ids, when non-empty, filters the
// stream to those task IDs; an empty set delivers every task. out carries task
// snapshots to be written to the connection.
type Subscription struct {
	id  int
	out chan *Task
	ids map[string]struct{}
}

// Hub is a small SSE pub/sub for task deltas. Clients subscribe (optionally
// filtering to a set of task IDs); the pool publishes every state change. Slow
// clients are never blocked: a full outbound buffer drops the event.
type Hub struct {
	mu     sync.Mutex
	nextID int
	subs   map[int]*Subscription
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]*Subscription)}
}

// Subscribe registers a new client. ids filters the stream; pass nil/empty to
// receive all tasks. The caller must Unsubscribe when the stream ends.
func (h *Hub) Subscribe(ids []string) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	s := &Subscription{
		id:  h.nextID,
		out: make(chan *Task, 64),
	}
	if len(ids) > 0 {
		s.ids = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			s.ids[id] = struct{}{}
		}
	}
	h.subs[s.id] = s
	return s
}

// Out returns the read-only channel of task snapshots delivered to this
// subscription. The protocol layer reads from it to write SSE events.
func (s *Subscription) Out() <-chan *Task { return s.out }

// Unsubscribe removes a client and closes its channel.
func (h *Hub) Unsubscribe(s *Subscription) {
	if s == nil {
		return
	}
	h.mu.Lock()
	if cur, ok := h.subs[s.id]; ok && cur == s {
		delete(h.subs, s.id)
	}
	h.mu.Unlock()
	close(s.out)
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
