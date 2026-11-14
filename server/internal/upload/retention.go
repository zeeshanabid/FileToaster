package upload

import (
  "os"
  "path/filepath"
  "strings"
  "time"
)

// ReclaimAll removes every filetoaster-* staging dir under tmpDir. It is the
// startup (restart) reclaim: sessions are in-memory and gone after a restart, so
// leaving their staging dirs behind would orphan them. deleteFn(id) is called
// for each removed dir's session (a no-op once the in-memory registry is empty).
// It returns the number of dirs removed.
func ReclaimAll(tmpDir string, deleteFn func(id string)) (int, error) {
  return reclaim(tmpDir, 0, deleteFn)
}

// ReclaimStale removes filetoaster-* staging dirs whose mtime is older than
// now-threshold. deleteFn(id) is called for each removed dir's session so the
// registry does not retain a staging dir with no live session. It returns the
// number of dirs removed. A threshold of 0 removes everything (used by ReclaimAll).
func ReclaimStale(tmpDir string, threshold time.Duration, deleteFn func(id string)) (int, error) {
  return reclaim(tmpDir, threshold, deleteFn)
}

// reclaim walks tmpDir and removes filetoaster-* dirs. When threshold is non-
// positive every dir is removed regardless of age; otherwise only dirs whose
// mtime is older than now-threshold are removed.
func reclaim(tmpDir string, threshold time.Duration, deleteFn func(id string)) (int, error) {
  entries, err := os.ReadDir(tmpDir)
  if err != nil {
    if os.IsNotExist(err) {
      return 0, nil
    }
    return 0, err
  }
  cutoff := time.Now().Add(-threshold)
  removed := 0
  for _, e := range entries {
    if !e.IsDir() || !strings.HasPrefix(e.Name(), dirPrefix) {
      continue
    }
    // A non-positive threshold removes every staging dir regardless of age
    // (a threshold of 0 means "remove everything"); otherwise only dirs whose
    // mtime is older than now-threshold are removed.
    if threshold > 0 {
      info, err := e.Info()
      if err != nil {
        continue
      }
      if info.ModTime().After(cutoff) {
        continue // recent enough: keep it (resumable)
      }
    }
    id := strings.TrimPrefix(e.Name(), dirPrefix)
    if err := os.RemoveAll(filepath.Join(tmpDir, e.Name())); err != nil {
      return removed, err
    }
    if deleteFn != nil {
      deleteFn(id)
    }
    removed++
  }
  return removed, nil
}
