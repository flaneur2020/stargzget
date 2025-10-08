package stargzget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/flaneur2020/stargz-get/stargzget/logger"
)

type RegistryClient interface {
	GetManifest(ctx context.Context, imageRef string) (*Manifest, error)
	WithCredential(username, password string) RegistryClient
}

type Manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	MediaType     string     `json:"mediaType"`
	Config        Descriptor `json:"config,omitempty"`
	Layers        []Layer    `json:"layers,omitempty"`
	// For OCI index
	Manifests []Descriptor `json:"manifests,omitempty"`
}

type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type Layer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

func (l *Layer) IsStargz() bool {
	// stargz layers use these media types
	return strings.Contains(l.MediaType, "gzip") ||
		strings.Contains(l.MediaType, "zstd+esgz") ||
		l.MediaType == "application/vnd.oci.image.layer.v1.tar+gzip"
}

type registryClient struct {
	httpClient *http.Client
	username   string
	password   string
}

func NewRegistryClient() RegistryClient {
	return &registryClient{
		httpClient: &http.Client{},
	}
}

// WithCredential returns a new RegistryClient with the provided credentials
func (c *registryClient) WithCredential(username, password string) RegistryClient {
	return &registryClient{
		httpClient: c.httpClient,
		username:   username,
		password:   password,
	}
}

// parseImageRef parses image reference like "ghcr.io/stargz-containers/node:13.13.0-esgz"
// returns (registry, repository, tag)
func parseImageRef(imageRef string) (string, string, string, error) {
	// Split registry and rest
	parts := strings.SplitN(imageRef, "/", 2)
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("invalid image ref: %s", imageRef)
	}

	registry := parts[0]
	rest := parts[1]

	// Split repository and tag
	repoParts := strings.Split(rest, ":")
	if len(repoParts) != 2 {
		return "", "", "", fmt.Errorf("missing tag in image ref: %s", imageRef)
	}

	repository := repoParts[0]
	tag := repoParts[1]

	return registry, repository, tag, nil
}

// getScheme returns http for localhost/127.0.0.1, otherwise https
func getScheme(registry string) string {
	// Extract host without port
	host := registry
	if idx := strings.Index(registry, ":"); idx != -1 {
		host = registry[:idx]
	}

	// Use http for localhost and 127.0.0.1
	if host == "localhost" || host == "127.0.0.1" {
		return "http"
	}

	return "https"
}

type authResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func (c *registryClient) getAuthToken(ctx context.Context, registry, repository, wwwAuthenticate string) (string, error) {
	// Parse WWW-Authenticate header
	// Example: Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:stargz-containers/node:pull"

	if !strings.HasPrefix(wwwAuthenticate, "Bearer ") {
		return "", ErrAuthFailed.WithCause(fmt.Errorf("unsupported auth scheme: %s", wwwAuthenticate))
	}

	params := make(map[string]string)
	authStr := strings.TrimPrefix(wwwAuthenticate, "Bearer ")
	parts := strings.Split(authStr, ",")

	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			key := kv[0]
			value := strings.Trim(kv[1], "\"")
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", ErrAuthFailed.WithCause(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", ErrAuthFailed.WithCause(fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body)))
	}

	var authResp authResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", ErrAuthFailed.WithCause(err)
	}

	token := authResp.Token
	if token == "" {
		token = authResp.AccessToken
	}

	return token, nil
}

func (c *registryClient) GetManifest(ctx context.Context, imageRef string) (*Manifest, error) {
	logger.Info("Fetching manifest for image: %s", imageRef)

	registry, repository, tag, err := parseImageRef(imageRef)
	if err != nil {
		return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	// Construct OCI registry API URL
	scheme := getScheme(registry)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repository, tag)

	logger.Debug("Manifest URL: %s", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	// Set accept header for OCI manifest
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	// Also accept Docker manifest v2
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	// Accept OCI index for multi-platform images
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")

	// Add Basic Auth if credentials are provided
	if c.username != "" && c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	logger.Debug("Sending HTTP request: GET %s", url)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		logger.Error("HTTP request failed: %v", err)
		return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}
	defer resp.Body.Close()

	logger.Debug("Received HTTP response: %d %s", resp.StatusCode, resp.Status)

	var token string
	// Handle 401 with token auth
	if resp.StatusCode == http.StatusUnauthorized {
		logger.Info("Authentication required, fetching token...")
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		if wwwAuth == "" {
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(fmt.Errorf("got 401 but no WWW-Authenticate header"))
		}

		logger.Debug("WWW-Authenticate: %s", wwwAuth)

		token, err = c.getAuthToken(ctx, registry, repository, wwwAuth)
		if err != nil {
			logger.Error("Failed to get auth token: %v", err)
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}

		logger.Info("Successfully obtained auth token")

		// Retry with token
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
		req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
		req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")
		req.Header.Set("Authorization", "Bearer "+token)

		// Add Basic Auth if credentials are provided (some registries may need both)
		if c.username != "" && c.password != "" {
			req.SetBasicAuth(c.username, c.password)
		}

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(body)))
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
	}

	// If it's an OCI index, fetch the first manifest
	// Check for manifests field instead of mediaType (some registries don't return mediaType)
	if len(manifest.Manifests) > 0 {
		// Use the first manifest (usually linux/amd64)
		manifestDigest := manifest.Manifests[0].Digest

		// Fetch the actual manifest by digest
		url = fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, repository, manifestDigest)
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
		req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")

		// Use token if we have one
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		// Add Basic Auth if credentials are provided
		if c.username != "" && c.password != "" {
			req.SetBasicAuth(c.username, c.password)
		}

		resp2, err := c.httpClient.Do(req)
		if err != nil {
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp2.Body)
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(fmt.Errorf("manifest request returned %d: %s", resp2.StatusCode, string(body)))
		}

		if err := json.NewDecoder(resp2.Body).Decode(&manifest); err != nil {
			return nil, ErrManifestFetch.WithDetail("imageRef", imageRef).WithCause(err)
		}
	}

	return &manifest, nil
}
