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
	imageAccessor ImageAccessor
}

func NewDownloader(imageAccessor ImageAccessor) Downloader {
	return &downloader{
		imageAccessor: imageAccessor,
	}
}

func (d *downloader) DownloadFile(ctx context.Context, blobDigest digest.Digest, fileName string, targetPath string, progress ProgressCallback) error {
	// Get image index
	index, err := d.imageAccessor.ImageIndex(ctx)
	if err != nil {
		return fmt.Errorf("failed to get image index: %w", err)
	}

	// Find file to verify it exists and get its size
	fileInfo, err := index.FindFile(fileName, blobDigest)
	if err != nil {
		return fmt.Errorf("failed to find file: %w", err)
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

	// Open the file from the image
	fileReader, err := d.imageAccessor.OpenFile(ctx, fileName, fileInfo.BlobDigest)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	// Wrap fileReader with progress tracking if callback is provided
	var readerToUse io.Reader = fileReader
	if progress != nil {
		readerToUse = &progressReader{
			reader:   fileReader,
			total:    fileInfo.Size,
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
	// Get image index
	index, err := d.imageAccessor.ImageIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image index: %w", err)
	}

	// Find the layer with the specified blob digest
	var layerInfo *LayerInfo
	for _, layer := range index.Layers {
		if layer.BlobDigest == blobDigest {
			layerInfo = layer
			break
		}
	}

	if layerInfo == nil {
		return nil, fmt.Errorf("blob not found: %s", blobDigest)
	}

	allFiles := layerInfo.Files
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
	type fileWithSize struct {
		path string
		size int64
	}

	var fileInfos []fileWithSize
	var totalSize int64

	for _, file := range files {
		// Get file size from layer info
		size, ok := layerInfo.FileSizes[file]
		if !ok {
			// Skip files that don't have size info
			continue
		}
		fileInfos = append(fileInfos, fileWithSize{path: file, size: size})
		totalSize += size
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

		currentTotal += info.size
		stats.DownloadedFiles++
		stats.DownloadedBytes += info.size
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
