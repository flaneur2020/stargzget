/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/log"
	"github.com/containerd/stargz-snapshotter/estargz"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"
)

const (
	defaultRegistry = "docker.io"
)

func main() {
	app := &cli.App{
		Name:  "stargz-downloader",
		Usage: "Download files from a stargz image to a local directory",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "image",
				Aliases:  []string{"i"},
				Usage:    "Image reference (e.g., ubuntu:latest or docker.io/library/ubuntu:latest)",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "output",
				Aliases:  []string{"o"},
				Usage:    "Output directory to download files",
				Required: true,
			},
			&cli.StringFlag{
				Name:    "username",
				Aliases: []string{"u"},
				Usage:   "Registry username for authentication",
			},
			&cli.StringFlag{
				Name:    "password",
				Aliases: []string{"p"},
				Usage:   "Registry password for authentication",
			},
			&cli.BoolFlag{
				Name:  "insecure",
				Usage: "Allow insecure connections to registry",
			},
			&cli.BoolFlag{
				Name:  "plain-http",
				Usage: "Use plain HTTP instead of HTTPS",
			},
			&cli.IntFlag{
				Name:  "concurrency",
				Usage: "Number of concurrent file downloads",
				Value: 10,
			},
			&cli.StringFlag{
				Name:    "file",
				Aliases: []string{"f"},
				Usage:   "Download a specific file from the layer (e.g., /usr/bin/bash). If specified, requires --layer flag.",
			},
			&cli.StringFlag{
				Name:    "layer",
				Aliases: []string{"l"},
				Usage:   "Layer digest to download from (e.g., sha256:abc123...). Required when --file is specified.",
			},
		},
		Action: run,
	}

	if err := app.Run(os.Args); err != nil {
		log.L.WithError(err).Fatal("failed to run stargz-downloader")
	}
}

func run(cliCtx *cli.Context) error {
	ctx := context.Background()

	imageRef := cliCtx.String("image")
	outputDir := cliCtx.String("output")
	username := cliCtx.String("username")
	password := cliCtx.String("password")
	insecure := cliCtx.Bool("insecure")
	plainHTTP := cliCtx.Bool("plain-http")
	concurrency := cliCtx.Int("concurrency")
	fileName := cliCtx.String("file")
	layerDigestStr := cliCtx.String("layer")

	// Validate single file download parameters
	if fileName != "" && layerDigestStr == "" {
		return fmt.Errorf("--layer flag is required when --file is specified")
	}
	if fileName == "" && layerDigestStr != "" {
		return fmt.Errorf("--file flag is required when --layer is specified")
	}

	// Parse image reference
	refSpec, err := reference.Parse(imageRef)
	if err != nil {
		return fmt.Errorf("failed to parse image reference: %w", err)
	}

	// Normalize reference (add default registry and library if needed)
	refSpec = normalizeReference(refSpec)

	fmt.Printf("Downloading image: %s\n", refSpec.String())
	fmt.Printf("Output directory: %s\n", outputDir)
	fmt.Printf("Concurrency: %d\n", concurrency)

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Setup docker resolver with authentication
	resolverOpts := docker.ResolverOptions{
		PlainHTTP: plainHTTP,
	}

	if insecure {
		resolverOpts.Client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
	}

	if username != "" && password != "" {
		resolverOpts.Authorizer = docker.NewDockerAuthorizer(
			docker.WithAuthCreds(func(host string) (string, string, error) {
				return username, password, nil
			}),
		)
	}

	resolver := docker.NewResolver(resolverOpts)

	// Fetch manifest
	_, desc, err := resolver.Resolve(ctx, refSpec.String())
	if err != nil {
		return fmt.Errorf("failed to resolve image: %w", err)
	}

	fetcher, err := resolver.Fetcher(ctx, refSpec.String())
	if err != nil {
		return fmt.Errorf("failed to create fetcher: %w", err)
	}

	// Check if single file download mode
	if fileName != "" {
		return downloadSingleFile(ctx, fetcher, refSpec, username, password, insecure, plainHTTP, layerDigestStr, fileName, outputDir)
	}

	// Otherwise, download entire image (all layers)

	// Fetch manifest content
	manifestReader, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest: %w", err)
	}
	defer manifestReader.Close()

	// Parse manifest
	var manifest ocispec.Manifest
	if err := parseJSON(manifestReader, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	fmt.Printf("Found %d layers\n", len(manifest.Layers))

	// Download and extract each layer
	for i, layer := range manifest.Layers {
		fmt.Printf("Processing layer %d/%d (digest: %s)\n", i+1, len(manifest.Layers), layer.Digest.String()[:12])

		if err := downloadLayer(ctx, fetcher, layer, outputDir, concurrency); err != nil {
			return fmt.Errorf("failed to download layer %s: %w", layer.Digest, err)
		}
	}

	fmt.Printf("Successfully downloaded all files to %s\n", outputDir)
	return nil
}

func downloadLayer(ctx context.Context, fetcher remotes.Fetcher, desc ocispec.Descriptor, outputDir string, concurrency int) error {
	// Fetch layer blob
	layerReader, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return fmt.Errorf("failed to fetch layer blob: %w", err)
	}
	defer layerReader.Close()

	// Try to open as stargz/estargz
	// First, we need to convert the reader to io.ReaderAt
	// For simplicity, we'll read the entire layer into memory or a temp file
	// In production, you might want to use a more efficient approach

	tempFile, err := os.CreateTemp("", "layer-*.tar.gz")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Copy layer data to temp file
	fmt.Printf("  Downloading layer blob...\n")
	if _, err := io.Copy(tempFile, layerReader); err != nil {
		return fmt.Errorf("failed to copy layer data: %w", err)
	}

	// Get file info for size
	stat, err := tempFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat temp file: %w", err)
	}

	// Open as estargz reader
	reader, err := estargz.Open(io.NewSectionReader(tempFile, 0, stat.Size()))
	if err != nil {
		// If it's not a valid estargz, it might be a regular tar.gz
		// Try to handle it as a regular tar archive
		return fmt.Errorf("failed to open layer as estargz (might not be a stargz layer): %w", err)
	}

	// Get root entry
	root, ok := reader.Lookup("")
	if !ok {
		return fmt.Errorf("failed to get root entry")
	}

	// Collect all entries first
	var entries []*entryTask
	collectEntries(root, "", &entries)
	fmt.Printf("  Found %d entries to extract\n", len(entries))

	// Create a semaphore to limit concurrency
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var extractErr error

	for _, task := range entries {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(t *entryTask) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			if err := extractEntry(reader, t, outputDir); err != nil {
				mu.Lock()
				if extractErr == nil {
					extractErr = err
				}
				mu.Unlock()
			}
		}(task)
	}

	wg.Wait()

	if extractErr != nil {
		return fmt.Errorf("failed to extract entries: %w", extractErr)
	}

	return nil
}

type entryTask struct {
	entry *estargz.TOCEntry
	path  string
}

func collectEntries(entry *estargz.TOCEntry, currentPath string, entries *[]*entryTask) {
	if entry == nil {
		return
	}

	// Add current entry if it's not the root
	if currentPath != "" {
		*entries = append(*entries, &entryTask{
			entry: entry,
			path:  currentPath,
		})
	}

	// Recursively collect children
	entry.ForeachChild(func(baseName string, childEntry *estargz.TOCEntry) bool {
		childPath := filepath.Join(currentPath, baseName)
		collectEntries(childEntry, childPath, entries)
		return true
	})
}

func extractEntry(reader *estargz.Reader, task *entryTask, outputDir string) error {
	entry := task.entry
	entryPath := task.path
	fullPath := filepath.Join(outputDir, entryPath)

	switch entry.Type {
	case "dir":
		// Create directory
		if err := os.MkdirAll(fullPath, os.FileMode(entry.Mode)); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", fullPath, err)
		}
		fmt.Printf("  Created directory: %s\n", entryPath)

	case "reg":
		// Create regular file
		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", fullPath, err)
		}

		// Open file for writing
		outFile, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(entry.Mode))
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", fullPath, err)
		}

		// Get reader for this entry
		entryReader, err := reader.OpenFile(entry.Name)
		if err != nil {
			outFile.Close()
			return fmt.Errorf("failed to open entry %s: %w", entry.Name, err)
		}

		// Copy file content
		if _, err := io.Copy(outFile, entryReader); err != nil {
			outFile.Close()
			return fmt.Errorf("failed to write file %s: %w", fullPath, err)
		}

		outFile.Close()
		fmt.Printf("  Downloaded file: %s (%d bytes)\n", entryPath, entry.Size)

	case "symlink":
		// Create symlink
		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for symlink %s: %w", fullPath, err)
		}

		// Remove existing file/symlink if it exists
		os.Remove(fullPath)

		if err := os.Symlink(entry.LinkName, fullPath); err != nil {
			return fmt.Errorf("failed to create symlink %s -> %s: %w", fullPath, entry.LinkName, err)
		}
		fmt.Printf("  Created symlink: %s -> %s\n", entryPath, entry.LinkName)

	case "hardlink":
		// Create hardlink
		linkTarget := filepath.Join(outputDir, strings.TrimPrefix(entry.LinkName, "./"))

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for hardlink %s: %w", fullPath, err)
		}

		// Remove existing file if it exists
		os.Remove(fullPath)

		if err := os.Link(linkTarget, fullPath); err != nil {
			// If hardlink fails, try to copy the file instead
			fmt.Printf("  Warning: hardlink failed, copying file instead: %s -> %s\n", entryPath, entry.LinkName)
			if err := copyFile(linkTarget, fullPath); err != nil {
				return fmt.Errorf("failed to copy file for hardlink %s: %w", fullPath, err)
			}
		} else {
			fmt.Printf("  Created hardlink: %s -> %s\n", entryPath, entry.LinkName)
		}

	default:
		// Skip other types (char, block, fifo, etc.)
		fmt.Printf("  Skipping entry with type %s: %s\n", entry.Type, entryPath)
	}

	return nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func normalizeReference(ref reference.Spec) reference.Spec {
	// Add default registry if not specified
	if ref.Hostname() == "" || !strings.Contains(ref.Hostname(), ".") {
		// This is likely a short name like "ubuntu:latest"
		// Normalize it to docker.io/library/ubuntu:latest
		ref.Locator = defaultRegistry + "/" + ref.Locator
	}

	// Add "library/" prefix for official images on docker.io
	if ref.Hostname() == defaultRegistry {
		parts := strings.Split(ref.Object, "/")
		if len(parts) == 1 {
			// Single part means it's an official image
			ref.Object = "library/" + ref.Object
		}
	}

	return ref
}

func parseJSON(r io.Reader, v interface{}) error {
	return json.NewDecoder(r).Decode(v)
}

// downloadSingleFile downloads a single file from a specific layer using on-demand downloading
func downloadSingleFile(ctx context.Context, fetcher remotes.Fetcher, refSpec reference.Spec, username, password string, insecure, plainHTTP bool, layerDigestStr, fileName, outputDir string) error {
	// Parse layer digest
	layerDigest, err := digest.Parse(layerDigestStr)
	if err != nil {
		return fmt.Errorf("failed to parse layer digest: %w", err)
	}

	fmt.Printf("\n=== Single File Download Mode ===\n")
	fmt.Printf("Image: %s\n", refSpec.String())
	fmt.Printf("Layer: %s\n", layerDigest.String())
	fmt.Printf("File: %s\n", fileName)
	fmt.Printf("Output: %s\n\n", outputDir)

	// Create HTTP client
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecure,
			},
			Proxy: http.ProxyFromEnvironment, // Support HTTP_PROXY, HTTPS_PROXY, ALL_PROXY
		},
		Timeout: 5 * time.Minute,
	}

	// Create registry client with Range support
	registryClient := newRegistryClient(httpClient, refSpec, username, password, insecure, plainHTTP)

	// Create download manager
	downloadManager := newDownloadManager(registryClient, outputDir)

	// Download the specific file
	targetPath := filepath.Join(outputDir, filepath.Base(fileName))
	if err := downloadManager.downloadFile(ctx, layerDigest, fileName, targetPath); err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	fmt.Printf("\n✓ Successfully downloaded file to: %s\n", targetPath)
	return nil
}
