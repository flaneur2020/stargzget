package stargzget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type RegistryClient interface {
	GetManifest(ctx context.Context, imageRef string) (*Manifest, error)
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
}

func NewRegistryClient() RegistryClient {
	return &registryClient{
		httpClient: &http.Client{},
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

type authResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func (c *registryClient) getAuthToken(ctx context.Context, registry, repository, wwwAuthenticate string) (string, error) {
	// Parse WWW-Authenticate header
	// Example: Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:stargz-containers/node:pull"

	if !strings.HasPrefix(wwwAuthenticate, "Bearer ") {
		return "", fmt.Errorf("unsupported auth scheme: %s", wwwAuthenticate)
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
		return "", fmt.Errorf("no realm in WWW-Authenticate header")
	}

	// Build token URL
	tokenURL := fmt.Sprintf("%s?service=%s&scope=%s", realm, service, scope)

	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var authResp authResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	token := authResp.Token
	if token == "" {
		token = authResp.AccessToken
	}

	return token, nil
}

func (c *registryClient) GetManifest(ctx context.Context, imageRef string) (*Manifest, error) {
	registry, repository, tag, err := parseImageRef(imageRef)
	if err != nil {
		return nil, err
	}

	// Construct OCI registry API URL
	url := fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, repository, tag)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set accept header for OCI manifest
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	// Also accept Docker manifest v2
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	// Accept OCI index for multi-platform images
	req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	var token string
	// Handle 401 with token auth
	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		if wwwAuth == "" {
			return nil, fmt.Errorf("got 401 but no WWW-Authenticate header")
		}

		token, err = c.getAuthToken(ctx, registry, repository, wwwAuth)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth token: %w", err)
		}

		// Retry with token
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
		req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
		req.Header.Add("Accept", "application/vnd.oci.image.index.v1+json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch manifest with token: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(body))
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to decode manifest: %w", err)
	}

	// If it's an OCI index, fetch the first manifest
	// Check for manifests field instead of mediaType (some registries don't return mediaType)
	if len(manifest.Manifests) > 0 {
		// Use the first manifest (usually linux/amd64)
		manifestDigest := manifest.Manifests[0].Digest

		// Fetch the actual manifest by digest
		url = fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, repository, manifestDigest)
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create manifest request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
		req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")

		// Use token if we have one
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp2, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch manifest by digest: %w", err)
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp2.Body)
			return nil, fmt.Errorf("manifest request returned %d: %s", resp2.StatusCode, string(body))
		}

		if err := json.NewDecoder(resp2.Body).Decode(&manifest); err != nil {
			return nil, fmt.Errorf("failed to decode manifest from index: %w", err)
		}
	}

	return &manifest, nil
}
