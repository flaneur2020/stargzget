package stargzget

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
)

// ProgressCallback is called during download to report progress
// current: bytes downloaded so far
// total: total file size (may be -1 if unknown)
type ProgressCallback func(current int64, total int64)

// DownloadStats contains statistics about a download operation
type DownloadStats struct {
	TotalFiles      int
	TotalBytes      int64
	DownloadedFiles int
	DownloadedBytes int64
}

type Downloader interface {
	DownloadFile(ctx context.Context, blobDigest digest.Digest, fileName string, targetPath string, progress ProgressCallback) error
	// DownloadDir downloads files from a directory in the blob recursively
	// dirPath: directory to download (use "." or "/" for all files)
	DownloadDir(ctx context.Context, blobDigest digest.Digest, dirPath string, outputDir string, progress ProgressCallback) (*DownloadStats, error)
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

	// Open the file from the blob
	fileReader, err := d.blobAccessor.OpenFile(ctx, blobDigest, fileName)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
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

func (d *downloader) DownloadDir(ctx context.Context, blobDigest digest.Digest, dirPath string, outputDir string, progress ProgressCallback) (*DownloadStats, error) {
	// List all files
	allFiles, err := d.blobAccessor.ListFiles(ctx, blobDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	if len(allFiles) == 0 {
		return &DownloadStats{}, nil
	}

	// Normalize dirPath
	if dirPath == "" || dirPath == "." || dirPath == "/" {
		dirPath = "" // Download all files
	} else {
		// Ensure dirPath starts with / and doesn't end with /
		if !strings.HasPrefix(dirPath, "/") {
			dirPath = "/" + dirPath
		}
		dirPath = filepath.Clean(dirPath)
	}

	// Filter files based on dirPath
	var files []string
	if dirPath == "" {
		// Download all files
		files = allFiles
	} else {
		// Only download files under dirPath
		for _, file := range allFiles {
			// Ensure file path starts with /
			filePath := file
			if !strings.HasPrefix(filePath, "/") {
				filePath = "/" + filePath
			}

			// Check if file is under dirPath
			if strings.HasPrefix(filePath, dirPath+"/") || filePath == dirPath {
				files = append(files, file)
			}
		}
	}

	if len(files) == 0 {
		return &DownloadStats{}, nil
	}

	// Get metadata for all files and calculate total size
	type fileInfo struct {
		path     string
		metadata *FileMetadata
	}

	var fileInfos []fileInfo
	var totalSize int64

	for _, file := range files {
		metadata, err := d.blobAccessor.GetFileMetadata(ctx, blobDigest, file)
		if err != nil {
			// Skip files that fail metadata retrieval
			continue
		}
		fileInfos = append(fileInfos, fileInfo{path: file, metadata: metadata})
		totalSize += metadata.Size
	}

	stats := &DownloadStats{
		TotalFiles: len(fileInfos),
		TotalBytes: totalSize,
	}

	// Notify the callback of total size before starting
	if progress != nil {
		progress(0, totalSize)
	}

	var currentTotal int64

	// Download each file
	for _, info := range fileInfos {
		// Construct output path maintaining directory structure
		outputPath := filepath.Clean(info.path)
		// Remove leading slash if present
		outputPath = filepath.Join(outputDir, outputPath)

		var progressCallback ProgressCallback
		if progress != nil {
			// Update total progress bar
			progressCallback = func(current, total int64) {
				progress(currentTotal+current, totalSize)
			}
		}

		err = d.DownloadFile(ctx, blobDigest, info.path, outputPath, progressCallback)
		if err != nil {
			// Continue with next file on error
			continue
		}

		currentTotal += info.metadata.Size
		stats.DownloadedFiles++
		stats.DownloadedBytes += info.metadata.Size
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
