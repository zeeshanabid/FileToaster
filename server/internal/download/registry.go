// Package download provides the in-memory token registry behind the single-file
// download endpoints. A client POSTs a source path (confined to the allowed
// roots) to obtain a short-lived, fixed-length token, then GETs
// /downloads/{token} to stream the file. The token is the only thing the
// download URL carries, so no client-supplied path can ever reach the streaming
// endpoint: traversal to forbidden or out-of-roots files is impossible and the
// token is consumed (removed) the first time the file streams.
package download

import (
  "crypto/rand"
  "encoding/hex"
  "sync"
  "time"
)

// TokenLength is the random byte length of every generated token. 24 bytes render
// as a 48-character fixed-length hex string — unguessable and of constant shape,
// so a token gives away nothing about the file it represents.
const TokenLength = 24

// entry is a pending download: the resolved (contained) source file and the
// instant its token was issued, which the age sweep uses to reclaim abandoned
// tokens that were never fetched.
type entry struct {
  path    string
  created time.Time
}

// Registry is an in-memory, concurrency-safe map of short-lived download tokens
// to the resolved source files they grant access to. Tokens are one-time use:
// Take removes the entry it returns, so a token cannot be reused after the file
// has streamed.
type Registry struct {
  mu     sync.RWMutex
  tokens map[string]entry
}

// NewRegistry returns an empty token registry.
func NewRegistry() *Registry {
  return &Registry{tokens: make(map[string]entry)}
}

// Issue binds path to a fresh fixed-length token and returns that token. It
// records the issue time so the age sweep can reclaim the entry when it lapses.
func (r *Registry) Issue(path string) string {
  token := newToken()
  r.mu.Lock()
  r.tokens[token] = entry{path: path, created: time.Now()}
  r.mu.Unlock()
  return token
}

// Take returns the path bound to token and removes the entry so the token cannot
// be reused. ok is false for an unknown or already-consumed token.
func (r *Registry) Take(token string) (path string, ok bool) {
  r.mu.Lock()
  defer r.mu.Unlock()
  e, found := r.tokens[token]
  if !found {
    return "", false
  }
  delete(r.tokens, token)
  return e.path, true
}

// Delete removes a token when present. It is idempotent and never errors.
func (r *Registry) Delete(token string) {
  r.mu.Lock()
  delete(r.tokens, token)
  r.mu.Unlock()
}

// Sweep removes tokens issued more than maxAge ago, reclaiming downloads that
// were never fetched (for example a browser tab that was closed). It returns the
// number of tokens dropped. A non-positive maxAge is a no-op so a misconfigured
// sweep never wipes live tokens.
func (r *Registry) Sweep(maxAge time.Duration) int {
  if maxAge <= 0 {
    return 0
  }
  deadline := time.Now().Add(-maxAge)
  r.mu.Lock()
  defer r.mu.Unlock()
  dropped := 0
  for tok, e := range r.tokens {
    if e.created.Before(deadline) {
      delete(r.tokens, tok)
      dropped++
    }
  }
  return dropped
}

// Size returns the number of live (unconsumed, not yet swept) tokens.
func (r *Registry) Size() int {
  r.mu.RLock()
  defer r.mu.RUnlock()
  return len(r.tokens)
}

// newToken returns a fresh fixed-length token from the OS CSPRNG. crypto/rand
// essentially always succeeds; on the (practically impossible) failure the loop
// retries so the token stays fixed-length and unguessable rather than panicking.
func newToken() string {
  b := make([]byte, TokenLength)
  for i := 0; i < 16; i++ {
    if _, err := rand.Read(b); err == nil {
      break
    }
  }
  return hex.EncodeToString(b)
}
