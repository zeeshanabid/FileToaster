// Package task is FileToaster's async task engine: tracked operations that run
// behind a bounded worker pool, report progress through a thread-safe store, and
// stream status deltas to clients over Server-Sent Events.
//
// The layering is intentionally decoupled so the engine can be unit-tested and
// reused by any operation:
//
//   - Task / Status / ByteProgress describe a single unit of work.
//   - Progression is how the work reports bytes and files back to the engine.
//   - Store is the in-memory, thread-safe source of truth (survives browser
//     refresh; cleared only by a server restart).
//   - Pool runs work with bounded parallelism and an in-memory overflow queue,
//     and supports cancelling queued or running work.
//   - Hub publishes task deltas to subscribed SSE clients.
package task

import (
  "encoding/json"
  "time"
)

// Status is the lifecycle state of a task.
type Status string

const (
  // StatusQueued means the task is waiting for a free worker slot.
  StatusQueued Status = "queued"
  // StatusRunning means a worker is executing the task.
  StatusRunning Status = "running"
  // StatusFinished means the task completed successfully.
  StatusFinished Status = "finished"
  // StatusFailed means the task returned an error.
  StatusFailed Status = "failed"
  // StatusCancelled means the task was cancelled before completing.
  StatusCancelled Status = "cancelled"
)

// IsTerminal reports whether the status is a terminal (no further transitions)
// state.
func (s Status) IsTerminal() bool {
  switch s {
  case StatusFinished, StatusFailed, StatusCancelled:
    return true
  }
  return false
}

// ByteProgress reports byte/file accounting and transfer rate for a task. Bytes
// are the unit progress tasks show (for copy, the bytes copied); RatePerSec is a
// smoothed throughput used to render KB/s or MB/s on the client.
type ByteProgress struct {
  // Total is the total size of the work in bytes (0 when unknown until the
  // task measures it).
  Total int64 `json:"total"`
  // Done is the bytes completed so far.
  Done int64 `json:"done"`
  // FilesTotal is the number of files the work expects to process (0 when the
  // work is not file-based).
  FilesTotal int `json:"filesTotal"`
  // FilesDone is the number of files completed so far.
  FilesDone int `json:"filesDone"`
  // RatePerSec is the smoothed bytes-per-second throughput.
  RatePerSec int64 `json:"ratePerSec"`
}

// Percent returns progress as a 0..100 floating point number, preferring the
// byte measure and falling back to the file measure when no byte total is set.
func (bp ByteProgress) Percent() float64 {
  switch {
  case bp.Total > 0:
    p := float64(bp.Done) / float64(bp.Total) * 100
    return clamp01(p)
  case bp.FilesTotal > 0:
    return float64(bp.FilesDone) / float64(bp.FilesTotal) * 100
  default:
    return 0
  }
}

func clamp01(v float64) float64 {
  if v < 0 {
    return 0
  }
  if v > 100 {
    return 100
  }
  return v
}

// Task is the serializable record of a single unit of work.
type Task struct {
  // ID is the unique task identifier.
  ID string `json:"id"`
  // Type is the operation key, e.g. "copy".
  Type string `json:"type"`
  // Status is the current lifecycle state.
  Status Status `json:"status"`
  // Progress is 0..100 percent complete.
  Progress float64 `json:"progress"`
  // Created and Updated are UTC timestamps.
  Created time.Time `json:"created"`
  Updated time.Time `json:"updated"`
  // Error carries the first user-facing message on failure or cancellation.
  Error string `json:"error,omitempty"`
  // Bytes carries the byte/file progress and throughput.
  Bytes ByteProgress `json:"bytes"`
  // Meta carries operation-specific metadata (opaque to the engine).
  Meta json.RawMessage `json:"meta,omitempty"`
}
