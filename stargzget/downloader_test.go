package stargzget

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
)

// mockImageAccessor is a mock implementation of ImageAccessor for testing
type mockImageAccessor struct {
	files map[string][]byte // path -> file content
}

func (m *mockImageAccessor) ImageIndex(ctx context.Context) (*ImageIndex, error) {
	// Not needed for downloader tests
	return nil, nil
}

func (m *mockImageAccessor) OpenFile(ctx context.Context, path string, blobDigest digest.Digest) (*io.SectionReader, error) {
	content, ok := m.files[path]
	if !ok {
		return nil, io.EOF
	}
	return io.NewSectionReader(bytes.NewReader(content), 0, int64(len(content))), nil
}

func TestDownloader_StartDownload(t *testing.T) {
	// Create temp directory for test outputs
	tempDir, err := os.MkdirTemp("", "downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Setup mock accessor with test files
	mockAccessor := &mockImageAccessor{
		files: map[string][]byte{
			"bin/echo": []byte("echo content"),
			"bin/cat":  []byte("cat content"),
			"lib/libc": []byte("libc content"),
		},
	}

	downloader := NewDownloader(mockAccessor)

	digest1 := digest.FromString("layer1")

	tests := []struct {
		name            string
		jobs            []*DownloadJob
		wantFiles       int
		wantBytes       int64
		validateContent map[string]string // outputPath -> expected content
	}{
		{
			name: "single file download",
			jobs: []*DownloadJob{
				{
					Path:       "bin/echo",
					BlobDigest: digest1,
					Size:       12,
					OutputPath: filepath.Join(tempDir, "test1", "echo"),
				},
			},
			wantFiles: 1,
			wantBytes: 12,
			validateContent: map[string]string{
				filepath.Join(tempDir, "test1", "echo"): "echo content",
			},
		},
		{
			name: "multiple files download",
			jobs: []*DownloadJob{
				{
					Path:       "bin/echo",
					BlobDigest: digest1,
					Size:       12,
					OutputPath: filepath.Join(tempDir, "test2", "bin", "echo"),
				},
				{
					Path:       "bin/cat",
					BlobDigest: digest1,
					Size:       11,
					OutputPath: filepath.Join(tempDir, "test2", "bin", "cat"),
				},
				{
					Path:       "lib/libc",
					BlobDigest: digest1,
					Size:       12,
					OutputPath: filepath.Join(tempDir, "test2", "lib", "libc"),
				},
			},
			wantFiles: 3,
			wantBytes: 35,
			validateContent: map[string]string{
				filepath.Join(tempDir, "test2", "bin", "echo"): "echo content",
				filepath.Join(tempDir, "test2", "bin", "cat"):  "cat content",
				filepath.Join(tempDir, "test2", "lib", "libc"): "libc content",
			},
		},
		{
			name:      "empty jobs",
			jobs:      []*DownloadJob{},
			wantFiles: 0,
			wantBytes: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Track progress callbacks
			var progressCalls int
			var lastCurrent, lastTotal int64

			progressCallback := func(current, total int64) {
				progressCalls++
				lastCurrent = current
				lastTotal = total
			}

			stats, err := downloader.StartDownload(context.Background(), tt.jobs, progressCallback)
			if err != nil {
				t.Errorf("StartDownload() error = %v", err)
				return
			}

			if stats.DownloadedFiles != tt.wantFiles {
				t.Errorf("StartDownload() downloaded files = %d, want %d", stats.DownloadedFiles, tt.wantFiles)
			}

			if stats.DownloadedBytes != tt.wantBytes {
				t.Errorf("StartDownload() downloaded bytes = %d, want %d", stats.DownloadedBytes, tt.wantBytes)
			}

			// Validate file contents
			for outputPath, expectedContent := range tt.validateContent {
				content, err := os.ReadFile(outputPath)
				if err != nil {
					t.Errorf("Failed to read output file %s: %v", outputPath, err)
					continue
				}

				if string(content) != expectedContent {
					t.Errorf("File %s content = %q, want %q", outputPath, string(content), expectedContent)
				}
			}

			// Validate progress tracking
			if len(tt.jobs) > 0 {
				if progressCalls == 0 {
					t.Error("Progress callback was never called")
				}

				if lastTotal != tt.wantBytes {
					t.Errorf("Progress total = %d, want %d", lastTotal, tt.wantBytes)
				}

				if lastCurrent != tt.wantBytes {
					t.Errorf("Progress current = %d, want %d (should reach total)", lastCurrent, tt.wantBytes)
				}
			}
		})
	}
}

func TestDownloadJob_Creation(t *testing.T) {
	digest1 := digest.FromString("test-digest")

	job := &DownloadJob{
		Path:       "bin/echo",
		BlobDigest: digest1,
		Size:       1024,
		OutputPath: "/tmp/echo",
	}

	if job.Path != "bin/echo" {
		t.Errorf("Job path = %s, want bin/echo", job.Path)
	}

	if job.Size != 1024 {
		t.Errorf("Job size = %d, want 1024", job.Size)
	}

	if job.OutputPath != "/tmp/echo" {
		t.Errorf("Job output path = %s, want /tmp/echo", job.OutputPath)
	}
}
