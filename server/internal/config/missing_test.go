package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingBufferSizes(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Write config without buffer sizes
	content := `addr: ":8080"
allowed_paths:
  - name: Test
    path: ` + tmpDir + `
session_secret: "test-secret-12345678901234567890123456789012"
admin_user: admin
admin_password_hash: ""
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Capture log output
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check that warnings were logged for each missing buffer size
	logOutput := buf.String()
	expectedKeys := []string{
		"copy_buffer_size",
		"move_buffer_size",
		"compress_buffer_size",
		"decompress_buffer_size",
		"finalize_buffer_size",
		"download_buffer_size",
	}

	for _, key := range expectedKeys {
		if !strings.Contains(logOutput, key) {
			t.Errorf("Expected warning for missing key %q not found in log output:\n%s", key, logOutput)
		}
		if !strings.Contains(logOutput, "using default") {
			t.Errorf("Expected 'using default' message not found in log output:\n%s", logOutput)
		}
	}

	// Verify default values are used (in MB)
	if cfg.CopyBufferSize != 8 {
		t.Errorf("CopyBufferSize = %d, want 8", cfg.CopyBufferSize)
	}
	if cfg.MoveBufferSize != 8 {
		t.Errorf("MoveBufferSize = %d, want 8", cfg.MoveBufferSize)
	}
	if cfg.CompressBufferSize != 8 {
		t.Errorf("CompressBufferSize = %d, want 8", cfg.CompressBufferSize)
	}
	if cfg.DecompressBufferSize != 8 {
		t.Errorf("DecompressBufferSize = %d, want 8", cfg.DecompressBufferSize)
	}
	if cfg.FinalizeBufferSize != 8 {
		t.Errorf("FinalizeBufferSize = %d, want 8", cfg.FinalizeBufferSize)
	}
	if cfg.DownloadBufferSize != 8 {
		t.Errorf("DownloadBufferSize = %d, want 8", cfg.DownloadBufferSize)
	}
}

func TestPresentBufferSizes(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	tmpDirForConfig := filepath.Join(tmpDir, "tmp_uploads")
	if err := os.MkdirAll(tmpDirForConfig, 0o700); err != nil {
		t.Fatal(err)
	}

	// Write config WITH all fields explicitly set (including buffer sizes)
	// This simulates a complete config file where user has set all values
	// Values are now in MB (integers 1-1000)
	content := `addr: ":8080"
allowed_paths:
  - name: Test
    path: ` + tmpDir + `
max_parallel_tasks: 4
shutdown_timeout_seconds: 30
gomaxprocs: 0
tmp_dir: ` + tmpDirForConfig + `
upload_stale_after: 24
upload_sweep_interval: 1
upload_chunk_size: 4
upload_max_concurrent_chunks: 4
session_secret: "test-secret-12345678901234567890123456789012"
admin_user: admin
admin_password_hash: ""
copy_buffer_size: 1
move_buffer_size: 2
compress_buffer_size: 4
decompress_buffer_size: 4
finalize_buffer_size: 16
download_buffer_size: 16
debug: false
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Capture log output
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check that NO warnings were logged
	logOutput := buf.String()
	if strings.Contains(logOutput, "using default") {
		t.Errorf("Unexpected 'using default' message found in log output:\n%s", logOutput)
	}

	// Verify custom values are used (in MB)
	if cfg.CopyBufferSize != 1 {
		t.Errorf("CopyBufferSize = %d, want 1", cfg.CopyBufferSize)
	}
	if cfg.MoveBufferSize != 2 {
		t.Errorf("MoveBufferSize = %d, want 2", cfg.MoveBufferSize)
	}
	if cfg.CompressBufferSize != 4 {
		t.Errorf("CompressBufferSize = %d, want 4", cfg.CompressBufferSize)
	}
	if cfg.DecompressBufferSize != 4 {
		t.Errorf("DecompressBufferSize = %d, want 4", cfg.DecompressBufferSize)
	}
	if cfg.FinalizeBufferSize != 16 {
		t.Errorf("FinalizeBufferSize = %d, want 16", cfg.FinalizeBufferSize)
	}
	if cfg.DownloadBufferSize != 16 {
		t.Errorf("DownloadBufferSize = %d, want 16", cfg.DownloadBufferSize)
	}
}
