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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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

func (c *RemoteRegistryStorage) WithCredential(username, password string) *RemoteRegistryStorage {
	return &RemoteRegistryStorage{
		httpClient: c.httpClient,
		username:   username,
		password:   password,
		authToken:  c.authToken,
	}
}

func (c *RemoteRegistryStorage) NewStorage(registry, repository string, manifest *Manifest) Storage {
	return &registryBlobStorage{
		client:     c,
		httpClient: c.httpClient,
		registry:   registry,
		repository: repository,
		manifest:   manifest,
		username:   c.username,
		password:   c.password,
	}
}

func (c *RemoteRegistryStorage) GetManifest(ctx context.Context, imageRef string) (*Manifest, error) {
	logger.Info("Fetching manifest for image: %s", imageRef)

	registry, repository, tag, err := parseImageRef(imageRef)
	if err != nil {
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	scheme := getScheme(registry)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repository, tag)
	logger.Debug("Manifest URL: %s", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")
	// Don't send credentials on first request - let the server tell us what auth method to use
	// c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		logger.Error("HTTP request failed: %v", err)
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")

		// Check if it's Bearer token auth or Basic auth
		if strings.HasPrefix(wwwAuth, "Bearer ") {
			// Docker/OCI registry with token auth
			token, err := c.getAuthToken(ctx, registry, repository, wwwAuth)
			if err != nil {
				logger.Error("Failed to get auth token: %v", err)
				return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
			}
			c.authToken = token
		} else if strings.HasPrefix(wwwAuth, "Basic ") {
			// Harbor or other registries using Basic auth
			if c.username == "" || c.password == "" {
				return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(fmt.Errorf("registry requires basic auth but no credentials provided"))
			}
			logger.Info("Using Basic authentication for registry")
		} else {
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(fmt.Errorf("unsupported auth scheme: %s", wwwAuth))
		}

		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
		req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
		req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")
		c.applyAuth(req)

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(body)))
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	if len(manifest.Manifests) > 0 {
		manifestDigest := manifest.Manifests[0].Digest
		logger.Info("Image is an index; selecting first manifest: %s", manifestDigest)

		indexReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repository, manifestDigest), nil)
		if err != nil {
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		indexReq.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
		indexReq.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
		c.applyAuth(indexReq)

		resp2, err := c.httpClient.Do(indexReq)
		if err != nil {
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp2.Body)
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(fmt.Errorf("manifest request returned %d: %s", resp2.StatusCode, string(body)))
		}

		var manifest2 Manifest
		if err := json.NewDecoder(resp2.Body).Decode(&manifest2); err != nil {
			return nil, stargzerrors.ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		manifest = manifest2
	}

	return &manifest, nil
}

func (c *RemoteRegistryStorage) applyAuth(req *http.Request) {
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	} else if c.username != "" && c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
}

func (c *RemoteRegistryStorage) getAuthToken(ctx context.Context, registry, repository, wwwAuthenticate string) (string, error) {
	if !strings.HasPrefix(wwwAuthenticate, "Bearer ") {
		return "", stargzerrors.ErrAuthFailed.WithCause(fmt.Errorf("unsupported auth scheme: %s", wwwAuthenticate))
	}

	params := make(map[string]string)
	authStr := strings.TrimPrefix(wwwAuthenticate, "Bearer ")
	parts := strings.Split(authStr, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			params[kv[0]] = strings.Trim(kv[1], "\"")
		}
	}

	realm := params["realm"]
	service := params["service"]
	scope := params["scope"]

	if realm == "" {
		return "", stargzerrors.ErrAuthFailed.WithCause(fmt.Errorf("no realm in WWW-Authenticate header"))
	}

	tokenURL := fmt.Sprintf("%s?service=%s&scope=%s", realm, service, scope)
	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", stargzerrors.ErrAuthFailed.WithCause(err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", stargzerrors.ErrAuthFailed.WithCause(err)
	}
	defer resp.Body.Close()

	logger.Debug("Token request status: %d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", stargzerrors.ErrAuthFailed.WithCause(fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body)))
	}

	var authResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", stargzerrors.ErrAuthFailed.WithCause(err)
	}

	token := authResp.Token
	if token == "" {
		token = authResp.AccessToken
	}
	if len(token) > 50 {
		logger.Debug("Received token (first 50 chars): %s...", token[:50])
	} else {
		logger.Debug("Received token: %s", token)
	}
	return token, nil
}

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

func (s *registryBlobStorage) ReadBlob(ctx context.Context, blobDigest digest.Digest, offset int64, length int64) (io.ReadCloser, error) {
	if offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}

	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", getScheme(s.registry), s.registry, s.repository, blobDigest.String())
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	if length > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	} else {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	s.applyAuth(req)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		token, err := s.client.getAuthToken(ctx, s.registry, s.repository, wwwAuth)
		if err != nil {
			return nil, fmt.Errorf("auth failed: %w", err)
		}
		s.authToken = token

		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		if length > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
		} else {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		s.applyAuth(req)

		resp, err = s.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
	}

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("range request failed: %d %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

func (s *registryBlobStorage) applyAuth(req *http.Request) {
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
}

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
