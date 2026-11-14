// Package upload implements chunked, resumable file uploads. Chunks are staged
// (per-file, at fixed offsets) under tmp_dir inside a per-session dir while a
// client uploads them; a single tracked op then copies staging into the target
// (finalize). Sessions live in memory only, so they survive a browser refresh
// but not a server restart.
package upload

import (
  "crypto/rand"
  "encoding/hex"
  "errors"
  "io"
  "os"
  "path/filepath"
  "sync"
  "time"
)

// dirPrefix is the prefix of every staging dir under tmp_dir, so the sweep can
// find them by scanning the directory.
const dirPrefix = "filetoaster-"

// DefaultChunkSize is the per-file chunk size used when the client does not send one.
const DefaultChunkSize = 4 << 20 // 4 MiB

// mtimeThrottle bounds how often a chunk write refreshes the staging dir's mtime.
// Writing file content does not update the parent dir mtime, but the age sweep
// relies on it, so writes refresh it (throttled) rather than on every write.
const mtimeThrottle = 200 * time.Millisecond

// ErrNoSession signals that no session exists for the referenced ID.
var ErrNoSession = errors.New("upload session not found")

// ErrChunkOutOfRange signals that a chunk index is outside the range for its file.
var ErrChunkOutOfRange = errors.New("chunk index is out of range")

// ErrAborted signals that the session was cancelled and no longer accepts chunks.
var ErrAborted = errors.New("upload session was cancelled")

// ErrIllegalName signals that a manifest entry (file rel or dir) is not a clean
// relative path, which could let an upload write outside the target on finalize.
var ErrIllegalName = errors.New("upload entry name is not a clean relative path")

// File is one uploaded file in the manifest: its path relative to the upload
// root, its byte size, and the number of chunks the client sliced it into (the
// server recomputes the chunk count from size and chunk size, so the client
// value is informational).
type File struct {
  Rel    string `json:"rel"`
  Size   int64  `json:"size"`
  Chunks int    `json:"chunks"`
}

// file is the server-side, chunk-count-normalized form of a manifest entry.
type file struct {
  rel    string
  size   int64
  chunks int
}

// Status is the resumable snapshot a client polls to learn which chunks remain.
type Status struct {
  ChunkSize      int64 `json:"chunkSize"`
  TotalChunks    int   `json:"totalChunks"`
  ReceivedChunks int   `json:"receivedChunks"`
  TotalBytes     int64 `json:"totalBytes"`
  ReceivedBytes  int64 `json:"receivedBytes"`
}

// Session is one staged upload. It is guarded by mu; concurrent chunk writes are
// serialized on mu, and each file has its own fileMu so writes to distinct files
// do not block one another.
type Session struct {
  // ID is the session identifier (a random hex string; never empty once the
  // session is created).
  ID string
  // Dest is the confined, absolute destination folder the finalized upload is
  // copied into.
  Dest string
  // Staging is the dir under tmp_dir holding the staged files.
  Staging string
  // ChunkSize is the chunk size in bytes the client sliced with.
  ChunkSize int64

  files    []file
  dirs     []string
  Present  map[int]bool // global present chunk indices
  Received int64        // total bytes written (unique chunks only)

  mu         sync.Mutex
  fileMu     []sync.Mutex
  lastHtime  time.Time
  aborted    bool
  finalized  bool
  finalizeID string
}

// Registry is the in-memory map of sessions, keyed by ID.
type Registry struct {
  mu       sync.Mutex
  sessions map[string]*Session
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
  return &Registry{sessions: make(map[string]*Session)}
}

// Create stores s. An empty ID is filled with a fresh random one.
func (r *Registry) Create(s *Session) {
  r.mu.Lock()
  defer r.mu.Unlock()
  if s.ID == "" {
    s.ID, _ = newSessionID()
  }
  r.sessions[s.ID] = s
}

// Get returns the session for id, or nil.
func (r *Registry) Get(id string) *Session {
  r.mu.Lock()
  defer r.mu.Unlock()
  return r.sessions[id]
}

// Delete removes the session for id (no-op if absent).
func (r *Registry) Delete(id string) {
  r.mu.Lock()
  defer r.mu.Unlock()
  delete(r.sessions, id)
}

// NewSession builds a session for tmpDir/dest. It normalizes each manifest file's
// chunk count from its size and chunk size and creates the staging dir.
func NewSession(tmpDir, dest string, chunkSize int64, files []File, dirs []string) (*Session, error) {
  if chunkSize <= 0 {
    chunkSize = DefaultChunkSize
  }
  id, err := newSessionID()
  if err != nil {
    return nil, err
  }
  staging := filepath.Join(tmpDir, dirPrefix+id)
  s := &Session{
    ID:        id,
    Dest:      dest,
    Staging:   staging,
    ChunkSize: chunkSize,
    Present:   make(map[int]bool),
    dirs:      dirs,
  }
  for _, f := range files {
    chunks := chunkCount(f.Size, chunkSize)
    s.files = append(s.files, file{rel: f.Rel, size: f.Size, chunks: chunks})
    s.fileMu = append(s.fileMu, sync.Mutex{})
  }
  return s, nil
}

// chunkCount returns the number of chunks a file of size bytes needs at the given
// chunk size. A non-empty chunk size always yields at least one chunk, so even a
// zero-byte file is uploaded (as a single empty chunk) and recreated on finalize.
func chunkCount(size, chunkSize int64) int {
  n := int((size + chunkSize - 1) / chunkSize)
  if n < 1 {
    n = 1
  }
  return n
}

// totalChunks is the sum of every file's chunk count.
func (s *Session) totalChunks() int {
  n := 0
  for _, f := range s.files {
    n += f.chunks
  }
  return n
}

// totalBytes is the sum of every file's size.
func (s *Session) totalBytes() int64 {
  n := int64(0)
  for _, f := range s.files {
    n += f.size
  }
  return n
}

// WriteChunk writes data for fileIdx/chunkIdx at its fixed offset, marking the
// global chunk index present and accounting the bytes. It returns completed when
// this write inserts the final missing chunk (so completion fires exactly once).
// An out-of-range index returns ErrChunkOutOfRange; a cancelled session returns
// ErrAborted (chunks are kept, so the upload stays resumable).
func (s *Session) WriteChunk(fileIdx, chunkIdx int, data []byte) (bool, error) {
  s.mu.Lock()
  defer s.mu.Unlock()
  if s.aborted {
    return false, ErrAborted
  }
  if fileIdx < 0 || fileIdx >= len(s.files) {
    return false, ErrChunkOutOfRange
  }
  f := s.files[fileIdx]
  if chunkIdx < 0 || chunkIdx >= f.chunks {
    return false, ErrChunkOutOfRange
  }

  global := 0
  for i := 0; i < fileIdx; i++ {
    global += s.files[i].chunks
  }
  global += chunkIdx
  offset := int64(chunkIdx) * s.ChunkSize

  s.refreshMtime()

  fmu := &s.fileMu[fileIdx]
  fmu.Lock()
  err := writeAt(filepath.Join(s.Staging, f.rel), offset, data)
  fmu.Unlock()
  if err != nil {
    return false, err
  }

  completed := false
  if !s.Present[global] {
    s.Present[global] = true
    s.Received += int64(len(data))
    if len(s.Present) == s.totalChunks() {
      completed = true
    }
  }
  return completed, nil
}

// writeAt creates (if needed) the file at full and appends data at offset.
func writeAt(full string, offset int64, data []byte) error {
  if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
    return err
  }
  fd, err := os.OpenFile(full, os.O_CREATE|os.O_RDWR, 0o644)
  if err != nil {
    return err
  }
  defer fd.Close()
  if _, err := fd.Seek(offset, io.SeekStart); err != nil {
    return err
  }
  _, err = fd.Write(data)
  return err
}

// refreshMtime bumps the staging dir mtime, throttled, so the age sweep reflects
// the most recent activity (writing file content does not touch the dir mtime).
func (s *Session) refreshMtime() {
  now := time.Now()
  if now.Sub(s.lastHtime) < mtimeThrottle {
    return
  }
  s.lastHtime = now
  _ = os.Chtimes(s.Staging, now, now)
}

// Status returns the resumable snapshot (present chunks, bytes, chunk size).
func (s *Session) Status() Status {
  s.mu.Lock()
  defer s.mu.Unlock()
  return Status{
    ChunkSize:      s.ChunkSize,
    TotalChunks:    s.totalChunks(),
    ReceivedChunks: len(s.Present),
    TotalBytes:     s.totalBytes(),
    ReceivedBytes:  s.Received,
  }
}

// FileCount returns the number of files in the manifest.
func (s *Session) FileCount() int {
  s.mu.Lock()
  defer s.mu.Unlock()
  return len(s.files)
}

// Aborted reports whether the session was cancelled (and thus refuses chunks).
func (s *Session) Aborted() bool {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.aborted
}

// SetAborted marks the session cancelled. Chunks and the staging dir are kept so
// the upload remains resumable until the age sweep reclaims it.
func (s *Session) SetAborted() {
  s.mu.Lock()
  defer s.mu.Unlock()
  s.aborted = true
  s.refreshMtime()
}

// FinalizeOnce flips the finalized flag exactly once and returns true for the
// single call that owns the finalize. The owner then submits the finalize op and
// records its id via SetFinalizeID before the session is dropped from the registry.
func (s *Session) FinalizeOnce() bool {
  s.mu.Lock()
  defer s.mu.Unlock()
  if s.finalized {
    return false
  }
  s.finalized = true
  return true
}

// SetFinalizeID records the finalize task id.
func (s *Session) SetFinalizeID(id string) {
  s.mu.Lock()
  defer s.mu.Unlock()
  s.finalizeID = id
}

// FinalizeID returns the finalize task id, if any.
func (s *Session) FinalizeID() string {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.finalizeID
}

// newSessionID returns a short random hex string used as both the session id and
// the staging dir name suffix (hex never contains the "-" in dirPrefix, so the
// sweep can recover the id by trimming the prefix).
func newSessionID() (string, error) {
  b := make([]byte, 12)
  if _, err := rand.Read(b); err != nil {
    return "", err
  }
  return hex.EncodeToString(b), nil
}
