package upload

import (
  "os"
  "path/filepath"
  "testing"
  "time"
)

// makeStagingDir creates a filetoaster-<name> staging dir with a file inside and
// (when past != nil) sets its mtime to past.
func makeStagingDir(t *testing.T, tmpDir, name string, past *time.Time) string {
  t.Helper()
  dir := filepath.Join(tmpDir, dirPrefix+name)
  if err := os.MkdirAll(dir, 0o755); err != nil {
    t.Fatal(err)
  }
  if err := os.WriteFile(filepath.Join(dir, "chunk"), []byte("x"), 0o644); err != nil {
    t.Fatal(err)
  }
  if past != nil {
    if err := os.Chtimes(dir, *past, *past); err != nil {
      t.Fatal(err)
    }
  }
  return dir
}

func dirExists(path string) bool {
  _, err := os.Stat(path)
  return err == nil
}

func TestReclaimAllRemovesEveryStagingDir(t *testing.T) {
  tmpDir := t.TempDir()
  old := time.Now().Add(-48 * time.Hour)
  m1 := makeStagingDir(t, tmpDir, "aaa", &old)
  m2 := makeStagingDir(t, tmpDir, "bbb", &old)
  // A non-staging dir must be left untouched.
  other := filepath.Join(tmpDir, "keep-me")
  if err := os.MkdirAll(other, 0o755); err != nil {
    t.Fatal(err)
  }

  var removed []string
  n, err := ReclaimAll(tmpDir, func(id string) { removed = append(removed, id) })
  if err != nil {
    t.Fatalf("ReclaimAll: %v", err)
  }
  if n != 2 {
    t.Errorf("ReclaimAll removed %d dirs, want 2", n)
  }
  if dirExists(m1) || dirExists(m2) {
    t.Error("staging dirs not removed by ReclaimAll")
  }
  if !dirExists(other) {
    t.Error("ReclaimAll removed a non-staging dir")
  }
  // The session ids recovered from the dir names are reported to deleteFn.
  if len(removed) != 2 {
    t.Fatalf("deleteFn called %d times, want 2 (%v)", len(removed), removed)
  }
}

func TestReclaimStaleRemovesOnlyDirsOlderThanThreshold(t *testing.T) {
  tmpDir := t.TempDir()
  stale := time.Now().Add(-48 * time.Hour)
  fresh := time.Now()
  mStale := makeStagingDir(t, tmpDir, "stale", &stale)
  mFresh := makeStagingDir(t, tmpDir, "fresh", &fresh)
  // A non-staging dir must be left untouched.
  if err := os.MkdirAll(filepath.Join(tmpDir, "ignore"), 0o755); err != nil {
    t.Fatal(err)
  }

  var removed []string
  n, err := ReclaimStale(tmpDir, 24*time.Hour, func(id string) { removed = append(removed, id) })
  if err != nil {
    t.Fatalf("ReclaimStale: %v", err)
  }
  if n != 1 {
    t.Errorf("ReclaimStale removed %d dirs, want 1", n)
  }
  if dirExists(mStale) {
    t.Error("staging dir older than threshold not reclaimed")
  }
  if !dirExists(mFresh) {
    t.Error("recent staging dir was reclaimed but should have been kept")
  }
  if len(removed) != 1 || removed[0] != "stale" {
    t.Errorf("deleteFn ids = %v, want [stale]", removed)
  }
}

func TestReclaimStaleThresholdZeroRemovesEverything(t *testing.T) {
  tmpDir := t.TempDir()
  future := time.Now().Add(time.Hour)
  mk := makeStagingDir(t, tmpDir, "x", &future) // future mtime
  if _, err := ReclaimStale(tmpDir, 0, nil); err != nil {
    t.Fatalf("ReclaimStale(threshold 0): %v", err)
  }
  if dirExists(mk) {
    t.Error("threshold 0 must remove every staging dir regardless of mtime")
  }
}

func TestReclaimIgnoresMissingTmpDir(t *testing.T) {
  missing := filepath.Join(t.TempDir(), "does-not-exist")
  if _, err := ReclaimAll(missing, nil); err != nil {
    t.Errorf("ReclaimAll on missing dir: %v, want nil", err)
  }
  if _, err := ReclaimStale(missing, time.Hour, nil); err != nil {
    t.Errorf("ReclaimStale on missing dir: %v, want nil", err)
  }
}
