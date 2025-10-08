//go:build integration

package stargzget_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flaneur2020/stargz-get/stargzget"
	"github.com/opencontainers/go-digest"
)

// TestIntegrationSingleFileChunkedDownload exercises the real registry path using the
// sample image provided in the contributor instructions. It intentionally requires the
// integration build tag because it performs network I/O and may take a few seconds.
func TestIntegrationSingleFileChunkedDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	imageRef := "ghcr.io/stargz-containers/node:13.13.0-esgz"
	targetPath := "usr/bin/bash"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := stargzget.NewRegistryClient()

	manifest, err := client.GetManifest(ctx, imageRef)
	if err != nil {
		t.Fatalf("GetManifest(%q) error = %v", imageRef, err)
	}

	registry, repository, err := stargzget.ParseImageReference(imageRef)
	if err != nil {
		t.Fatalf("ParseImageReference(%q) error = %v", imageRef, err)
	}

	accessor := stargzget.NewImageAccessor(client, registry, repository, manifest)

	index, err := accessor.ImageIndex(ctx)
	if err != nil {
		t.Fatalf("ImageIndex() error = %v", err)
	}

	fileInfo, err := index.FindFile(targetPath, digest.Digest(""))
	if err != nil {
		t.Fatalf("FindFile(%q) error = %v", targetPath, err)
	}

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "bash")

	job := &stargzget.DownloadJob{
		Path:       targetPath,
		BlobDigest: fileInfo.BlobDigest,
		Size:       fileInfo.Size,
		OutputPath: outputPath,
	}

	opts := &stargzget.DownloadOptions{
		Concurrency:              4,
		SingleFileChunkThreshold: 1, // force the chunked path regardless of size
	}

	downloader := stargzget.NewDownloader(accessor)
	stats, err := downloader.StartDownload(ctx, []*stargzget.DownloadJob{job}, nil, opts)
	if err != nil {
		t.Fatalf("StartDownload() error = %v", err)
	}

	if stats.DownloadedFiles != 1 {
		t.Fatalf("DownloadedFiles = %d, want 1", stats.DownloadedFiles)
	}
	if stats.DownloadedBytes != fileInfo.Size {
		t.Fatalf("DownloadedBytes = %d, want %d", stats.DownloadedBytes, fileInfo.Size)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", outputPath, err)
	}

	if info.Size() != fileInfo.Size {
		t.Fatalf("output size = %d, want %d", info.Size(), fileInfo.Size)
	}
}
