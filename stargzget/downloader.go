package stargzget

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

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
	jobChan := make(chan *DownloadJob, len(jobs))

	// Mutex for protecting shared state
	var mu sync.Mutex
	var currentTotal int64

	// WaitGroup to wait for all workers to complete
	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Process jobs from the channel
			for job := range jobChan {
				downloaded := false
				var lastErr error

				// Try downloading with retries
				for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
					if attempt > 0 {
						mu.Lock()
						stats.Retries++
						mu.Unlock()
					}

					err := d.downloadSingleFile(ctx, job, &currentTotal, totalSize, progress, &mu)
					if err == nil {
						downloaded = true
						mu.Lock()
						currentTotal += job.Size
						stats.DownloadedFiles++
						stats.DownloadedBytes += job.Size
						mu.Unlock()
						break
					}

					lastErr = err
					// If this wasn't the last attempt, we'll retry
				}

				if !downloaded {
					mu.Lock()
					stats.FailedFiles++
					mu.Unlock()
					// Optionally log the error (for now we continue with next file)
					_ = lastErr
				}
			}
		}()
	}

	// Send all jobs to the channel
	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)

	// Wait for all workers to complete
	wg.Wait()

	return stats, nil
}

// downloadSingleFile downloads a single file
func (d *downloader) downloadSingleFile(ctx context.Context, job *DownloadJob, currentTotal *int64, totalSize int64, progress ProgressCallback, mu *sync.Mutex) error {
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
				progress(*currentTotal+current, totalSize)
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
