package download

import (
  "strings"
  "sync"
  "testing"
  "time"
)

// TestIssueReturnsFixedLengthUniqueTokens verifies every token is the fixed
// hex-encoded length of TokenLength bytes and that repeated issuance does not
// collide.
func TestIssueReturnsFixedLengthUniqueTokens(t *testing.T) {
  reg := NewRegistry()

  seen := make(map[string]struct{})
  for i := 0; i < 1000; i++ {
    tok := reg.Issue("/files/a.txt")
    if want := TokenLength * 2; len(tok) != want {
      t.Fatalf("token length = %d, want %d (fixed)", len(tok), want)
    }
    // A token is unguessable hex: only hex characters.
    for _, c := range tok {
      if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
        t.Fatalf("token %q is not hex", tok)
      }
    }
    if _, dup := seen[tok]; dup {
      t.Fatalf("duplicate token %q issued", tok)
    }
    seen[tok] = struct{}{}
  }
}

// TestTakeReturnsPathAndConsumes verifies a token yields its bound path and is
// then gone — it cannot be reused.
func TestTakeReturnsPathAndConsumes(t *testing.T) {
  reg := NewRegistry()
  tok := reg.Issue("/files/report.pdf")

  path, ok := reg.Take(tok)
  if !ok || path != "/files/report.pdf" {
    t.Fatalf("Take = %q, %v; want /files/report.pdf, true", path, ok)
  }
  // The token is consumed: a second Take misses.
  if _, ok := reg.Take(tok); ok {
    t.Fatalf("token was reused after consumption")
  }
  if reg.Size() != 0 {
    t.Fatalf("Size = %d, want 0 (token consumed)", reg.Size())
  }
}

// TestTakeUnknownToken verifies Take reports ok=false for an unknown token and
// for a token bound to a different file.
func TestTakeUnknownToken(t *testing.T) {
  reg := NewRegistry()
  if _, ok := reg.Take("does-not-exist"); ok {
    t.Fatalf("Take on unknown token returned ok")
  }
  reg.Issue("/files/one.txt")
  if p, ok := reg.Take("other"); ok || p != "" {
    t.Fatalf("Take(other) = %q, %v; want \"\", false", p, ok)
  }
}

// TestDeleteRemovesToken verifies Delete removes a token without affecting others.
func TestDeleteRemovesToken(t *testing.T) {
  reg := NewRegistry()
  a := reg.Issue("/files/a.txt")
  b := reg.Issue("/files/b.txt")

  reg.Delete(a)
  if _, ok := reg.Take(a); ok {
    t.Fatalf("deleted token was still consumable")
  }
  // A sibling token is untouched.
  if _, ok := reg.Take(b); !ok {
    t.Fatalf("sibling token vanished after deleting another")
  }
  // Delete is idempotent: a missing token is a no-op.
  reg.Delete("still-gone")
}

// TestSweepRules verifies the sweep age gate: a non-positive maxAge is a no-op,
// and only tokens older than maxAge are reclaimed (fresh ones survive).
func TestSweepRules(t *testing.T) {
  reg := NewRegistry()
  tok := reg.Issue("/files/one.txt")

  if got := reg.Sweep(0); got != 0 {
    t.Fatalf("zero maxAge swept %d, want 0", got)
  }
  if got := reg.Sweep(-time.Second); got != 0 {
    t.Fatalf("negative maxAge swept %d, want 0", got)
  }
  // A token issued "now" is well within an hour, so nothing is swept.
  if got := reg.Sweep(time.Hour); got != 0 {
    t.Fatalf("sweep with an hour horizon dropped %d, want 0", got)
  }
  if _, ok := reg.Take(tok); !ok {
    t.Fatalf("fresh token vanished without being swept")
  }
}

// TestSweepReclaimsAgedTokens ages a token by rewriting its stored issue time
// (the test lives in package download, so it can read the internal map) and
// proves the sweep removes it while a newer token survives.
func TestSweepReclaimsAgedTokens(t *testing.T) {
  reg := NewRegistry()
  aged := reg.Issue("/files/aged.txt")
  fresh := reg.Issue("/files/fresh.txt")

  reg.mu.Lock()
  e := reg.tokens[aged]
  e.created = time.Now().Add(-2 * time.Hour)
  reg.tokens[aged] = e
  reg.mu.Unlock()

  if got := reg.Sweep(time.Hour); got != 1 {
    t.Fatalf("sweep reclaimed %d tokens, want 1", got)
  }
  if _, ok := reg.Take(aged); ok {
    t.Fatalf("aged token survived the sweep")
  }
  if _, ok := reg.Take(fresh); !ok {
    t.Fatalf("fresh token was swept")
  }
}

// TestConcurrentAccess guards the registry under concurrent Issue/Take/Delete
// with the race detector to catch data races in production use.
func TestConcurrentAccess(t *testing.T) {
  reg := NewRegistry()
  var wg sync.WaitGroup
  const workers = 32
  for i := 0; i < workers; i++ {
    wg.Add(1)
    go func(i int) {
      defer wg.Done()
      name := strings.ToUpper(string(rune('A'+i%26))) + ".txt"
      tok := reg.Issue("/files/" + name)
      _, _ = reg.Take(tok)
      reg.Delete(tok)
      reg.Sweep(time.Minute)
      _ = reg.Size()
    }(i)
  }
  wg.Wait()
}
