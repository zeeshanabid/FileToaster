package upload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"filetoaster/internal/task"
)

// writeTree creates the given file/dir layout under root (a value maps to a file
// with that content; an empty string maps to an empty directory).
func writeTree(t *testing.T, root string, layout map[string]string) {
	t.Helper()
	for rel, content := range layout {
		full := filepath.Join(root, rel)
		if content == "" {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readIfExists(t *testing.T, path string) (string, bool) {
	t.Helper()
	got, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(got), true
}

func TestFinalizeCopiesStagingIntoTargetAndRemovesStaging(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	dest := filepath.Join(tmp, "dest")
	writeTree(t, staging, map[string]string{
		"a.txt":      "alpha",
		"sub/b.txt":  "beta",
		"sub/empty/": "",
	})

	if err := Finalize(context.Background(), staging, dest, &task.Progression{}, DefaultFinalizeConfig()); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	if got, _ := readIfExists(t, filepath.Join(dest, "a.txt")); got != "alpha" {
		t.Errorf("dest/a.txt = %q, want %q", got, "alpha")
	}
	if got, _ := readIfExists(t, filepath.Join(dest, "sub", "b.txt")); got != "beta" {
		t.Errorf("dest/sub/b.txt = %q, want %q", got, "beta")
	}
	// The empty directory is recreated at the target.
	if info, err := os.Stat(filepath.Join(dest, "sub", "empty")); err != nil || !info.IsDir() {
		t.Errorf("dest/sub/empty dir not recreated: err=%v", err)
	}
	// On success the staging dir is consumed (removed).
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staging not removed on success: err=%v", err)
	}
}

func TestFinalizeUniqueDestOnCollision(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	dest := filepath.Join(tmp, "dest")
	writeTree(t, staging, map[string]string{"a.txt": "new"})
	// A same-named file already exists at the target.
	writeTree(t, dest, map[string]string{"a.txt": "existing"})

	if err := Finalize(context.Background(), staging, dest, &task.Progression{}, DefaultFinalizeConfig()); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// The existing file is preserved and the staged file lands on a unique name.
	if got, _ := readIfExists(t, filepath.Join(dest, "a.txt")); got != "existing" {
		t.Errorf("dest/a.txt = %q, want %q (existing preserved)", got, "existing")
	}
	got, ok := readIfExists(t, filepath.Join(dest, "a (copy).txt"))
	if !ok || got != "new" {
		t.Errorf("dest/a (copy).txt = %q, ok=%v, want %q", got, ok, "new")
	}
}

func TestFinalizeCancelAbortsAndKeepsStaging(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	dest := filepath.Join(tmp, "dest")
	writeTree(t, staging, map[string]string{"a.txt": "alpha"})

	// A pre-cancelled context aborts immediately, before writing anything.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Finalize(ctx, staging, dest, &task.Progression{}, DefaultFinalizeConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Finalize with cancelled context: err = %v, want context.Canceled", err)
	}
	// The staging dir is kept so the copy can be retried.
	if _, err := os.Stat(staging); err != nil {
		t.Errorf("staging should survive a cancelled Finalize: err=%v", err)
	}
}

func TestFinalizeFailureKeepsStaging(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "staging")
	dest := filepath.Join(tmp, "dest")
	writeTree(t, staging, map[string]string{"sub/x.txt": "data"})
	// Make dest/sub a file so MkdirAll(dest/sub) during the copy fails.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "sub"), []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Finalize(context.Background(), staging, dest, &task.Progression{}, DefaultFinalizeConfig())
	if err == nil {
		t.Fatal("Finalize should fail when the destination path is not creatable")
	}
	// On failure the staging dir is kept so the copy can be retried.
	if _, err := os.Stat(staging); err != nil {
		t.Errorf("staging should survive a failed Finalize: err=%v", err)
	}
}
