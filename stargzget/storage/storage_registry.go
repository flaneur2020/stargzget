package storage

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	stargzerrors "github.com/flaneur2020/stargz-get/stargzget/errors"
	"github.com/flaneur2020/stargz-get/stargzget/logger"
	"github.com/opencontainers/go-digest"
)

// RemoteRegistryStorage coordinates manifest fetching and blob access against an OCI registry.
type RemoteRegistryStorage struct {
	httpClient *http.Client
	username   string
	password   string
	authToken  string
}

// Manifest represents an OCI image manifest.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config,omitempty"`
	Layers        []Layer      `json:"layers,omitempty"`
	Manifests     []Descriptor `json:"manifests,omitempty"` // For OCI index
}

// Descriptor is an OCI descriptor.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Layer represents a manifest layer.
type Layer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// NewRemoteRegistryStorage creates a registry-backed storage helper.
func NewRemoteRegistryStorage(insecure bool) *RemoteRegistryStorage {
	client := &http.Client{}
	if insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return &RemoteRegistryStorage{httpClient: client}
}

// WithCredential returns a new storage instance with credentials.
func (c *RemoteRegistryStorage) WithCredential(username, password string) *RemoteRegistryStorage {
	return &RemoteRegistryStorage{
		httpClient: c.httpClient,
		username:   username,
		password:   password,
		authToken:  c.authToken,
	}
}

// NewStorage creates a blob storage instance for a specific repository.
func (c *RemoteRegistryStorage) NewStorage(registry, repository string, manifest *Manifest) Storage {
	return &registryBlobStorage{
		client:     c,
		httpClient: c.httpClient,
		registry:   registry,
		repository: repository,
		manifest:   manifest,
		username:   c.username,
		password:   c.password,
		authToken:  c.authToken,
	}
}

// GetManifest fetches the manifest for an image reference.
func (c *RemoteRegistryStorage) GetManifest(ctx context.Context, imageRef string) (*Manifest, error) {
	logger.Info("Fetching manifest for image: %s", imageRef)

	registry, repository, tag, err := parseImageRef(imageRef)
	if err != nil {
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	scheme := getScheme(registry)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repository, tag)
	logger.Debug("Manifest URL: %s", url)

	// Try anonymous request first - let server tell us auth requirements
	manifest, err := c.fetchManifest(ctx, url)
	if err == nil {
		return manifest, nil
	}

	// Check if it's an auth error
	if !isAuthError(err) {
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	// Extract auth requirements and authenticate
	wwwAuth := extractWWWAuth(err)
	if err := c.authenticate(ctx, registry, repository, wwwAuth); err != nil {
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	// Retry with authentication
	manifest, err = c.fetchManifest(ctx, url)
	if err != nil {
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	// Handle OCI index - fetch the first platform-specific manifest
	if len(manifest.Manifests) > 0 {
		manifestDigest := manifest.Manifests[0].Digest
		logger.Info("Image is an index; selecting first manifest: %s", manifestDigest)

		indexURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repository, manifestDigest)
		manifest, err = c.fetchManifest(ctx, indexURL)
		if err != nil {
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
	}

	return manifest, nil
}

// fetchManifest performs a single manifest fetch request.
func (c *RemoteRegistryStorage) fetchManifest(ctx context.Context, url string) (*Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")

	// Apply auth if we have it
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		return nil, &authError{wwwAuth: wwwAuth}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(body))
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// authenticate handles the authentication flow based on WWW-Authenticate header.
func (c *RemoteRegistryStorage) authenticate(ctx context.Context, registry, repository, wwwAuth string) error {
	if wwwAuth == "" {
		return fmt.Errorf("no WWW-Authenticate header in 401 response")
	}

	// Bearer token authentication (Docker/Harbor/GitHub)
	if strings.HasPrefix(wwwAuth, "Bearer ") {
		token, err := c.getBearerToken(ctx, wwwAuth)
		if err != nil {
			return err
		}
		c.authToken = token
		logger.Debug("Acquired bearer token (length: %d)", len(token))
		return nil
	}

	// Basic authentication
	if strings.HasPrefix(wwwAuth, "Basic ") {
		if c.username == "" || c.password == "" {
			return fmt.Errorf("registry requires basic auth but no credentials provided")
		}
		logger.Info("Using Basic authentication")
		return nil
	}

	return fmt.Errorf("unsupported auth scheme: %s", wwwAuth)
}

// getBearerToken requests a bearer token from the auth service.
func (c *RemoteRegistryStorage) getBearerToken(ctx context.Context, wwwAuth string) (string, error) {
	params := parseWWWAuth(wwwAuth)

	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("no realm in WWW-Authenticate header")
	}

	// Build token URL
	tokenURL := realm
	if service := params["service"]; service != "" {
		tokenURL += "?service=" + service
	}
	if scope := params["scope"]; scope != "" {
		if strings.Contains(tokenURL, "?") {
			tokenURL += "&scope=" + scope
		} else {
			tokenURL += "?scope=" + scope
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", err
	}

	// Use Basic auth for token request if we have credentials
	if c.username != "" && c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
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
		return "", err
	}

	token := authResp.Token
	if token == "" {
		token = authResp.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("no token in auth response")
	}

	return token, nil
}

// applyAuth applies authentication to a request.
func (c *RemoteRegistryStorage) applyAuth(req *http.Request) {
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	} else if c.username != "" && c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
}

// registryBlobStorage implements Storage for registry blobs.
type registryBlobStorage struct {
	client     *RemoteRegistryStorage
	httpClient *http.Client
	registry   string
	repository string
	manifest   *Manifest
	username   string
	password   string
	authToken  string
}

// ListBlobs lists all blobs in the manifest.
func (s *registryBlobStorage) ListBlobs(ctx context.Context) ([]BlobDescriptor, error) {
	if s.manifest == nil {
		return nil, fmt.Errorf("manifest not loaded for registry storage")
	}

	blobs := make([]BlobDescriptor, 0, len(s.manifest.Layers))
	for _, layer := range s.manifest.Layers {
		dgst, err := digest.Parse(layer.Digest)
		if err != nil {
			continue
		}
		blobs = append(blobs, BlobDescriptor{
			Digest:    dgst,
			Size:      layer.Size,
			MediaType: layer.MediaType,
		})
	}
	return blobs, nil
}

// ReadBlob reads a range of bytes from a blob.
func (s *registryBlobStorage) ReadBlob(ctx context.Context, blobDigest digest.Digest, offset int64, length int64) (io.ReadCloser, error) {
	if offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}

	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", getScheme(s.registry), s.registry, s.repository, blobDigest.String())

	// Try with existing auth (reuse token from manifest fetch)
	body, err := s.fetchBlobRange(ctx, url, offset, length)
	if err == nil {
		return body, nil
	}

	// Check if it's an auth error
	if !isAuthError(err) {
		return nil, err
	}

	// Need to authenticate
	wwwAuth := extractWWWAuth(err)
	if err := s.authenticate(ctx, wwwAuth); err != nil {
		return nil, err
	}

	// Retry with authentication
	return s.fetchBlobRange(ctx, url, offset, length)
}

// fetchBlobRange performs a single blob range request.
func (s *registryBlobStorage) fetchBlobRange(ctx context.Context, url string, offset, length int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set range header
	if length > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	} else {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	// Apply auth if we have it
	s.applyAuth(req)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		return nil, &authError{wwwAuth: wwwAuth}
	}

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("range request failed: %d %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// authenticate handles the authentication flow for blob storage.
func (s *registryBlobStorage) authenticate(ctx context.Context, wwwAuth string) error {
	if wwwAuth == "" {
		return fmt.Errorf("no WWW-Authenticate header in 401 response")
	}

	// Bearer token authentication
	if strings.HasPrefix(wwwAuth, "Bearer ") {
		token, err := s.client.getBearerToken(ctx, wwwAuth)
		if err != nil {
			return fmt.Errorf("auth failed: %w", err)
		}
		s.authToken = token
		return nil
	}

	// Basic authentication
	if strings.HasPrefix(wwwAuth, "Basic ") {
		if s.username == "" || s.password == "" {
			return fmt.Errorf("registry requires basic auth but no credentials provided")
		}
		return nil
	}

	return fmt.Errorf("unsupported auth scheme: %s", wwwAuth)
}

// applyAuth applies authentication to a request.
func (s *registryBlobStorage) applyAuth(req *http.Request) {
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	} else if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
}

// Helper functions

// parseImageRef parses an image reference into registry, repository, and tag.
func parseImageRef(imageRef string) (string, string, string, error) {
	parts := strings.SplitN(imageRef, "/", 2)
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("invalid image ref: %s", imageRef)
	}

	registry := parts[0]
	rest := parts[1]
	repoParts := strings.Split(rest, ":")
	if len(repoParts) != 2 {
		return "", "", "", fmt.Errorf("missing tag in image ref: %s", imageRef)
	}

	return registry, repoParts[0], repoParts[1], nil
}

// getScheme returns http or https based on the registry host.
func getScheme(registry string) string {
	host := registry
	if idx := strings.Index(registry, ":"); idx != -1 {
		host = registry[:idx]
	}
	if host == "localhost" || host == "127.0.0.1" {
		return "http"
	}
	return "https"
}

// parseWWWAuth parses WWW-Authenticate header into a map of parameters.
func parseWWWAuth(wwwAuth string) map[string]string {
	params := make(map[string]string)

	// Remove "Bearer " prefix
	authStr := strings.TrimPrefix(wwwAuth, "Bearer ")

	// Parse key=value pairs
	parts := strings.Split(authStr, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			params[kv[0]] = strings.Trim(kv[1], "\"")
		}
	}

	return params
}

// authError represents an authentication error with WWW-Authenticate header.
type authError struct {
	wwwAuth string
}

func (e *authError) Error() string {
	return "authentication required"
}

// isAuthError checks if an error is an authentication error.
func isAuthError(err error) bool {
	_, ok := err.(*authError)
	return ok
}

// extractWWWAuth extracts the WWW-Authenticate header from an auth error.
func extractWWWAuth(err error) string {
	if authErr, ok := err.(*authError); ok {
		return authErr.wwwAuth
	}
	return ""
}
