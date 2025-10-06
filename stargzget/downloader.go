package stargzget

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
)

// ProgressCallback is called during download to report progress
// current: bytes downloaded so far
// total: total file size (may be -1 if unknown)
type ProgressCallback func(current int64, total int64)

// DownloadJob represents a single file to download
type DownloadJob struct {
	Path       string        // File path in the image
	BlobDigest digest.Digest // Which blob contains this file
	Size       int64         // File size
	OutputPath string        // Where to save the file locally
}

// DownloadStats contains statistics about a download operation
type DownloadStats struct {
	TotalFiles      int
	TotalBytes      int64
	DownloadedFiles int
	DownloadedBytes int64
}

type Downloader interface {
	// StartDownload downloads a list of files with progress tracking
	StartDownload(ctx context.Context, jobs []*DownloadJob, progress ProgressCallback) (*DownloadStats, error)
}

type downloader struct {
	imageAccessor ImageAccessor
}

func NewDownloader(imageAccessor ImageAccessor) Downloader {
	return &downloader{
		imageAccessor: imageAccessor,
	}
}

func (d *downloader) StartDownload(ctx context.Context, jobs []*DownloadJob, progress ProgressCallback) (*DownloadStats, error) {
	if len(jobs) == 0 {
		return &DownloadStats{}, nil
	}

	// Calculate total size
	var totalSize int64
	for _, job := range jobs {
		totalSize += job.Size
	}

	stats := &DownloadStats{
		TotalFiles: len(jobs),
		TotalBytes: totalSize,
	}

	// Notify the callback of total size before starting
	if progress != nil {
		progress(0, totalSize)
	}

	var currentTotal int64

	// Download each file
	for _, job := range jobs {
		// Create target directory if needed
		targetDir := filepath.Dir(job.OutputPath)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			// Skip files that fail to create directory
			continue
		}

		// Create target file
		outFile, err := os.Create(job.OutputPath)
		if err != nil {
			// Skip files that fail to create
			continue
		}

		// Open the file from the image
		fileReader, err := d.imageAccessor.OpenFile(ctx, job.Path, job.BlobDigest)
		if err != nil {
			outFile.Close()
			// Skip files that fail to open
			continue
		}

		// Wrap fileReader with progress tracking if callback is provided
		var readerToUse io.Reader = fileReader
		if progress != nil {
			// Update total progress bar
			readerToUse = &progressReader{
				reader: fileReader,
				total:  job.Size,
				callback: func(current, total int64) {
					progress(currentTotal+current, totalSize)
				},
			}
		}

		// Copy file content to target
		_, err = io.Copy(outFile, readerToUse)
		outFile.Close()

		if err != nil {
			// Continue with next file on error
			continue
		}

		currentTotal += job.Size
		stats.DownloadedFiles++
		stats.DownloadedBytes += job.Size
	}

	return stats, nil
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
