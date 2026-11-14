package main

import (
  "context"
  "log/slog"
  "time"

  "filetoaster/internal/upload"
)

// uploadCleanup owns the background upload-staging age sweep. The sweep runs on a
// ticker and is stopped (via its context) on shutdown; it occupies no task-pool
// slot and is never a tracked task.
type uploadCleanup struct {
  cancel context.CancelFunc
  done   chan struct{}
}

// startUploadCleanup runs the age sweep over tmpDir on a ticker ticking every
// interval. It removes staging dirs older than staleAfter (and drops their
// session via deleteFn). The returned value stops and reports completion.
func startUploadCleanup(tmpDir string, staleAfter, interval time.Duration, deleteFn func(string), logger *slog.Logger) uploadCleanup {
  ctx, cancel := context.WithCancel(context.Background())
  done := make(chan struct{})
  go func() {
    defer close(done)
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    for {
      select {
      case <-ctx.Done():
        return
      case <-ticker.C:
        n, err := upload.ReclaimStale(tmpDir, staleAfter, deleteFn)
        if err != nil {
          logger.Error("upload cleanup failed", "error", err.Error())
          continue
        }
        if n > 0 {
          logger.Info("reclaimed stale upload staging", "dirs", n)
        }
      }
    }
  }()
  return uploadCleanup{cancel: cancel, done: done}
}
