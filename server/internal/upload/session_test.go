package upload

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// twoFileManifest returns two files (a.txt = 10 bytes -> 3 chunks at chunkSize,
// sub/b.txt = 4 bytes -> 1 chunk) and the shared dir so the test can drive
// WriteChunk deterministically.
func twoFileManifest(chunkSize int64) ([]File, []string, int) {
	files := []File{
		{Rel: "a.txt", Size: 10},
		{Rel: filepath.Join("sub", "b.txt"), Size: 4},
	}
	return files, []string{"sub"}, chunkCount(10, chunkSize) + chunkCount(4, chunkSize)
}

func TestWriteChunkWritesAtOffsetAndDetectsCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	chunkSize := int64(4)
	files, dirs, totalChunks := twoFileManifest(chunkSize)
	sess, err := NewSession(tmpDir, dest, chunkSize, files, dirs)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("session created with empty ID")
	}

	write := func(fileIdx, chunkIdx int, data string) bool {
		completed, err := sess.WriteChunk(fileIdx, chunkIdx, []byte(data))
		if err != nil {
			t.Fatalf("WriteChunk(%d,%d): %v", fileIdx, chunkIdx, err)
		}
		return completed
	}

	// file 0 (a.txt): 3 chunks at offsets 0, 4, 8.
	if write(0, 0, "0123") {
		t.Fatal("file 0 chunk 0 should not complete the upload")
	}
	if write(0, 1, "4567") {
		t.Fatal("file 0 chunk 1 should not complete the upload")
	}
	// The chunk after the second-to-last does not complete; the final chunk of
	// file 1 is still pending.
	if write(0, 2, "89") {
		t.Fatal("writing the second-to-last chunk must not complete; the last chunk still pending")
	}
	// file 1 (sub/b.txt): 1 chunk at global index 3 completes the upload.
	if !write(1, 0, "0123") {
		t.Fatal("the last chunk must complete the upload")
	}

	// Completion fires exactly once: any later write reports not-completed.
	if write(0, 0, "0123") {
		t.Fatal("a duplicate write after completion must not report completion again")
	}

	gotA, err := os.ReadFile(filepath.Join(sess.Staging, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(gotA) != "0123456789" {
		t.Fatalf("a.txt = %q, want %q", gotA, "0123456789")
	}
	gotB, err := os.ReadFile(filepath.Join(sess.Staging, "sub", "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if string(gotB) != "0123" {
		t.Fatalf("b.txt = %q, want %q", gotB, "0123")
	}

	st := sess.Status()
	if st.TotalChunks != totalChunks {
		t.Errorf("TotalChunks = %d, want %d", st.TotalChunks, totalChunks)
	}
	if st.ReceivedChunks != totalChunks {
		t.Errorf("ReceivedChunks = %d, want %d", st.ReceivedChunks, totalChunks)
	}
	if st.TotalBytes != 14 {
		t.Errorf("TotalBytes = %d, want 14", st.TotalBytes)
	}
	if st.ReceivedBytes != 14 {
		t.Errorf("ReceivedBytes = %d, want 14 (unique chunks only)", st.ReceivedBytes)
	}
}

func TestWriteChunkDuplicateChunkDoesNotDoubleCount(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(tmpDir, dest, 4, []File{{Rel: "a.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	completed, err := sess.WriteChunk(0, 0, []byte("abcd"))
	if err != nil || !completed {
		t.Fatalf("first write: completed=%v err=%v", completed, err)
	}
	// Re-sending the same chunk must not change received bytes or re-complete.
	completed, err = sess.WriteChunk(0, 0, []byte("abcd"))
	if err != nil {
		t.Fatalf("duplicate write: %v", err)
	}
	if completed {
		t.Fatal("duplicate chunk must not report completion")
	}
	st := sess.Status()
	if st.ReceivedBytes != 4 {
		t.Errorf("ReceivedBytes after duplicate = %d, want 4", st.ReceivedBytes)
	}
	if st.ReceivedChunks != 1 {
		t.Errorf("ReceivedChunks after duplicate = %d, want 1", st.ReceivedChunks)
	}
}

func TestWriteChunkOutOfRangeRejected(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(tmpDir, dest, 4, []File{{Rel: "a.txt", Size: 4}, {Rel: "b.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := sess.WriteChunk(0, 4, []byte("x")); !errors.Is(err, ErrChunkOutOfRange) {
		t.Errorf("chunkIdx out of range: err = %v, want ErrChunkOutOfRange", err)
	}
	if _, err := sess.WriteChunk(5, 0, []byte("x")); !errors.Is(err, ErrChunkOutOfRange) {
		t.Errorf("fileIdx out of range: err = %v, want ErrChunkOutOfRange", err)
	}
}

func TestWriteChunkAbortedSessionRefusesChunks(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(tmpDir, dest, 4, []File{{Rel: "a.txt", Size: 8}}, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// An already-received chunk stays staged (resumable) after abort.
	if _, err := sess.WriteChunk(0, 0, []byte("abcd")); err != nil {
		t.Fatalf("pre-abort write: %v", err)
	}
	sess.SetAborted()
	if _, err := sess.WriteChunk(0, 1, []byte("efgh")); !errors.Is(err, ErrAborted) {
		t.Errorf("write on aborted session: err = %v, want ErrAborted", err)
	}
	// The staged chunk survived.
	got, err := os.ReadFile(filepath.Join(sess.Staging, "a.txt"))
	if err != nil {
		t.Fatalf("read staged chunk: %v", err)
	}
	if string(got) != "abcd" {
		t.Fatalf("staged chunk = %q, want %q", got, "abcd")
	}
}

func TestZeroByteFileGetsAChunk(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(tmpDir, dest, 4, []File{{Rel: "empty.txt", Size: 0}}, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// A zero-byte file is a single (empty) chunk, so it is uploaded and recreated.
	completed, err := sess.WriteChunk(0, 0, []byte(""))
	if err != nil {
		t.Fatalf("write empty chunk: %v", err)
	}
	if !completed {
		t.Fatal("the single empty chunk must complete the upload")
	}
	if st := sess.Status(); st.TotalChunks != 1 {
		t.Fatalf("TotalChunks = %d, want 1 (a zero-byte file needs one chunk)", st.TotalChunks)
	}
}

func TestRegistryCreateAssignsIDAndGetDelete(t *testing.T) {
	reg := NewRegistry()
	sess, err := NewSession(t.TempDir(), filepath.Join(t.TempDir(), "dest"), 4, []File{{Rel: "a.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	reg.Create(sess)
	if sess.ID == "" {
		t.Fatal("Create did not assign an ID")
	}
	if got := reg.Get(sess.ID); got != sess {
		t.Fatal("Get returned a different session")
	}
	if reg.Get("missing") != nil {
		t.Fatal("Get on unknown ID should return nil")
	}
	reg.Delete(sess.ID)
	if reg.Get(sess.ID) != nil {
		t.Fatal("Delete did not remove the session")
	}
}

func TestSessionIDIsHexAndRecoverableFromStagingName(t *testing.T) {
	sess, err := NewSession(t.TempDir(), filepath.Join(t.TempDir(), "dest"), 4, []File{{Rel: "a.txt", Size: 4}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(sess.Staging)
	if base != dirPrefix+sess.ID {
		t.Fatalf("staging dir = %q, want %q%q", base, dirPrefix, sess.ID)
	}
}
