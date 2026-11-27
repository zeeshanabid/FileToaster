package task

import (
	"testing"
	"time"
)

// isClosed reports whether a channel has been closed by attempting a
// non-blocking read.
func isClosed(c <-chan *Task) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// TestHubShutdownClosesAllSubscriptions verifies Shutdown closes every active
// subscription channel and empties the hub. Unsubscribe afterwards must stay a
// safe no-op (the channel was already closed by Shutdown, not by Unsubscribe).
func TestHubShutdownClosesAllSubscriptions(t *testing.T) {
	h := NewHub()
	subs := make([]*Subscription, 0, 3)
	for i := 0; i < 3; i++ {
		subs = append(subs, h.Subscribe(nil))
	}
	if h.Active() != 3 {
		t.Fatalf("active = %d, want 3", h.Active())
	}

	h.Shutdown()
	if h.Active() != 0 {
		t.Fatalf("active after Shutdown = %d, want 0", h.Active())
	}
	for i, s := range subs {
		if !isClosed(s.Out()) {
			t.Errorf("sub[%d].Out not closed after Shutdown", i)
		}
	}
	// Idempotent Unsubscribe must not panic on an already-closed channel.
	for _, s := range subs {
		h.Unsubscribe(s)
		h.Unsubscribe(s)
	}
}

// TestHubShutdownStopsNewSubscriptions verifies that subscribing during/after a
// shutdown returns an already-closed stream so a handler loop exits instead of
// blocking the server shutdown.
func TestHubShutdownStopsNewSubscriptions(t *testing.T) {
	h := NewHub()
	h.Subscribe(nil)
	h.Shutdown()

	s := h.Subscribe(nil)
	if !isClosed(s.Out()) {
		t.Fatalf("subscribed stream during shutdown is not closed")
	}
}

// TestHubUnsubscribeAfterCloseIsSafe ensures Unsubscribe never double-closes,
// including for a stream that Shutdown already closed (it sets the closed flag
// itself). This mirrors the handler's defer Unsubscribe running after the hub
// has torn the stream down.
func TestHubUnsubscribeAfterCloseIsSafe(t *testing.T) {
	h := NewHub()
	h.Shutdown() // closing=true

	// A subscription created during shutdown returns an already-closed stream
	// with the closed flag set.
	s := h.Subscribe(nil)
	if !isClosed(s.Out()) {
		t.Fatalf("subscribed stream during shutdown is not closed")
	}
	// The handler's defer Unsubscribe then runs; it must not double-close.
	h.Unsubscribe(s)
	h.Unsubscribe(s)
}

// isClosedEvent reports whether an event channel has been closed by attempting a
// non-blocking read.
func isClosedEvent(c <-chan *Event) bool {
	select {
	case _, ok := <-c:
		return !ok
	default:
		return false
	}
}

// TestHubPublishEventScopesByTaskID verifies PublishEvent delivers an event to
// an unfiltered subscriber and to a subscriber filtered to the event's task id,
// but not to a subscriber filtered to a different task.
func TestHubPublishEventScopesByTaskID(t *testing.T) {
	h := NewHub()
	all := h.Subscribe(nil)
	matching := h.Subscribe([]string{"task-a"})
	other := h.Subscribe([]string{"task-b"})

	h.PublishEvent(&Event{TaskID: "task-a", Name: "search-result", Data: []byte(`{"n":1}`)})

	select {
	case e := <-all.Events():
		if e.TaskID != "task-a" || e.Name != "search-result" {
			t.Fatalf("all got %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("unfiltered subscriber did not receive the event")
	}

	select {
	case e := <-matching.Events():
		if e.TaskID != "task-a" {
			t.Fatalf("matching got %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("matching subscriber did not receive the event")
	}

	select {
	case e := <-other.Events():
		t.Fatalf("non-matching subscriber received %+v", e)
	case <-time.After(20 * time.Millisecond):
		// Expected: the filter excluded the event.
	}
}

// TestHubShutdownClosesEventChannels verifies Shutdown closes the event channel
// alongside the task channel.
func TestHubShutdownClosesEventChannels(t *testing.T) {
	h := NewHub()
	s := h.Subscribe(nil)
	h.Shutdown()
	if !isClosedEvent(s.Events()) {
		t.Fatal("event channel not closed after Shutdown")
	}
}

// TestHubPublishEventDoesNotDrop verifies event delivery is reliable: publishing
// many more events than the outbound buffer holds must not lose any of them.
// A lossy (drop-on-full) hub made consecutive identical searches return
// different result counts.
func TestHubPublishEventDoesNotDrop(t *testing.T) {
	h := NewHub()
	sub := h.Subscribe(nil)

	const n = 2000 // far above the 256-event buffer
	received := make(chan *Event, n)
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		i := 0
		for e := range sub.Events() {
			received <- e
			i++
			// Slow the consumer so the producer is forced to wait for buffer
			// space instead of racing ahead and dropping events.
			if i%25 == 0 {
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()

	for i := 0; i < n; i++ {
		h.PublishEvent(&Event{TaskID: "task-a", Name: "search-result", Data: []byte(`{"n":1}`)})
	}

	deadline := time.After(10 * time.Second)
	for len(received) < n {
		select {
		case <-deadline:
			t.Fatalf("received %d events, want %d (events were dropped)", len(received), n)
		case <-time.After(time.Millisecond):
		}
	}

	h.Unsubscribe(sub)
	<-drainDone
	if got := len(received); got != n {
		t.Fatalf("received %d events, want %d", got, n)
	}
}

// TestHubPublishEventReleasedOnUnsubscribe verifies a producer blocked on a full
// event buffer is released when the subscriber goes away, so a slow or departed
// client can never deadlock the task that is streaming to it.
func TestHubPublishEventReleasedOnUnsubscribe(t *testing.T) {
	h := NewHub()
	sub := h.Subscribe(nil)

	returned := make(chan struct{})
	go func() {
		// The buffer holds 256 events; once full the producer blocks until the
		// subscription is torn down.
		for i := 0; i < 1000; i++ {
			h.PublishEvent(&Event{TaskID: "task-a", Name: "search-result", Data: []byte("x")})
		}
		close(returned)
	}()

	// Give the producer time to fill the buffer and block.
	time.Sleep(50 * time.Millisecond)
	h.Unsubscribe(sub)

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("producer not released after the subscriber unsubscribed")
	}
}
