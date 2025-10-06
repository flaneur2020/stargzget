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

	"github.com/containerd/stargz-snapshotter/estargz"
	"github.com/opencontainers/go-digest"
)

type BlobAccessor interface {
	ListFiles(ctx context.Context, blobDigest digest.Digest) ([]string, error)

	GetFileMetadata(ctx context.Context, blobDigest digest.Digest, fileName string) (*FileMetadata, error)

	// OpenReader opens the stargz blob as a reader
	OpenReader(ctx context.Context, blobDigest digest.Digest) (*estargz.Reader, error)
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

type blobAccessor struct {
	httpClient     *http.Client
	registryClient RegistryClient
	registry       string
	repository     string
	// Cache: digest -> JTOC
	tocCache map[string]*estargz.JTOC
	// Auth token cache
	authToken string
}

func NewBlobAccessor(registryClient RegistryClient, registry, repository string) BlobAccessor {
	return &blobAccessor{
		httpClient:     &http.Client{},
		registryClient: registryClient,
		registry:       registry,
		repository:     repository,
		tocCache:       make(map[string]*estargz.JTOC),
	}
}

// getAuthToken gets auth token for blob access (similar to registry client)
func (b *blobAccessor) getAuthToken(ctx context.Context, wwwAuthenticate string) (string, error) {
	// Reuse the same logic from RegistryClient
	if b.authToken != "" {
		return b.authToken, nil
	}

	if !bytes.Contains([]byte(wwwAuthenticate), []byte("Bearer ")) {
		return "", fmt.Errorf("unsupported auth scheme: %s", wwwAuthenticate)
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
		return "", fmt.Errorf("no realm in WWW-Authenticate header")
	}

	// Build token URL
	tokenURL := fmt.Sprintf("%s?service=%s&scope=%s", realm, service, scope)

	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var authResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	token := authResp.Token
	if token == "" {
		token = authResp.AccessToken
	}

	b.authToken = token
	return token, nil
}

// downloadTOC downloads the stargz TOC using estargz library
func (b *blobAccessor) downloadTOC(ctx context.Context, blobDigest string) (*estargz.JTOC, error) {
	// Check cache
	if toc, ok := b.tocCache[blobDigest]; ok {
		return toc, nil
	}

	// Construct blob URL
	blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", b.registry, b.repository, blobDigest)

	// Create a readerat implementation that uses HTTP range requests
	blobReader := &httpBlobReader{
		client:       b.httpClient,
		url:          blobURL,
		authToken:    &b.authToken,
		ctx:          ctx,
		blobAccessor: b,
	}

	// Try to get the size first
	size, err := blobReader.getSize()
	if err != nil {
		return nil, fmt.Errorf("failed to get blob size: %w", err)
	}

	// Get TOC offset using OpenFooter
	sr := io.NewSectionReader(blobReader, 0, size)
	tocOffset, _, err := estargz.OpenFooter(sr)
	if err != nil {
		return nil, fmt.Errorf("failed to open footer: %w", err)
	}

	// Read TOC section (from tocOffset to end)
	tocSectionSize := size - tocOffset
	tocSection := make([]byte, tocSectionSize)
	_, err = blobReader.ReadAt(tocSection, tocOffset)
	if err != nil {
		return nil, fmt.Errorf("failed to read TOC section: %w", err)
	}

	// The TOC section is gzipped tar, decompress and find stargz.index.json
	gzReader, err := gzip.NewReader(bytes.NewReader(tocSection))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader for TOC: %w", err)
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
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}

		// Look for stargz.index.json
		if header.Name == "stargz.index.json" {
			tocJSONBytes, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, fmt.Errorf("failed to read TOC JSON: %w", err)
			}

			var toc estargz.JTOC
			if err := json.Unmarshal(tocJSONBytes, &toc); err != nil {
				return nil, fmt.Errorf("failed to parse TOC JSON: %w", err)
			}

			// Cache it
			b.tocCache[blobDigest] = &toc

			return &toc, nil
		}
	}

	return nil, fmt.Errorf("stargz.index.json not found in TOC section")
}

// httpBlobReader implements io.ReaderAt for HTTP range requests
type httpBlobReader struct {
	client       *http.Client
	url          string
	authToken    *string // pointer to share token with parent blobAccessor
	ctx          context.Context
	size         int64
	sizeInit     bool
	blobAccessor *blobAccessor
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

	resp, err := r.client.Do(req)
	if err != nil {
		return -1, err
	}
	resp.Body.Close()

	// Handle 401
	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		token, err := r.blobAccessor.getAuthToken(r.ctx, wwwAuth)
		if err != nil {
			return -1, fmt.Errorf("auth failed: %w", err)
		}

		// Retry with token
		req, err = http.NewRequestWithContext(r.ctx, "HEAD", r.url, nil)
		if err != nil {
			return -1, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

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

func (b *blobAccessor) ListFiles(ctx context.Context, blobDigest digest.Digest) ([]string, error) {
	toc, err := b.downloadTOC(ctx, blobDigest.String())
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

func (b *blobAccessor) GetFileMetadata(ctx context.Context, blobDigest digest.Digest, fileName string) (*FileMetadata, error) {
	toc, err := b.downloadTOC(ctx, blobDigest.String())
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

func (b *blobAccessor) OpenReader(ctx context.Context, blobDigest digest.Digest) (*estargz.Reader, error) {
	// Construct blob URL
	blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", b.registry, b.repository, blobDigest.String())

	// Create a blob reader
	blobReader := &httpBlobReader{
		client:       b.httpClient,
		url:          blobURL,
		authToken:    &b.authToken,
		ctx:          ctx,
		blobAccessor: b,
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

	return reader, nil
}
