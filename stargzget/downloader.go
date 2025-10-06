package stargzget

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
)

type Downloader interface {
	DownloadFile(ctx context.Context, blobDigest digest.Digest, fileName string, targetPath string) error
}

type downloader struct {
	blobAccessor BlobAccessor
}

func NewDownloader(blobAccessor BlobAccessor) Downloader {
	return &downloader{
		blobAccessor: blobAccessor,
	}
}

func (d *downloader) DownloadFile(ctx context.Context, blobDigest digest.Digest, fileName string, targetPath string) error {
	// Get file metadata to verify it exists
	_, err := d.blobAccessor.GetFileMetadata(ctx, blobDigest, fileName)
	if err != nil {
		return fmt.Errorf("failed to get file metadata: %w", err)
	}

	// Create target directory if needed
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create target file
	outFile, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer outFile.Close()

	// Open the stargz reader
	reader, err := d.blobAccessor.OpenReader(ctx, blobDigest)
	if err != nil {
		return fmt.Errorf("failed to open stargz reader: %w", err)
	}

	// Open the file from the stargz archive
	fileReader, err := reader.OpenFile(fileName)
	if err != nil {
		return fmt.Errorf("failed to open file in stargz: %w", err)
	}

	// Copy file content to target
	bytesWritten, err := io.Copy(outFile, fileReader)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	fmt.Printf("Successfully downloaded %s (%d bytes)\n", fileName, bytesWritten)

	return nil
}
