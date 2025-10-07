package stargzget

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/flaneur2020/stargz-get/logger"
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
	FailedFiles     int   // Number of files that failed after all retries
	Retries         int   // Total number of retries performed
}

// DownloadOptions configures download behavior
type DownloadOptions struct {
	MaxRetries  int // Maximum number of retries per file (default: 3)
	Concurrency int // Number of concurrent workers (default: 4, set to 1 for sequential)
}

type Downloader interface {
	// StartDownload downloads a list of files with progress tracking and retry support
	// If opts is nil, uses default options (MaxRetries: 3)
	StartDownload(ctx context.Context, jobs []*DownloadJob, progress ProgressCallback, opts *DownloadOptions) (*DownloadStats, error)
}

type downloader struct {
	imageAccessor ImageAccessor
}

func NewDownloader(imageAccessor ImageAccessor) Downloader {
	return &downloader{
		imageAccessor: imageAccessor,
	}
}

func (d *downloader) StartDownload(ctx context.Context, jobs []*DownloadJob, progress ProgressCallback, opts *DownloadOptions) (*DownloadStats, error) {
	if len(jobs) == 0 {
		return &DownloadStats{}, nil
	}

	// Use default options if not provided
	if opts == nil {
		opts = &DownloadOptions{
			MaxRetries:  3,
			Concurrency: 4,
		}
	}

	// Set default concurrency if not specified
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}

	// Set default max retries if not specified
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 3
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

	// Create a channel for distributing jobs to workers
	type jobWithOffset struct {
		job        *DownloadJob
		baseOffset int64
	}
	jobChan := make(chan *jobWithOffset, len(jobs))

	// Mutex for protecting shared state
	var mu sync.Mutex

	// WaitGroup to wait for all workers to complete
	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Process jobs from the channel
			for jwo := range jobChan {
				downloaded := false
				var lastErr error

				logger.Debug("Starting download: %s (%d bytes)", jwo.job.Path, jwo.job.Size)

				// Try downloading with retries
				for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
					if attempt > 0 {
						logger.Warn("Retrying download (attempt %d/%d): %s - %v", attempt, opts.MaxRetries, jwo.job.Path, lastErr)
						mu.Lock()
						stats.Retries++
						mu.Unlock()
					}

					err := d.downloadSingleFile(ctx, jwo.job, jwo.baseOffset, totalSize, progress, &mu)
					if err == nil {
						downloaded = true
						mu.Lock()
						stats.DownloadedFiles++
						stats.DownloadedBytes += jwo.job.Size
						mu.Unlock()
						logger.Info("Successfully downloaded: %s (%d bytes)", jwo.job.Path, jwo.job.Size)
						break
					}

					lastErr = err
					// If this wasn't the last attempt, we'll retry
				}

				if !downloaded {
					mu.Lock()
					stats.FailedFiles++
					mu.Unlock()
					logger.Error("Failed to download after %d attempts: %s - %v", opts.MaxRetries+1, jwo.job.Path, lastErr)
				}
			}
		}()
	}

	// Send all jobs to the channel with pre-calculated offsets
	var currentOffset int64
	for _, job := range jobs {
		jobChan <- &jobWithOffset{
			job:        job,
			baseOffset: currentOffset,
		}
		currentOffset += job.Size
	}
	close(jobChan)

	// Wait for all workers to complete
	wg.Wait()

	return stats, nil
}

// downloadSingleFile downloads a single file
func (d *downloader) downloadSingleFile(ctx context.Context, job *DownloadJob, baseOffset int64, totalSize int64, progress ProgressCallback, mu *sync.Mutex) error {
	// Create target directory if needed
	targetDir := filepath.Dir(job.OutputPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
	}

	// Create target file
	outFile, err := os.Create(job.OutputPath)
	if err != nil {
		return ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
	}
	defer outFile.Close()

	// Open the file from the image
	fileReader, err := d.imageAccessor.OpenFile(ctx, job.Path, job.BlobDigest)
	if err != nil {
		return ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
	}

	// Wrap fileReader with progress tracking if callback is provided
	var readerToUse io.Reader = fileReader
	if progress != nil {
		// Update total progress bar with mutex protection
		readerToUse = &progressReader{
			reader: fileReader,
			total:  job.Size,
			callback: func(current, total int64) {
				mu.Lock()
				progress(baseOffset+current, totalSize)
				mu.Unlock()
			},
		}
	}

	// Copy file content to target
	_, err = io.Copy(outFile, readerToUse)
	if err != nil {
		return ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
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
