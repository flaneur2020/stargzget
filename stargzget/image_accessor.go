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
	"sort"
	"strings"

	"github.com/flaneur2020/stargz-get/estargzutil"
	"github.com/flaneur2020/stargz-get/logger"
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
// If blobDigest is empty, returns files from all layers (later layers override earlier ones)
func (idx *ImageIndex) FilterFiles(pathPattern string, blobDigest digest.Digest) []*FileInfo {
	// Normalize path pattern
	if pathPattern == "." || pathPattern == "/" || pathPattern == "" {
		pathPattern = "" // Match all files
	}

	matcher := newPathMatcher(pathPattern)
	var results []*FileInfo

	// If no blob digest specified, search in the global file index (later layers override earlier ones)
	if blobDigest.String() == "" {
		for _, fileInfo := range idx.files {
			if matcher.matches(fileInfo.Path) {
				results = append(results, fileInfo)
			}
		}
		return results
	}

	// Blob digest is specified - search only in that specific layer
	for _, layer := range idx.Layers {
		if layer.BlobDigest == blobDigest {
			for _, filePath := range layer.Files {
				if matcher.matches(filePath) {
					size := layer.FileSizes[filePath]
					results = append(results, &FileInfo{
						Path:       filePath,
						BlobDigest: layer.BlobDigest,
						Size:       size,
					})
				}
			}
			break
		}
	}

	return results
}

// pathMatcher encapsulates path pattern matching logic for FilterFiles
type pathMatcher struct {
	matchAll  bool
	pattern   string
	dirPrefix bool
}

func newPathMatcher(pattern string) pathMatcher {
	if pattern == "" {
		return pathMatcher{matchAll: true}
	}

	dirPrefix := strings.HasSuffix(pattern, "/")
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}

	return pathMatcher{
		pattern:   pattern,
		dirPrefix: dirPrefix,
	}
}

func (m pathMatcher) matches(path string) bool {
	if m.matchAll {
		return true
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if m.dirPrefix {
		return strings.HasPrefix(path, m.pattern)
	}

	return path == m.pattern || strings.HasPrefix(path, m.pattern+"/")
}

type ImageAccessor interface {
	// ImageIndex returns the index of all files in the image
	ImageIndex(ctx context.Context) (*ImageIndex, error)

	// GetFileMetadata returns metadata (size and chunk layout) for a file
	GetFileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error)

	// ReadChunk fetches and decompresses a single chunk of file data
	ReadChunk(ctx context.Context, path string, blobDigest digest.Digest, chunk Chunk) ([]byte, error)

	// WithCredential returns a new ImageAccessor with the provided credentials
	WithCredential(username, password string) ImageAccessor
}

type FileMetadata struct {
	Size   int64
	Chunks []Chunk
}

type Chunk struct {
	Offset           int64 // Uncompressed offset within the file
	Size             int64 // Uncompressed size of this chunk
	CompressedOffset int64 // Offset within the blob where this chunk's gzip stream begins
	InnerOffset      int64 // Uncompressed offset within the gzip member to reach this chunk
}

type tocEntry struct {
	Name          string            `json:"name"`
	Type          string            `json:"type"`
	Size          int64             `json:"size,omitempty"`
	Offset        int64             `json:"offset,omitempty"`
	ChunkOffset   int64             `json:"chunkOffset,omitempty"`
	ChunkSize     int64             `json:"chunkSize,omitempty"`
	InnerOffset   int64             `json:"innerOffset,omitempty"`
	ChunkDigest   string            `json:"chunkDigest,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
	nextOffset    int64
	chunkTopIndex int
}

type jtoc struct {
	Version int         `json:"version"`
	Entries []*tocEntry `json:"entries"`
}

type imageAccessor struct {
	httpClient     *http.Client
	registryClient RegistryClient
	registry       string
	repository     string
	manifest       *Manifest
	// Cache: digest -> TOC
	tocCache map[string]*jtoc
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
		tocCache:       make(map[string]*jtoc),
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

// downloadTOC downloads the stargz TOC.
func (i *imageAccessor) downloadTOC(ctx context.Context, blobDigest string) (*jtoc, error) {
	// Check cache
	if toc, ok := i.tocCache[blobDigest]; ok {
		logger.Debug("TOC cache hit for blob: %s", blobDigest[:12])
		return toc, nil
	}

	logger.Info("Downloading TOC for blob: %s", blobDigest[:12]+"...")

	// Construct blob URL
	scheme := getScheme(i.registry)
	blobURL := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", scheme, i.registry, i.repository, blobDigest)

	logger.Debug("TOC URL: %s", blobURL)

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
		logger.Error("Failed to get blob size: %v", err)
		return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
	}

	logger.Debug("Blob size: %d bytes", size)

	// Get TOC offset using OpenFooter
	sr := io.NewSectionReader(blobReader, 0, size)
	tocOffset, _, err := estargzutil.OpenFooter(sr)
	if err != nil {
		logger.Error("Failed to read stargz footer: %v", err)
		return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
	}

	logger.Debug("TOC offset: %d (%.2f%% of blob)", tocOffset, float64(tocOffset)/float64(size)*100)

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
		if header.Name == estargzutil.TOCTarName {
			tocJSONBytes, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, ErrTOCDownload.WithDetail("blobDigest", blobDigest).WithCause(err)
			}

			var toc jtoc
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

func (r *httpBlobReader) setAuthHeaders(req *http.Request) {
	if *r.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+*r.authToken)
	}

	if r.imageAccessor.username != "" && r.imageAccessor.password != "" {
		req.SetBasicAuth(r.imageAccessor.username, r.imageAccessor.password)
	}
}

func (r *httpBlobReader) getSize() (int64, error) {
	if r.sizeInit {
		return r.size, nil
	}

	req, err := http.NewRequestWithContext(r.ctx, "HEAD", r.url, nil)
	if err != nil {
		return -1, err
	}

	r.setAuthHeaders(req)

	resp, err := r.client.Do(req)
	if err != nil {
		return -1, err
	}
	resp.Body.Close()

	// Handle 401
	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		if _, err := r.imageAccessor.getAuthToken(r.ctx, wwwAuth); err != nil {
			return -1, fmt.Errorf("auth failed: %w", err)
		}

		// Retry with token
		req, err = http.NewRequestWithContext(r.ctx, "HEAD", r.url, nil)
		if err != nil {
			return -1, err
		}
		r.setAuthHeaders(req)
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
	r.setAuthHeaders(req)

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

func (r *httpBlobReader) openRangeReader(off int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(r.ctx, "GET", r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	r.setAuthHeaders(req)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		if _, err := r.imageAccessor.getAuthToken(r.ctx, wwwAuth); err != nil {
			return nil, fmt.Errorf("auth failed: %w", err)
		}
		req, err = http.NewRequestWithContext(r.ctx, "GET", r.url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
		r.setAuthHeaders(req)
		resp, err = r.client.Do(req)
		if err != nil {
			return nil, err
		}
	}

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("range request failed: %d", resp.StatusCode)
	}

	return resp.Body, nil
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

// GetFileMetadata returns metadata about a file, including its chunk layout.
func (i *imageAccessor) GetFileMetadata(ctx context.Context, blobDigest digest.Digest, fileName string) (*FileMetadata, error) {
	toc, err := i.downloadTOC(ctx, blobDigest.String())
	if err != nil {
		return nil, err
	}

	var (
		found    bool
		fileSize int64
		chunks   []Chunk
	)

	for _, entry := range toc.Entries {
		if entry.Name != fileName {
			continue
		}

		switch entry.Type {
		case "reg":
			found = true
			fileSize = entry.Size
			chunkSize := entry.ChunkSize
			if chunkSize == 0 && entry.Size != 0 {
				chunkSize = entry.Size
			}
			chunks = append(chunks, Chunk{
				Offset:           entry.ChunkOffset,
				Size:             chunkSize,
				CompressedOffset: entry.Offset,
				InnerOffset:      entry.InnerOffset,
			})
		case "chunk":
			found = true
			chunkSize := entry.ChunkSize
			if chunkSize == 0 && fileSize != 0 {
				chunkSize = fileSize - entry.ChunkOffset
			}
			chunks = append(chunks, Chunk{
				Offset:           entry.ChunkOffset,
				Size:             chunkSize,
				CompressedOffset: entry.Offset,
				InnerOffset:      entry.InnerOffset,
			})
		}
	}

	if !found {
		return nil, fmt.Errorf("file not found: %s", fileName)
	}

	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].Offset == chunks[j].Offset {
			return chunks[i].InnerOffset < chunks[j].InnerOffset
		}
		return chunks[i].Offset < chunks[j].Offset
	})

	for idx := range chunks {
		if chunks[idx].Size == 0 {
			nextOffset := fileSize
			if idx+1 < len(chunks) {
				nextOffset = chunks[idx+1].Offset
			}
			size := nextOffset - chunks[idx].Offset
			if size <= 0 {
				size = fileSize - chunks[idx].Offset
			}
			if size < 0 {
				size = 0
			}
			chunks[idx].Size = size
		}
	}

	return &FileMetadata{
		Size:   fileSize,
		Chunks: chunks,
	}, nil
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

func (i *imageAccessor) ReadChunk(ctx context.Context, path string, blobDigest digest.Digest, chunk Chunk) ([]byte, error) {
	if chunk.Size == 0 {
		return []byte{}, nil
	}

	if blobDigest.String() == "" {
		return nil, fmt.Errorf("blobDigest is required for chunk reads")
	}

	scheme := getScheme(i.registry)
	blobURL := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", scheme, i.registry, i.repository, blobDigest.String())

	blobReader := &httpBlobReader{
		client:        i.httpClient,
		url:           blobURL,
		authToken:     &i.authToken,
		ctx:           ctx,
		imageAccessor: i,
	}

	rangeReader, err := blobReader.openRangeReader(chunk.CompressedOffset)
	if err != nil {
		return nil, err
	}
	defer rangeReader.Close()

	gz, err := gzip.NewReader(rangeReader)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	if chunk.InnerOffset > 0 {
		if _, err := io.CopyN(io.Discard, gz, chunk.InnerOffset); err != nil {
			return nil, err
		}
	}

	buf := make([]byte, chunk.Size)
	n, err := io.ReadFull(gz, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	if int64(n) != chunk.Size {
		return nil, io.ErrUnexpectedEOF
	}

	return buf, nil
}
