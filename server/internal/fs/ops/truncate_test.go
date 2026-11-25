package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"filetoaster/internal/fs"
	"filetoaster/internal/task"
)

func TestTruncateResolve(t *testing.T) {
	tmpDir := t.TempDir()
	roots := fs.NewRoots(tmpDir)

	// Create test files
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")
	dir1 := filepath.Join(tmpDir, "dir1")
	os.WriteFile(file1, []byte("hello world"), 0644)
	os.WriteFile(file2, []byte("test content"), 0644)
	os.Mkdir(dir1, 0755)

	tests := []struct {
		name       string
		paths      []string
		size       int64
		wantErr    bool
		errMessage string
	}{
		{
			name:    "truncate single file",
			paths:   []string{file1},
			size:    5,
			wantErr: false,
		},
		{
			name:    "truncate multiple files",
			paths:   []string{file1, file2},
			size:    10,
			wantErr: false,
		},
		{
			name:       "truncate with directory fails",
			paths:      []string{dir1},
			size:       5,
			wantErr:    true,
			errMessage: "truncate operation only supports files, not directories",
		},
		{
			name:       "truncate with negative size fails",
			paths:      []string{file1},
			size:       -1,
			wantErr:    true,
			errMessage: "target size must be a non-negative integer",
		},
		{
			name:       "truncate empty paths fails",
			paths:      []string{},
			size:       5,
			wantErr:    true,
			errMessage: "at least one path is required",
		},
		{
			name:    "truncate file to zero size",
			paths:   []string{file1},
			size:    0,
			wantErr: false,
		},
		{
			name:    "truncate file to larger size",
			paths:   []string{file1},
			size:    100,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := TruncateResolve(roots, tt.paths, tt.size)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if tt.errMessage != "" && !contains(err.Error(), tt.errMessage) {
					t.Errorf("expected error containing %q, got %q", tt.errMessage, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if plan == nil {
					t.Errorf("expected plan but got nil")
				} else if len(plan.Items) != len(tt.paths) {
					t.Errorf("expected %d items, got %d", len(tt.paths), len(plan.Items))
				}
				for i, item := range plan.Items {
					if item.Size != tt.size {
						t.Errorf("item %d: expected size %d, got %d", i, tt.size, item.Size)
					}
				}
			}
		})
	}
}

func TestTruncateRun(t *testing.T) {
	tmpDir := t.TempDir()
	roots := fs.NewRoots(tmpDir)

	// Create test files
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")
	os.WriteFile(file1, []byte("hello world"), 0644)  // 11 bytes
	os.WriteFile(file2, []byte("test content"), 0644) // 12 bytes

	// Test truncate to smaller size
	t.Run("truncate to smaller size", func(t *testing.T) {
		plan, err := TruncateResolve(roots, []string{file1, file2}, 5)
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}

		prog := &task.Progression{}
		err = TruncateRun(context.Background(), plan, prog, DefaultTruncateConfig())
		if err != nil {
			t.Errorf("run failed: %v", err)
		}

		// Verify file sizes
		info1, _ := os.Stat(file1)
		info2, _ := os.Stat(file2)
		if info1.Size() != 5 {
			t.Errorf("file1 size: expected 5, got %d", info1.Size())
		}
		if info2.Size() != 5 {
			t.Errorf("file2 size: expected 5, got %d", info2.Size())
		}

		// Verify progress
		snap := prog.Snapshot()
		if snap.FilesDone != 2 {
			t.Errorf("FilesDone: expected 2, got %d", snap.FilesDone)
		}
	})

	// Test truncate to zero size
	t.Run("truncate to zero size", func(t *testing.T) {
		file3 := filepath.Join(tmpDir, "file3.txt")
		os.WriteFile(file3, []byte("content to remove"), 0644)

		plan, err := TruncateResolve(roots, []string{file3}, 0)
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}

		prog := &task.Progression{}
		err = TruncateRun(context.Background(), plan, prog, DefaultTruncateConfig())
		if err != nil {
			t.Errorf("run failed: %v", err)
		}

		info, _ := os.Stat(file3)
		if info.Size() != 0 {
			t.Errorf("file3 size: expected 0, got %d", info.Size())
		}
	})

	// Test truncate to larger size (extend with zeros)
	t.Run("truncate to larger size", func(t *testing.T) {
		file4 := filepath.Join(tmpDir, "file4.txt")
		os.WriteFile(file4, []byte("short"), 0644) // 5 bytes

		plan, err := TruncateResolve(roots, []string{file4}, 20)
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}

		prog := &task.Progression{}
		err = TruncateRun(context.Background(), plan, prog, DefaultTruncateConfig())
		if err != nil {
			t.Errorf("run failed: %v", err)
		}

		info, _ := os.Stat(file4)
		if info.Size() != 20 {
			t.Errorf("file4 size: expected 20, got %d", info.Size())
		}

		// Verify content is padded with zeros
		content, _ := os.ReadFile(file4)
		if len(content) != 20 {
			t.Errorf("content length: expected 20, got %d", len(content))
		}
		if string(content[:5]) != "short" {
			t.Errorf("content prefix: expected 'short', got %q", string(content[:5]))
		}
		for i := 5; i < 20; i++ {
			if content[i] != 0 {
				t.Errorf("content[%d]: expected 0, got %d", i, content[i])
			}
		}
	})

	// Test cancellation
	t.Run("cancelled context", func(t *testing.T) {
		file5 := filepath.Join(tmpDir, "file5.txt")
		os.WriteFile(file5, []byte("test"), 0644)

		plan, err := TruncateResolve(roots, []string{file5}, 2)
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		prog := &task.Progression{}
		err = TruncateRun(ctx, plan, prog, DefaultTruncateConfig())
		if err == nil || err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
