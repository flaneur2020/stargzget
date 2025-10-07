package stargzget

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/containerd/stargz-snapshotter/estargz"
	"github.com/opencontainers/go-digest"
)

// FileInfo contains information about a file in the image
type FileInfo struct {
	Path       string
	BlobDigest digest.Digest
	Size       int64
}

// LayerInfo contains information about a layer
type LayerInfo struct {
	BlobDigest digest.Digest
	Files      []string
	FileSizes  map[string]int64
}

// ImageIndex is an index of all files across all layers
type ImageIndex struct {
	Layers []*LayerInfo
	// File index: path -> FileInfo
	files map[string]*FileInfo
}

// AllFiles returns all file paths in the index (from all layers, later layers override earlier ones)
func (idx *ImageIndex) AllFiles() []string {
	paths := make([]string, 0, len(idx.files))
	for path := range idx.files {
		paths = append(paths, path)
	}
	return paths
}

// FindFile finds a file in the image index
// If blobDigest is empty, it searches all layers for the file
// If blobDigest is provided, it only searches within that specific blob
func (idx *ImageIndex) FindFile(path string, blobDigest digest.Digest) (*FileInfo, error) {
	if blobDigest.String() == "" {
		// Search in all layers
		info, ok := idx.files[path]
		if !ok {
			return nil, ErrFileNotFound.WithDetail("path", path)
		}
		return info, nil
	}

	// Search in specific blob
	for _, layer := range idx.Layers {
		if layer.BlobDigest == blobDigest {
			if size, ok := layer.FileSizes[path]; ok {
				return &FileInfo{
					Path:       path,
					BlobDigest: blobDigest,
					Size:       size,
				}, nil
			}
			return nil, ErrFileNotFound.WithDetail("path", path).WithDetail("blobDigest", blobDigest.String())
		}
	}
	return nil, ErrBlobNotFound.WithDetail("blobDigest", blobDigest.String())
}

// FilterFiles filters files based on path pattern and optional blob digest
// pathPattern can be:
// - A specific file path (e.g., "bin/echo")
// - A directory path (e.g., "bin/" or "bin") - returns all files under that directory
// - "." or "/" or "" - returns all files
// If blobDigest is provided (not empty), only returns files from that blob
func (idx *ImageIndex) FilterFiles(pathPattern string, blobDigest digest.Digest) []*FileInfo {
	var results []*FileInfo

	// Normalize path pattern
	if pathPattern == "." || pathPattern == "/" || pathPattern == "" {
		pathPattern = "" // Match all files
	}

	// Determine which layers to search
	var layersToSearch []*LayerInfo
	if blobDigest.String() == "" {
		// Search all layers
		layersToSearch = idx.Layers
	} else {
		// Search only the specified blob
		for _, layer := range idx.Layers {
			if layer.BlobDigest == blobDigest {
				layersToSearch = []*LayerInfo{layer}
				break
			}
		}
	}

	// Collect files from selected layers
	for _, layer := range layersToSearch {
		for _, filePath := range layer.Files {
			matched := false

			if pathPattern == "" {
				// Match all files
				matched = true
			} else {
				// Normalize file path for comparison
				normalizedFile := filePath
				if !strings.HasPrefix(normalizedFile, "/") {
					normalizedFile = "/" + normalizedFile
				}

				normalizedPattern := pathPattern
				if !strings.HasPrefix(normalizedPattern, "/") {
					normalizedPattern = "/" + normalizedPattern
				}

				// Check if it's a directory pattern (ends with /)
				if strings.HasSuffix(pathPattern, "/") {
					// Directory match - check if file is under this directory
					matched = strings.HasPrefix(normalizedFile, normalizedPattern)
				} else {
					// Could be either a file or directory
					// Match exact file or files under directory
					matched = normalizedFile == normalizedPattern ||
						strings.HasPrefix(normalizedFile, normalizedPattern+"/")
				}
			}

			if matched {
				size := layer.FileSizes[filePath]
				results = append(results, &FileInfo{
					Path:       filePath,
					BlobDigest: layer.BlobDigest,
					Size:       size,
				})
			}
		}
	}

	return results
}

type ImageAccessor interface {
	// ImageIndex returns the index of all files in the image
	ImageIndex(ctx context.Context) (*ImageIndex, error)

	// OpenFile opens a file from the image
	// blobDigest is required and specifies which blob to open the file from
	OpenFile(ctx context.Context, path string, blobDigest digest.Digest) (*io.SectionReader, error)

	// WithCredential returns a new ImageAccessor with the provided credentials
	WithCredential(username, password string) ImageAccessor
}

type FileMetadata struct {
	Size   int64
	Chunks []Chunk
}

type Chunk struct {
	Offset         int64
	Size           int64
	CompressedSize int64 // Size in the blob (compressed)
}

type imageAccessor struct {
	httpClient     *http.Client
	registryClient RegistryClient
	registry       string
	repository     string
	manifest       *Manifest
	// Cache: digest -> JTOC
	tocCache map[string]*estargz.JTOC
	// Auth token cache
	authToken string
	// Cached index
	index *ImageIndex
	// Basic auth credentials
	username string
	password string
}

func NewImageAccessor(registryClient RegistryClient, registry, repository string, manifest *Manifest) ImageAccessor {
	return &imageAccessor{
		httpClient:     &http.Client{},
		registryClient: registryClient,
		registry:       registry,
		repository:     repository,
		manifest:       manifest,
		tocCache:       make(map[string]*estargz.JTOC),
	}
}

// WithCredential returns a new ImageAccessor with the provided credentials
func (i *imageAccessor) WithCredential(username, password string) ImageAccessor {
	return &imageAccessor{
		httpClient:     i.httpClient,
		registryClient: i.registryClient,
		registry:       i.registry,
		repository:     i.repository,
		manifest:       i.manifest,
		tocCache:       i.tocCache,
		authToken:      i.authToken,
		index:          i.index,
		username:       username,
		password:       password,
	}
}

// getAuthToken gets auth token for blob access (similar to registry client)
func (i *imageAccessor) getAuthToken(ctx context.Context, wwwAuthenticate string) (string, error) {
	// Reuse the same logic from RegistryClient
	if i.authToken != "" {
		return i.authToken, nil
	}

	if !bytes.Contains([]byte(wwwAuthenticate), []byte("Bearer ")) {
		return "", ErrAuthFailed.WithCause(fmt.Errorf("unsupported auth scheme: %s", wwwAuthenticate))
	}

	params := make(map[string]string)
	authStr := wwwAuthenticate[len("Bearer "):]
	parts := bytes.Split([]byte(authStr), []byte(","))

	for _, part := range parts {
		kv := bytes.SplitN(bytes.TrimSpace(part), []byte("="), 2)
		if len(kv) == 2 {
			key := string(kv[0])
			value := string(bytes.Trim(kv[1], "\""))
			params[key] = value
		}
	}

	realm := params["realm"]
	service := params["service"]
	scope := params["scope"]

	if realm == "" {
		return "", ErrAuthFailed.WithCause(fmt.Errorf("no realm in WWW-Authenticate header"))
	}

	// Build token URL
	tokenURL := fmt.Sprintf("%s?service=%s&scope=%s", realm, service, scope)

	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", ErrAuthFailed.WithCause(err)
	}

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return "", ErrAuthFailed.WithCause(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", ErrAuthFailed.WithCause(fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body)))
	}

	var authResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", ErrAuthFailed.WithCause(err)
	}

	token := authResp.Token
	if token == "" {
		token = authResp.AccessToken
	}

	i.authToken = token
	return token, nil
}

// downloadTOC downloads the stargz TOC using estargz library
func (i *imageAccessor) downloadTOC(ctx context.Context, blobDigest string) (*estargz.JTOC, error) {
	// Check cache
	if toc, ok := i.tocCache[blobDigest]; ok {
		return toc, nil
	}

	// Construct blob URL
	blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", i.registry, i.repository, blobDigest)

	// Create a readerat implementation that uses HTTP range requests
	blobReader := &httpBlobReader{
		client:        i.httpClient,
		url:           blobURL,
		authToken:     &i.authToken,
		ctx:           ctx,
		imageAccessor: i,
	}

	// Try to get the size first
	size, err := blobReader.getSize()
	if err != nil {
		return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
	}

	// Get TOC offset using OpenFooter
	sr := io.NewSectionReader(blobReader, 0, size)
	tocOffset, _, err := estargz.OpenFooter(sr)
	if err != nil {
		return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
	}

	// Read TOC section (from tocOffset to end)
	tocSectionSize := size - tocOffset
	tocSection := make([]byte, tocSectionSize)
	_, err = blobReader.ReadAt(tocSection, tocOffset)
	if err != nil {
		return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
	}

	// The TOC section is gzipped tar, decompress and find stargz.index.json
	gzReader, err := gzip.NewReader(bytes.NewReader(tocSection))
	if err != nil {
		return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
	}
	defer gzReader.Close()

	// Parse as tar and find stargz.index.json
	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
		}

		// Look for stargz.index.json
		if header.Name == "stargz.index.json" {
			tocJSONBytes, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
			}

			var toc estargz.JTOC
			if err := json.Unmarshal(tocJSONBytes, &toc); err != nil {
				return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
			}

			// Cache it
			i.tocCache[blobDigest] = &toc

			return &toc, nil
		}
	}

	return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(fmt.Errorf("stargz.index.json not found in TOC section"))
}

// httpBlobReader implements io.ReaderAt for HTTP range requests
type httpBlobReader struct {
	client        *http.Client
	url           string
	authToken     *string // pointer to share token with parent imageAccessor
	ctx           context.Context
	size          int64
	sizeInit      bool
	imageAccessor *imageAccessor
}

func (r *httpBlobReader) getSize() (int64, error) {
	if r.sizeInit {
		return r.size, nil
	}

	req, err := http.NewRequestWithContext(r.ctx, "HEAD", r.url, nil)
	if err != nil {
		return -1, err
	}

	if *r.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+*r.authToken)
	}

	// Add Basic Auth if credentials are provided
	if r.imageAccessor.username != "" && r.imageAccessor.password != "" {
		req.SetBasicAuth(r.imageAccessor.username, r.imageAccessor.password)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return -1, err
	}
	resp.Body.Close()

	// Handle 401
	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		token, err := r.imageAccessor.getAuthToken(r.ctx, wwwAuth)
		if err != nil {
			return -1, fmt.Errorf("auth failed: %w", err)
		}

		// Retry with token
		req, err = http.NewRequestWithContext(r.ctx, "HEAD", r.url, nil)
		if err != nil {
			return -1, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		// Add Basic Auth if credentials are provided
		if r.imageAccessor.username != "" && r.imageAccessor.password != "" {
			req.SetBasicAuth(r.imageAccessor.username, r.imageAccessor.password)
		}

		resp, err = r.client.Do(req)
		if err != nil {
			return -1, err
		}
		resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("HEAD request failed: %d", resp.StatusCode)
	}

	r.size = resp.ContentLength
	r.sizeInit = true
	return r.size, nil
}

func (r *httpBlobReader) ReadAt(p []byte, off int64) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	rangeHeader := fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1)

	req, err := http.NewRequestWithContext(r.ctx, "GET", r.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", rangeHeader)

	if *r.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+*r.authToken)
	}

	// Add Basic Auth if credentials are provided
	if r.imageAccessor.username != "" && r.imageAccessor.password != "" {
		req.SetBasicAuth(r.imageAccessor.username, r.imageAccessor.password)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("range request failed: %d", resp.StatusCode)
	}

	return io.ReadFull(resp.Body, p)
}

// listFiles is a private helper method for internal use
func (i *imageAccessor) listFiles(ctx context.Context, blobDigest digest.Digest) ([]string, error) {
	toc, err := i.downloadTOC(ctx, blobDigest.String())
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range toc.Entries {
		if entry.Type == "reg" { // regular file
			files = append(files, entry.Name)
		}
	}

	return files, nil
}

// getFileMetadata is a private helper method for internal use
func (i *imageAccessor) getFileMetadata(ctx context.Context, blobDigest digest.Digest, fileName string) (*FileMetadata, error) {
	toc, err := i.downloadTOC(ctx, blobDigest.String())
	if err != nil {
		return nil, err
	}

	// Find the file entry
	for _, entry := range toc.Entries {
		if entry.Name == fileName && entry.Type == "reg" {
			// For now, treat as single chunk (will handle chunking later in Phase 3)
			return &FileMetadata{
				Size: entry.Size,
				Chunks: []Chunk{
					{
						Offset:         entry.Offset,
						Size:           entry.Size,
						CompressedSize: entry.ChunkSize,
					},
				},
			}, nil
		}
	}

	return nil, fmt.Errorf("file not found: %s", fileName)
}

// ImageIndex returns the index of all files in the image
func (i *imageAccessor) ImageIndex(ctx context.Context) (*ImageIndex, error) {
	// Check cache
	if i.index != nil {
		return i.index, nil
	}

	index := &ImageIndex{
		Layers: make([]*LayerInfo, 0),
		files:  make(map[string]*FileInfo),
	}

	// Iterate through all layers in the manifest
	for _, layer := range i.manifest.Layers {
		// Parse digest
		dgst, err := digest.Parse(layer.Digest)
		if err != nil {
			// Skip invalid digests
			continue
		}

		// Download TOC for this layer
		toc, err := i.downloadTOC(ctx, layer.Digest)
		if err != nil {
			// Skip layers that fail to load TOC
			continue
		}

		layerInfo := &LayerInfo{
			BlobDigest: dgst,
			Files:      make([]string, 0),
			FileSizes:  make(map[string]int64),
		}

		// Add files from this layer
		for _, entry := range toc.Entries {
			if entry.Type == "reg" { // regular file
				layerInfo.Files = append(layerInfo.Files, entry.Name)
				layerInfo.FileSizes[entry.Name] = entry.Size

				// Add to global file index (later layers override earlier ones)
				index.files[entry.Name] = &FileInfo{
					Path:       entry.Name,
					BlobDigest: dgst,
					Size:       entry.Size,
				}
			}
		}

		index.Layers = append(index.Layers, layerInfo)
	}

	// Cache the index
	i.index = index

	return index, nil
}

// buildIndex is deprecated and removed - use ImageIndex() instead

func (i *imageAccessor) OpenFile(ctx context.Context, path string, blobDigest digest.Digest) (*io.SectionReader, error) {
	// Note: If blobDigest is empty, the caller should use ImageIndex to find the blob
	// This method requires a valid blobDigest
	if blobDigest.String() == "" {
		return nil, fmt.Errorf("blobDigest is required for OpenFile")
	}

	// Construct blob URL
	blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", i.registry, i.repository, blobDigest.String())

	// Create a blob reader
	blobReader := &httpBlobReader{
		client:        i.httpClient,
		url:           blobURL,
		authToken:     &i.authToken,
		ctx:           ctx,
		imageAccessor: i,
	}

	// Get blob size
	size, err := blobReader.getSize()
	if err != nil {
		return nil, fmt.Errorf("failed to get blob size: %w", err)
	}

	// Open the stargz blob
	sr := io.NewSectionReader(blobReader, 0, size)
	reader, err := estargz.Open(sr)
	if err != nil {
		return nil, fmt.Errorf("failed to open stargz: %w", err)
	}

	// Open the specific file
	fileReader, err := reader.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", path, err)
	}

	// Get file metadata to know the size
	metadata, err := i.getFileMetadata(ctx, blobDigest, path)
	if err != nil {
		return nil, fmt.Errorf("failed to get file metadata: %w", err)
	}

	// Return a SectionReader that reads from the file
	// We need to wrap the fileReader in a way that allows ReadAt
	// Since fileReader is just an io.Reader, we need to read all content first
	content, err := io.ReadAll(fileReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}

	return io.NewSectionReader(bytes.NewReader(content), 0, metadata.Size), nil
}
