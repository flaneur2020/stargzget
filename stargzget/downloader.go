package stargzget

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
)

// ProgressCallback is called during download to report progress
// current: bytes downloaded so far
// total: total file size (may be -1 if unknown)
type ProgressCallback func(current int64, total int64)

type Downloader interface {
	DownloadFile(ctx context.Context, blobDigest digest.Digest, fileName string, targetPath string, progress ProgressCallback) error
}

type downloader struct {
	blobAccessor BlobAccessor
}

func NewDownloader(blobAccessor BlobAccessor) Downloader {
	return &downloader{
		blobAccessor: blobAccessor,
	}
}

func (d *downloader) DownloadFile(ctx context.Context, blobDigest digest.Digest, fileName string, targetPath string, progress ProgressCallback) error {
	// Get file metadata to verify it exists
	metadata, err := d.blobAccessor.GetFileMetadata(ctx, blobDigest, fileName)
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

	// Wrap fileReader with progress tracking if callback is provided
	var readerToUse io.Reader = fileReader
	if progress != nil {
		readerToUse = &progressReader{
			reader:   fileReader,
			total:    metadata.Size,
			callback: progress,
		}
	}

	// Copy file content to target
	_, err = io.Copy(outFile, readerToUse)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	return nil
}

// progressReader wraps an io.Reader to report download progress
type progressReader struct {
	reader   io.Reader
	total    int64
	current  int64
	callback ProgressCallback
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.current += int64(n)
	if pr.callback != nil {
		pr.callback(pr.current, pr.total)
	}
	return n, err
}
