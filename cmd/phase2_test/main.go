package main

import (
	"context"
	"fmt"
	"log"

	"github.com/flaneur2020/stargz-get/stargzget"
	"github.com/opencontainers/go-digest"
)

func main() {
	client := stargzget.NewRegistryClient()

	imageRef := "ghcr.io/stargz-containers/node:13.13.0-esgz"
	fmt.Printf("Fetching manifest for: %s\n\n", imageRef)

	manifest, err := client.GetManifest(context.Background(), imageRef)
	if err != nil {
		log.Fatalf("Failed to get manifest: %v", err)
	}

	// Use the first layer
	if len(manifest.Layers) == 0 {
		log.Fatal("No layers found")
	}

	firstLayer := manifest.Layers[0]
	fmt.Printf("Using first layer: %s\n", firstLayer.Digest)
	fmt.Printf("Layer media type: %s\n", firstLayer.MediaType)
	fmt.Printf("Layer size: %d bytes (%.2f MB)\n\n", firstLayer.Size, float64(firstLayer.Size)/1024/1024)

	// Parse image ref to get registry and repository
	blobAccessor := stargzget.NewBlobAccessor(client, "ghcr.io", "stargz-containers/node")

	blobDigest, err := digest.Parse(firstLayer.Digest)
	if err != nil {
		log.Fatalf("Failed to parse digest: %v", err)
	}

	fmt.Println("Downloading TOC and listing files...")
	files, err := blobAccessor.ListFiles(context.Background(), blobDigest)
	if err != nil {
		log.Fatalf("Failed to list files: %v", err)
	}

	fmt.Printf("\nTotal files in layer: %d\n\n", len(files))

	fmt.Println("First 50 files:")
	for i, file := range files {
		if i >= 50 {
			break
		}
		fmt.Printf("  [%d] %s\n", i, file)
	}

	if len(files) > 50 {
		fmt.Printf("\n... and %d more files\n", len(files)-50)
	}
}
