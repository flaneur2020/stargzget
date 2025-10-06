package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sync"

	"github.com/containerd/containerd/v2/pkg/reference"
	digest "github.com/opencontainers/go-digest"
)

// registryClient handles downloading blobs using native HTTP Range requests
type registryClient struct {
	httpClient *http.Client
	refSpec    reference.Spec
	username   string
	password   string
	insecure   bool
	plainHTTP  bool

	// Token cache
	tokenMu sync.RWMutex
	token   string
}

// newRegistryClient creates a new registry client with HTTP Range support
func newRegistryClient(httpClient *http.Client, refSpec reference.Spec, username, password string, insecure, plainHTTP bool) *registryClient {
	// Configure client to handle redirects properly
	// Remove Authorization header on redirect to different hosts
	if httpClient.CheckRedirect == nil {
		httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			// Remove Authorization header when redirecting to different host
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			return nil
		}
	}

	return &registryClient{
		httpClient: httpClient,
		refSpec:    refSpec,
		username:   username,
		password:   password,
		insecure:   insecure,
		plainHTTP:  plainHTTP,
	}
}

// getBlobURL constructs the blob download URL for a registry
func (rc *registryClient) getBlobURL(blobDigest digest.Digest) string {
	scheme := "https"
	if rc.plainHTTP {
		scheme = "http"
	}

	registry := rc.refSpec.Hostname()
	// Use Locator which contains registry/repository, then strip the registry part
	// Locator format: "ghcr.io/stargz-containers/ubuntu"
	// We need just "stargz-containers/ubuntu"
	repository := rc.refSpec.Locator
	if registry != "" {
		// Remove "registry/" prefix from locator
		repository = repository[len(registry)+1:] // +1 for the "/"
	}

	// Format: https://registry/v2/<repository>/blobs/<digest>
	return fmt.Sprintf("%s://%s/v2/%s/blobs/%s", scheme, registry, repository, blobDigest.String())
}

// authenticate performs Docker registry authentication (Bearer token)
func (rc *registryClient) authenticate(ctx context.Context, authenticateHeader string) error {
	// Parse WWW-Authenticate header using regex
	// Format: Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/ubuntu:pull"

	realmRe := regexp.MustCompile(`realm="([^"]+)"`)
	serviceRe := regexp.MustCompile(`service="([^"]+)"`)
	scopeRe := regexp.MustCompile(`scope="([^"]+)"`)

	realmMatch := realmRe.FindStringSubmatch(authenticateHeader)
	serviceMatch := serviceRe.FindStringSubmatch(authenticateHeader)
	scopeMatch := scopeRe.FindStringSubmatch(authenticateHeader)

	if len(realmMatch) < 2 {
		return fmt.Errorf("invalid WWW-Authenticate header: no realm found in %q", authenticateHeader)
	}

	realm := realmMatch[1]
	service := ""
	scope := ""

	if len(serviceMatch) >= 2 {
		service = serviceMatch[1]
	}
	if len(scopeMatch) >= 2 {
		scope = scopeMatch[1]
	}

	// Build auth URL with proper URL encoding
	authURL := realm
	params := url.Values{}
	if service != "" {
		params.Add("service", service)
	}
	if scope != "" {
		params.Add("scope", scope)
	}
	if len(params) > 0 {
		authURL = authURL + "?" + params.Encode()
	}

	// Create auth request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create auth request: %w", err)
	}

	// Add Basic Auth if credentials provided
	if rc.username != "" && rc.password != "" {
		req.SetBasicAuth(rc.username, rc.password)
	}

	// Execute auth request
	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute auth request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth request failed with status %d, URL: %s, body: %s", resp.StatusCode, authURL, string(body))
	}

	// Parse token response
	var tokenResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to parse token response: %w", err)
	}

	// Store token (prefer token over access_token)
	token := tokenResp.Token
	if token == "" {
		token = tokenResp.AccessToken
	}

	rc.tokenMu.Lock()
	rc.token = token
	rc.tokenMu.Unlock()

	return nil
}

// doRequest performs an HTTP request with authentication retry
func (rc *registryClient) doRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Add auth token if available
	rc.tokenMu.RLock()
	token := rc.token
	rc.tokenMu.RUnlock()

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if rc.username != "" && rc.password != "" {
		req.SetBasicAuth(rc.username, rc.password)
	}

	// Execute request
	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Check if we need to authenticate
	if resp.StatusCode == http.StatusUnauthorized {
		authenticateHeader := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()

		if authenticateHeader == "" {
			return nil, fmt.Errorf("unauthorized but no WWW-Authenticate header")
		}

		// Perform authentication
		if err := rc.authenticate(ctx, authenticateHeader); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}

		// Retry request with new token
		rc.tokenMu.RLock()
		req.Header.Set("Authorization", "Bearer "+rc.token)
		rc.tokenMu.RUnlock()

		resp, err = rc.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// fetchBlobRange downloads a specific byte range from a blob using HTTP Range request
func (rc *registryClient) fetchBlobRange(ctx context.Context, blobDigest digest.Digest, offset, size int64) (io.ReadCloser, error) {
	blobURL := rc.getBlobURL(blobDigest)

	// Create HTTP request with Range header
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Range header: "bytes=offset-end"
	end := offset + size - 1
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
	req.Header.Set("Accept", "application/octet-stream")

	// Execute request with auth
	resp, err := rc.doRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	// Check response status
	// 206 Partial Content is expected for range requests
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Verify we got the correct amount of data
	if resp.ContentLength >= 0 && resp.ContentLength != size {
		resp.Body.Close()
		return nil, fmt.Errorf("expected %d bytes, but server returned %d bytes", size, resp.ContentLength)
	}

	return resp.Body, nil
}

// fetchEntireBlob downloads the entire blob
func (rc *registryClient) fetchEntireBlob(ctx context.Context, blobDigest digest.Digest) (io.ReadCloser, int64, error) {
	blobURL := rc.getBlobURL(blobDigest)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/octet-stream")

	resp, err := rc.doRequest(ctx, req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to execute request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return resp.Body, resp.ContentLength, nil
}

// getBlobSize gets the size of a blob using a HEAD request (most efficient)
func (rc *registryClient) getBlobSize(ctx context.Context, blobDigest digest.Digest) (int64, error) {
	blobURL := rc.getBlobURL(blobDigest)

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, blobURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create HEAD request: %w", err)
	}

	resp, err := rc.doRequest(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute HEAD request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD request failed with status: %d", resp.StatusCode)
	}

	if resp.ContentLength < 0 {
		return 0, fmt.Errorf("server did not return Content-Length")
	}

	return resp.ContentLength, nil
}

// fetchTOC downloads just the TOC (footer + TOC JSON) from the end of the blob
func (rc *registryClient) fetchTOC(ctx context.Context, blobDigest digest.Digest, blobSize int64) (io.ReadCloser, error) {
	const tocFetchSize = 50 * 1024 // 50KB

	var fetchSize int64 = tocFetchSize
	if blobSize < tocFetchSize {
		fetchSize = blobSize
	}

	offset := blobSize - fetchSize
	return rc.fetchBlobRange(ctx, blobDigest, offset, fetchSize)
}
