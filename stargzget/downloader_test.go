package stargzget

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	stargzerrors "github.com/flaneur2020/stargz-get/stargzget/errors"
	"github.com/flaneur2020/stargz-get/stargzget/estargzutil"
	stor "github.com/flaneur2020/stargz-get/stargzget/storage"
	"github.com/opencontainers/go-digest"
)

type mockBlobResolver struct {
	files     map[string][]byte
	chunkSize int64
}

func (m *mockBlobResolver) FileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error) {
	content, ok := m.files[path]
	if !ok {
		return nil, stargzerrors.ErrFileNotFound.WithDetail("path", path)
	}

	size := int64(len(content))
	if size == 0 {
		return &FileMetadata{Size: 0, Chunks: []Chunk{}}, nil
	}

	chunkSize := m.chunkSize
	if chunkSize <= 0 || chunkSize > size {
		chunkSize = size
	}

	chunks := make([]Chunk, 0, (size+chunkSize-1)/chunkSize)
	for offset := int64(0); offset < size; offset += chunkSize {
		remaining := size - offset
		current := chunkSize
		if remaining < current {
			current = remaining
		}
		if current <= 0 {
			break
		}
		chunks = append(chunks, Chunk{Offset: offset, Size: current})
	}

	return &FileMetadata{Size: size, Chunks: chunks}, nil
}

func (m *mockBlobResolver) ReadChunk(ctx context.Context, blobDigest digest.Digest, path string, chunk Chunk) ([]byte, error) {
	content, ok := m.files[path]
	if !ok {
		return nil, stargzerrors.ErrFileNotFound.WithDetail("path", path)
	}

	start := int(chunk.Offset)
	end := start + int(chunk.Size)
	if start < 0 || end > len(content) {
		return nil, io.ErrUnexpectedEOF
	}

	buf := make([]byte, int(chunk.Size))
	copy(buf, content[start:end])
	return buf, nil
}

func (m *mockBlobResolver) TOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error) {
	return &estargzutil.JTOC{}, nil
}

func TestDownloader_StartDownload(t *testing.T) {
	// Create temp directory for test outputs
	tempDir, err := os.MkdirTemp("", "downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Setup mock resolver with test files
	mockResolver := &mockBlobResolver{
		files: map[string][]byte{
			"bin/echo": []byte("echo content"),
			"bin/cat":  []byte("cat content"),
			"lib/libc": []byte("libc content"),
		},
	}

	downloader := NewDownloader(mockResolver)

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
			var lastTotal, maxCurrent int64

			progressCallback := func(current, total int64) {
				progressCalls++
				lastTotal = total
				if current > maxCurrent {
					maxCurrent = current
				}
			}

			stats, err := downloader.StartDownload(context.Background(), tt.jobs, progressCallback, nil)
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

				if maxCurrent != tt.wantBytes {
					t.Errorf("Progress max current = %d, want %d (should reach total)", maxCurrent, tt.wantBytes)
				}
			}
		})
	}
}

func TestDownloader_SingleFileChunkedDownload(t *testing.T) {
	tempDir := t.TempDir()

	content := bytes.Repeat([]byte("chunk-data"), 64) // 640 bytes
	mockResolver := &mockBlobResolver{
		files: map[string][]byte{
			"usr/bin/bash": content,
		},
		chunkSize: 128,
	}

	downloader := NewDownloader(mockResolver)
	job := &DownloadJob{
		Path:       "usr/bin/bash",
		BlobDigest: digest.FromString("blob"),
		Size:       int64(len(content)),
		OutputPath: filepath.Join(tempDir, "bash"),
	}

	var lastCurrent int64
	var progressCalls int
	progress := func(current, total int64) {
		progressCalls++
		lastCurrent = current
	}

	opts := &DownloadOptions{
		Concurrency:              4,
		SingleFileChunkThreshold: 256,
	}

	stats, err := downloader.StartDownload(context.Background(), []*DownloadJob{job}, progress, opts)
	if err != nil {
		t.Fatalf("StartDownload() unexpected error: %v", err)
	}

	if stats.DownloadedFiles != 1 {
		t.Fatalf("DownloadedFiles = %d, want 1", stats.DownloadedFiles)
	}

	if stats.DownloadedBytes != int64(len(content)) {
		t.Fatalf("DownloadedBytes = %d, want %d", stats.DownloadedBytes, len(content))
	}

	data, err := os.ReadFile(job.OutputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("output content mismatch")
	}

	if progressCalls == 0 {
		t.Fatalf("expected progress callback to be invoked")
	}
	if lastCurrent != int64(len(content)) {
		t.Fatalf("progress current = %d, want %d", lastCurrent, len(content))
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

type mockFailingBlobResolver struct {
	files        map[string][]byte
	failCount    map[string]int
	attemptCount map[string]int
	mu           sync.Mutex
}

func (m *mockFailingBlobResolver) FileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error) {
	content, ok := m.files[path]
	if !ok {
		return nil, stargzerrors.ErrFileNotFound.WithDetail("path", path)
	}

	size := int64(len(content))
	chunks := []Chunk{}
	if size > 0 {
		chunks = append(chunks, Chunk{Offset: 0, Size: size})
	}

	return &FileMetadata{Size: size, Chunks: chunks}, nil
}

func (m *mockFailingBlobResolver) ReadChunk(ctx context.Context, blobDigest digest.Digest, path string, chunk Chunk) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.attemptCount == nil {
		m.attemptCount = make(map[string]int)
	}

	m.attemptCount[path]++

	if failTimes, exists := m.failCount[path]; exists {
		if m.attemptCount[path] <= failTimes {
			return nil, io.ErrUnexpectedEOF
		}
	}

	content, ok := m.files[path]
	if !ok {
		return nil, io.EOF
	}

	start := int(chunk.Offset)
	end := start + int(chunk.Size)
	if start < 0 || end > len(content) {
		return nil, io.ErrUnexpectedEOF
	}

	buf := make([]byte, int(chunk.Size))
	copy(buf, content[start:end])
	return buf, nil
}

func (m *mockFailingBlobResolver) TOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error) {
	return &estargzutil.JTOC{}, nil
}

func TestDownloader_StartDownload_WithRetries(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "downloader-retry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name        string
		failCount   map[string]int // path -> number of times to fail before success
		maxRetries  int
		wantSuccess int
		wantFailed  int
		wantRetries int
	}{
		{
			name: "succeed on first attempt",
			failCount: map[string]int{
				"file1": 0, // no failures
			},
			maxRetries:  3,
			wantSuccess: 1,
			wantFailed:  0,
			wantRetries: 0,
		},
		{
			name: "succeed after 1 retry",
			failCount: map[string]int{
				"file1": 1, // fail once, then succeed
			},
			maxRetries:  3,
			wantSuccess: 1,
			wantFailed:  0,
			wantRetries: 1,
		},
		{
			name: "succeed after 2 retries",
			failCount: map[string]int{
				"file1": 2, // fail twice, then succeed
			},
			maxRetries:  3,
			wantSuccess: 1,
			wantFailed:  0,
			wantRetries: 2,
		},
		{
			name: "fail after max retries",
			failCount: map[string]int{
				"file1": 10, // always fail
			},
			maxRetries:  3,
			wantSuccess: 0,
			wantFailed:  1,
			wantRetries: 3,
		},
		{
			name: "mixed success and failure",
			failCount: map[string]int{
				"file1": 0,  // succeed immediately
				"file2": 1,  // succeed after 1 retry
				"file3": 10, // fail after all retries
			},
			maxRetries:  2,
			wantSuccess: 2,
			wantFailed:  1,
			wantRetries: 3, // 0 for file1, 1 for file2, 2 for file3
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockResolver := &mockFailingBlobResolver{
				files: map[string][]byte{
					"file1": []byte("content1"),
					"file2": []byte("content2"),
					"file3": []byte("content3"),
				},
				failCount:    tt.failCount,
				attemptCount: make(map[string]int),
			}

			downloader := NewDownloader(mockResolver)

			var jobs []*DownloadJob
			for path := range tt.failCount {
				jobs = append(jobs, &DownloadJob{
					Path:       path,
					BlobDigest: digest.FromString("test"),
					Size:       int64(len(mockResolver.files[path])),
					OutputPath: filepath.Join(tempDir, tt.name, path),
				})
			}

			opts := &DownloadOptions{
				MaxRetries: tt.maxRetries,
			}

			stats, err := downloader.StartDownload(context.Background(), jobs, nil, opts)
			if err != nil {
				t.Errorf("StartDownload() unexpected error: %v", err)
				return
			}

			if stats.DownloadedFiles != tt.wantSuccess {
				t.Errorf("DownloadedFiles = %d, want %d", stats.DownloadedFiles, tt.wantSuccess)
			}

			if stats.FailedFiles != tt.wantFailed {
				t.Errorf("FailedFiles = %d, want %d", stats.FailedFiles, tt.wantFailed)
			}

			if stats.Retries != tt.wantRetries {
				t.Errorf("Retries = %d, want %d", stats.Retries, tt.wantRetries)
			}
		})
	}
}

func TestDownloader_Concurrency(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "downloader-concurrency-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock accessor with multiple files
	mockResolver := &mockBlobResolver{
		files: map[string][]byte{
			"file1": []byte("content1"),
			"file2": []byte("content2"),
			"file3": []byte("content3"),
			"file4": []byte("content4"),
			"file5": []byte("content5"),
			"file6": []byte("content6"),
			"file7": []byte("content7"),
			"file8": []byte("content8"),
		},
	}

	downloader := NewDownloader(mockResolver)

	// Create 8 download jobs
	var jobs []*DownloadJob
	for i := 1; i <= 8; i++ {
		path := "file" + string(rune('0'+i))
		jobs = append(jobs, &DownloadJob{
			Path:       path,
			BlobDigest: digest.FromString("test"),
			Size:       8,
			OutputPath: filepath.Join(tempDir, path),
		})
	}

	tests := []struct {
		name        string
		concurrency int
		wantFiles   int
		wantBytes   int64
	}{
		{
			name:        "sequential (concurrency=1)",
			concurrency: 1,
			wantFiles:   8,
			wantBytes:   64,
		},
		{
			name:        "parallel with 2 workers",
			concurrency: 2,
			wantFiles:   8,
			wantBytes:   64,
		},
		{
			name:        "parallel with 4 workers",
			concurrency: 4,
			wantFiles:   8,
			wantBytes:   64,
		},
		{
			name:        "parallel with 8 workers",
			concurrency: 8,
			wantFiles:   8,
			wantBytes:   64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &DownloadOptions{
				MaxRetries:  3,
				Concurrency: tt.concurrency,
			}

			stats, err := downloader.StartDownload(context.Background(), jobs, nil, opts)
			if err != nil {
				t.Errorf("StartDownload() error = %v", err)
				return
			}

			if stats.DownloadedFiles != tt.wantFiles {
				t.Errorf("DownloadedFiles = %d, want %d", stats.DownloadedFiles, tt.wantFiles)
			}

			if stats.DownloadedBytes != tt.wantBytes {
				t.Errorf("DownloadedBytes = %d, want %d", stats.DownloadedBytes, tt.wantBytes)
			}

			if stats.FailedFiles != 0 {
				t.Errorf("FailedFiles = %d, want 0", stats.FailedFiles)
			}

			// Verify all files were created with correct content
			for i := 1; i <= 8; i++ {
				path := "file" + string(rune('0'+i))
				content, err := os.ReadFile(filepath.Join(tempDir, path))
				if err != nil {
					t.Errorf("Failed to read file %s: %v", path, err)
					continue
				}

				expectedContent := "content" + string(rune('0'+i))
				if string(content) != expectedContent {
					t.Errorf("File %s content = %q, want %q", path, string(content), expectedContent)
				}
			}

			// Clean up files for next test
			os.RemoveAll(tempDir)
			os.MkdirAll(tempDir, 0755)
		})
	}
}

func TestDownloader_ConcurrencyWithRetries(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "downloader-concurrency-retry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mockResolver := &mockFailingBlobResolver{
		files: map[string][]byte{
			"file1": []byte("content1"),
			"file2": []byte("content2"),
			"file3": []byte("content3"),
			"file4": []byte("content4"),
		},
		failCount: map[string]int{
			"file1": 0, // succeed immediately
			"file2": 1, // fail once
			"file3": 2, // fail twice
			"file4": 3, // fail three times (will ultimately fail with maxRetries=2)
		},
		attemptCount: make(map[string]int),
	}

	downloader := NewDownloader(mockResolver)

	jobs := []*DownloadJob{
		{Path: "file1", BlobDigest: digest.FromString("test"), Size: 8, OutputPath: filepath.Join(tempDir, "file1")},
		{Path: "file2", BlobDigest: digest.FromString("test"), Size: 8, OutputPath: filepath.Join(tempDir, "file2")},
		{Path: "file3", BlobDigest: digest.FromString("test"), Size: 8, OutputPath: filepath.Join(tempDir, "file3")},
		{Path: "file4", BlobDigest: digest.FromString("test"), Size: 8, OutputPath: filepath.Join(tempDir, "file4")},
	}

	opts := &DownloadOptions{
		MaxRetries:  2,
		Concurrency: 2,
	}

	stats, err := downloader.StartDownload(context.Background(), jobs, nil, opts)
	if err != nil {
		t.Errorf("StartDownload() unexpected error: %v", err)
		return
	}

	// file1: success (0 retries)
	// file2: success after 1 retry (1 retry)
	// file3: success after 2 retries (2 retries)
	// file4: fail after 2 retries (2 retries)
	// Total: 3 success, 1 failed, 5 retries
	if stats.DownloadedFiles != 3 {
		t.Errorf("DownloadedFiles = %d, want 3", stats.DownloadedFiles)
	}

	if stats.FailedFiles != 1 {
		t.Errorf("FailedFiles = %d, want 1", stats.FailedFiles)
	}

	if stats.Retries != 5 {
		t.Errorf("Retries = %d, want 5", stats.Retries)
	}
}

func TestIntegrationSingleFileChunkedDownload(t *testing.T) {
	if testing.Short() || os.Getenv("STARGZ_INTEGRATION") == "" {
		t.Skip("set STARGZ_INTEGRATION=1 to run integration test")
	}

	const imageRef = "ghcr.io/stargz-containers/node:13.13.0-esgz"
	const targetPath = "usr/bin/bash"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := stor.NewRemoteRegistryStorage()
	manifest, err := client.GetManifest(ctx, imageRef)
	if err != nil {
		t.Fatalf("GetManifest(%q) error = %v", imageRef, err)
	}

	registry, repository := splitImageRef(t, imageRef)
	storage := client.NewStorage(registry, repository, manifest)
	resolver := NewBlobResolver(storage)
	loader := NewBlobIndexLoader(storage, resolver)

	index, err := loader.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	targetInfo, err := index.FindFile(targetPath, digest.Digest(""))
	if err != nil {
		t.Fatalf("FindFile(%q) error = %v", targetPath, err)
	}

	targetMeta, err := resolver.FileMetadata(ctx, targetInfo.BlobDigest, targetInfo.Path)
	if err != nil {
		t.Fatalf("FileMetadata(%q) error = %v", targetPath, err)
	}

	if len(targetMeta.Chunks) <= 1 {
		t.Skipf("file %s is not chunked in this image", targetPath)
	}

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "bash")

	job := &DownloadJob{
		Path:       targetInfo.Path,
		BlobDigest: targetInfo.BlobDigest,
		Size:       targetInfo.Size,
		OutputPath: outputPath,
	}

	opts := &DownloadOptions{
		Concurrency:              4,
		SingleFileChunkThreshold: 1,
	}

	downloader := NewDownloader(resolver)
	stats, err := downloader.StartDownload(ctx, []*DownloadJob{job}, nil, opts)
	if err != nil {
		t.Fatalf("StartDownload() error = %v", err)
	}

	if stats.DownloadedFiles != 1 {
		t.Fatalf("DownloadedFiles = %d, want 1", stats.DownloadedFiles)
	}
	if stats.DownloadedBytes != targetInfo.Size {
		t.Fatalf("DownloadedBytes = %d, want %d", stats.DownloadedBytes, targetInfo.Size)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", outputPath, err)
	}

	if info.Size() != targetInfo.Size {
		t.Fatalf("output size = %d, want %d", info.Size(), targetInfo.Size)
	}
}

func splitImageRef(t *testing.T, ref string) (string, string) {
	t.Helper()

	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("invalid image reference: %s", ref)
	}

	registry := parts[0]
	rest := parts[1]

	repoParts := strings.Split(rest, ":")
	if len(repoParts) < 2 {
		t.Fatalf("image reference missing tag: %s", ref)
	}

	repository := strings.Join(repoParts[:len(repoParts)-1], ":")

	return registry, repository
}
