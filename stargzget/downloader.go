package stargzget

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	stargzerrors "github.com/flaneur2020/stargz-get/stargzget/errors"
	"github.com/flaneur2020/stargz-get/stargzget/logger"
	"github.com/flaneur2020/stargz-get/stargzget/storage"
	"github.com/opencontainers/go-digest"
)

// ProgressCallback is called during download to report progress
// current: bytes downloaded so far
// total: total file size (may be -1 if unknown)
type ProgressCallback func(current int64, total int64)

// StatusCallback is called when download status changes
// activeFiles: list of files currently being downloaded
// completedFiles: number of files completed so far
// totalFiles: total number of files to download
type StatusCallback func(activeFiles []string, completedFiles int, totalFiles int)

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
	FailedFiles     int // Number of files that failed after all retries
	Retries         int // Total number of retries performed
}

// DownloadOptions configures download behavior
type DownloadOptions struct {
	MaxRetries               int            // Maximum number of retries per file (default: 3)
	Concurrency              int            // Number of concurrent workers (default: 4, set to 1 for sequential)
	OnStatus                 StatusCallback // Optional callback for status updates (file started/completed)
	SingleFileChunkThreshold int64          // Files >= this size (bytes) may use chunked download (default: 10MB)
}

// jobWithOffset associates a download job with its base offset in the
// aggregate progress space so we can report total progress across files.
type jobWithOffset struct {
	job        *DownloadJob
	baseOffset int64
}

type Downloader interface {
	// StartDownload downloads a list of files with progress tracking and retry support
	// If opts is nil, uses default options (MaxRetries: 3)
	StartDownload(ctx context.Context, jobs []*DownloadJob, progress ProgressCallback, opts *DownloadOptions) (*DownloadStats, error)
}

type downloader struct {
	resolver BlobResolver
	storage  storage.Storage
}

const defaultSingleFileChunkThreshold int64 = 10 * 1024 * 1024 // 10MB

func NewDownloader(resolver BlobResolver, storage storage.Storage) Downloader {
	return &downloader{
		resolver: resolver,
		storage:  storage,
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

	if opts.SingleFileChunkThreshold <= 0 {
		opts.SingleFileChunkThreshold = defaultSingleFileChunkThreshold
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
	jobChan := make(chan *jobWithOffset, len(jobs))

	// Mutex for protecting shared state
	var mu sync.Mutex

	// Track active downloads for status updates
	activeFiles := make([]string, 0, opts.Concurrency)

	// WaitGroup to wait for all workers to complete
	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for jwo := range jobChan {
				d.processDownloadJob(ctx, jwo, stats, totalSize, progress, opts, &mu, &activeFiles)
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

// processDownloadJob processes jobs from jobChan, handling retries, stats, and status updates.
func (d *downloader) processDownloadJob(
	ctx context.Context,
	jwo *jobWithOffset,
	stats *DownloadStats,
	totalSize int64,
	progress ProgressCallback,
	opts *DownloadOptions,
	mu *sync.Mutex,
	activeFiles *[]string,
) {
	downloaded := false
	var lastErr error

	// Add to active files and notify status
	mu.Lock()
	*activeFiles = append(*activeFiles, jwo.job.Path)
	if opts.OnStatus != nil {
		opts.OnStatus(append([]string{}, *activeFiles...), stats.DownloadedFiles, stats.TotalFiles)
	}
	mu.Unlock()

	logger.Debug("Starting download: %s (%d bytes)", jwo.job.Path, jwo.job.Size)

	// Try downloading with retries
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			logger.Warn("Retrying download (attempt %d/%d): %s - %v", attempt, opts.MaxRetries, jwo.job.Path, lastErr)
			mu.Lock()
			stats.Retries++
			mu.Unlock()
		}

		err := d.downloadSingleFile(ctx, jwo.job, jwo.baseOffset, totalSize, progress, mu, opts)
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

	// Remove from active files and notify status
	mu.Lock()
	for i, f := range *activeFiles {
		if f == jwo.job.Path {
			*activeFiles = append((*activeFiles)[:i], (*activeFiles)[i+1:]...)
			break
		}
	}
	if opts.OnStatus != nil {
		opts.OnStatus(append([]string{}, *activeFiles...), stats.DownloadedFiles, stats.TotalFiles)
	}
	mu.Unlock()

	if !downloaded {
		mu.Lock()
		stats.FailedFiles++
		mu.Unlock()
		logger.Error("Failed to download after %d attempts: %s - %v", opts.MaxRetries+1, jwo.job.Path, lastErr)
	}
}

// downloadSingleFile downloads a single file
func (d *downloader) downloadSingleFile(ctx context.Context, job *DownloadJob, baseOffset int64, totalSize int64, progress ProgressCallback, mu *sync.Mutex, opts *DownloadOptions) error {
	// Create target directory if needed
	targetDir := filepath.Dir(job.OutputPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
	}

	// Create target file
	outFile, err := os.Create(job.OutputPath)
	if err != nil {
		return stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
	}
	defer outFile.Close()

	metadata, err := d.resolver.FileMetadata(ctx, job.BlobDigest, job.Path)
	if err != nil {
		return stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
	}

	if metadata == nil {
		return stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithMessage("missing file metadata")
	}

	if len(metadata.Chunks) == 0 {
		if progress != nil && job.Size == 0 {
			mu.Lock()
			progress(baseOffset, totalSize)
			mu.Unlock()
		}
		return nil
	}

	useChunked := len(metadata.Chunks) > 1 &&
		metadata.Size >= opts.SingleFileChunkThreshold &&
		job.Size >= opts.SingleFileChunkThreshold

	chunkWorkers := 1
	if useChunked {
		chunkWorkers = opts.Concurrency
		if chunkWorkers <= 0 {
			chunkWorkers = 1
		}
		if chunkWorkers > len(metadata.Chunks) {
			chunkWorkers = len(metadata.Chunks)
		}
		if chunkWorkers < 1 {
			chunkWorkers = 1
		}
	}

	return d.downloadFileChunks(ctx, job, metadata, outFile, baseOffset, totalSize, progress, mu, chunkWorkers)
}

func (d *downloader) downloadFileChunks(
	ctx context.Context,
	job *DownloadJob,
	metadata *FileMetadata,
	outFile *os.File,
	baseOffset int64,
	totalSize int64,
	progress ProgressCallback,
	mu *sync.Mutex,
	workerCount int,
) error {
	ctxChunk, cancel := context.WithCancel(ctx)
	defer cancel()

	chunkJobs := make(chan Chunk)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var completed int64
	if workerCount < 1 {
		workerCount = 1
	}

	sendErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunk := range chunkJobs {
				if chunk.Size <= 0 {
					continue
				}

				if ctxChunk.Err() != nil {
					return
				}

				data, err := d.readChunk(ctxChunk, job.BlobDigest, job.Path, chunk)
				if err != nil {
					sendErr(stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err))
					cancel()
					return
				}

				if int64(len(data)) != chunk.Size {
					sendErr(stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithCause(io.ErrUnexpectedEOF))
					cancel()
					return
				}

				if _, err := outFile.WriteAt(data, chunk.Offset); err != nil {
					sendErr(stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err))
					cancel()
					return
				}

				if progress != nil {
					newProgress := atomic.AddInt64(&completed, int64(len(data)))
					mu.Lock()
					progress(baseOffset+newProgress, totalSize)
					mu.Unlock()
				}
			}
		}()
	}

chunkLoop:
	for _, chunk := range metadata.Chunks {
		if chunk.Size <= 0 {
			continue
		}
		select {
		case <-ctxChunk.Done():
			break chunkLoop
		case chunkJobs <- chunk:
		}
	}
	close(chunkJobs)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
	}

	if metadata.Size >= 0 {
		if err := outFile.Truncate(metadata.Size); err != nil {
			return stargzerrors.ErrDownloadFailed.WithDetail("path", job.Path).WithCause(err)
		}
	}

	return nil
}

func (d *downloader) readChunk(ctx context.Context, blobDigest digest.Digest, path string, chunk Chunk) ([]byte, error) {
	reader, err := d.storage.ReadBlob(ctx, blobDigest, chunk.CompressedOffset, 0)
	if err != nil {
		return nil, stargzerrors.ErrDownloadFailed.WithDetail("path", path).WithCause(err)
	}
	defer reader.Close()

	gz, err := gzip.NewReader(reader)
	if err != nil {
		return nil, stargzerrors.ErrDownloadFailed.WithDetail("path", path).WithCause(err)
	}
	defer gz.Close()

	if chunk.InnerOffset > 0 {
		if _, err := io.CopyN(io.Discard, gz, chunk.InnerOffset); err != nil {
			return nil, stargzerrors.ErrDownloadFailed.WithDetail("path", path).WithCause(err)
		}
	}

	buf := make([]byte, chunk.Size)
	n, err := io.ReadFull(gz, buf)
	if err != nil && err != io.EOF {
		return nil, stargzerrors.ErrDownloadFailed.WithDetail("path", path).WithCause(err)
	}
	if int64(n) != chunk.Size {
		return nil, stargzerrors.ErrDownloadFailed.WithDetail("path", path).WithCause(io.ErrUnexpectedEOF)
	}

	return buf, nil
}
