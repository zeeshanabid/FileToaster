package task

import "testing"

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
