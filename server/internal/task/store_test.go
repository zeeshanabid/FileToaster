package task

import (
	"reflect"
	"testing"
)

// TestStoreClearTerminal verifies that clearing the task panel removes only
// terminal records: finished, failed and cancelled tasks disappear (so they do
// not return in the snapshot after a page refresh) while running and queued work
// stays tracked.
func TestStoreClearTerminal(t *testing.T) {
	s := NewStore()
	s.Add(&Task{ID: "run", Type: "copy", Status: StatusRunning})
	s.Add(&Task{ID: "queued", Type: "copy", Status: StatusQueued})
	s.Add(&Task{ID: "finished", Type: "upload", Status: StatusFinished})
	s.Add(&Task{ID: "failed", Type: "upload", Status: StatusFailed})
	s.Add(&Task{ID: "cancelled", Type: "copy", Status: StatusCancelled})

	removed := s.ClearTerminal()
	want := []string{"cancelled", "failed", "finished"}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("ClearTerminal() = %v, want %v", removed, want)
	}

	remaining := s.List()
	ids := make([]string, 0, len(remaining))
	for _, task := range remaining {
		ids = append(ids, task.ID)
	}
	if !reflect.DeepEqual(ids, []string{"queued", "run"}) {
		t.Fatalf("remaining tasks = %v, want [queued run]", ids)
	}

	// A second clear finds no terminal tasks and leaves the active ones alone.
	if removed := s.ClearTerminal(); len(removed) != 0 {
		t.Fatalf("second ClearTerminal() removed %v, want none", removed)
	}
}
