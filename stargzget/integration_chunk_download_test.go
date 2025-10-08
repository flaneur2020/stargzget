//go:build integration

package stargzget_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := stargzget.NewRegistryClient()

	manifest, err := client.GetManifest(ctx, imageRef)
	if err != nil {
		t.Fatalf("GetManifest(%q) error = %v", imageRef, err)
	}

	registry, repository := splitImageRef(t, imageRef)

	accessor := stargzget.NewImageAccessor(client, registry, repository, manifest)

	index, err := accessor.ImageIndex(ctx)
	if err != nil {
		t.Fatalf("ImageIndex() error = %v", err)
	}

	const targetPath = "usr/bin/bash"

	targetInfo, err := index.FindFile(targetPath, digest.Digest(""))
	if err != nil {
		t.Fatalf("FindFile(%q) error = %v", targetPath, err)
	}

	targetMeta, err := accessor.GetFileMetadata(ctx, targetInfo.BlobDigest, targetInfo.Path)
	if err != nil {
		t.Fatalf("GetFileMetadata(%q) error = %v", targetPath, err)
	}

	if len(targetMeta.Chunks) <= 1 {
		t.Skipf("file %s is not chunked in this image", targetPath)
	}

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "bash")

	job := &stargzget.DownloadJob{
		Path:       targetInfo.Path,
		BlobDigest: targetInfo.BlobDigest,
		Size:       targetInfo.Size,
		OutputPath: outputPath,
	}

	opts := &stargzget.DownloadOptions{
		Concurrency:              4,
		SingleFileChunkThreshold: 1,
	}

	downloader := stargzget.NewDownloader(accessor)
	stats, err := downloader.StartDownload(ctx, []*stargzget.DownloadJob{job}, nil, opts)
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
