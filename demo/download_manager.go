package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/stargz-snapshotter/estargz"
	digest "github.com/opencontainers/go-digest"
)

// downloadManager coordinates stargz file downloads from container registries
type downloadManager struct {
	registryClient *registryClient
	outputDir      string
}

// newDownloadManager creates a new download manager
func newDownloadManager(registryClient *registryClient, outputDir string) *downloadManager {
	return &downloadManager{
		registryClient: registryClient,
		outputDir:      outputDir,
	}
}

// downloadFile downloads a single file from a stargz layer
// Current implementation: Download entire blob first (will optimize later with Range for TOC)
func (dm *downloadManager) downloadFile(ctx context.Context, blobDigest digest.Digest, fileName, targetPath string) error {
	// Step 1: Get blob size
	fmt.Printf("  Getting blob size...\n")
	blobSize, err := dm.registryClient.getBlobSize(ctx, blobDigest)
	if err != nil {
		return fmt.Errorf("failed to get blob size: %w", err)
	}
	fmt.Printf("  Blob size: %d bytes (%.2f MB)\n", blobSize, float64(blobSize)/(1024*1024))

	//  Step 2: Download entire blob (temporary - will optimize to only download TOC + chunks later)
	fmt.Printf("  Downloading blob...\n")
	blobReader, _, err := dm.registryClient.fetchEntireBlob(ctx, blobDigest)
	if err != nil {
		return fmt.Errorf("failed to fetch blob: %w", err)
	}
	defer blobReader.Close()

	// Write to temp file
	tempFile, err := os.CreateTemp("", "stargz-*.blob")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	defer tempFile.Close()

	written, err := io.Copy(tempFile, blobReader)
	if err != nil {
		return fmt.Errorf("failed to write blob: %w", err)
	}
	fmt.Printf("  Downloaded %d bytes\n", written)

	// Step 3: Open as stargz and parse TOC
	fmt.Printf("  Parsing TOC...\n")
	stargzReader, err := newStargzReader(io.NewSectionReader(tempFile, 0, written))
	if err != nil {
		return fmt.Errorf("failed to create stargz reader: %w", err)
	}
	defer stargzReader.close()
	fmt.Printf("  TOC parsed successfully\n")

	// Step 4: Find the file
	fmt.Printf("  Looking up file: %s\n", fileName)
	chunks, err := stargzReader.getFileChunks(fileName)
	if err != nil {
		return fmt.Errorf("failed to get file chunks: %w", err)
	}

	if len(chunks) == 0 {
		fmt.Printf("  File is empty\n")
		return dm.writeEmptyFile(targetPath)
	}

	fmt.Printf("  File has %d chunks\n", len(chunks))

	// Step 5: Extract file data from the blob (it's already downloaded)
	var fileData bytes.Buffer
	for i, chunk := range chunks {
		fmt.Printf("  [%d/%d] Extracting chunk from blob (offset: %d, size: %d bytes)...\n",
			i+1, len(chunks), chunk.offset, chunk.size)

		// Read chunk from temp file
		chunkData := make([]byte, chunk.size)
		if _, err := tempFile.ReadAt(chunkData, chunk.offset); err != nil {
			return fmt.Errorf("failed to read chunk %d: %w", i, err)
		}

		// Decompress
		decompressor := &estargz.GzipDecompressor{}
		decompressedReader, err := decompressor.Reader(bytes.NewReader(chunkData))
		if err != nil {
			return fmt.Errorf("failed to decompress chunk %d: %w", i, err)
		}

		if _, err := io.Copy(&fileData, decompressedReader); err != nil {
			decompressedReader.Close()
			return fmt.Errorf("failed to read chunk %d: %w", i, err)
		}
		decompressedReader.Close()

		fmt.Printf("  [%d/%d] Chunk extracted and decompressed\n", i+1, len(chunks))
	}

	// Step 6: Write to target path
	fmt.Printf("  Writing file to: %s (%d bytes)\n", targetPath, fileData.Len())
	return dm.writeFile(targetPath, fileData.Bytes())
}

// writeFile writes data to a file, creating directories as needed
func (dm *downloadManager) writeFile(targetPath string, data []byte) error {
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(dm.outputDir, targetPath)
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// writeEmptyFile creates an empty file
func (dm *downloadManager) writeEmptyFile(targetPath string) error {
	return dm.writeFile(targetPath, []byte{})
}
