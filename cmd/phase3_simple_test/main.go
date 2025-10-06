package main

import (
	"context"
	"fmt"
	"log"
	"os"

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

	// Use the first layer (smaller, basic filesystem)
	if len(manifest.Layers) == 0 {
		log.Fatal("No layers found")
	}

	firstLayer := manifest.Layers[0]
	fmt.Printf("Using first layer: %s\n\n", firstLayer.Digest)

	// Create BlobAccessor
	blobAccessor := stargzget.NewBlobAccessor(client, "ghcr.io", "stargz-containers/node")

	blobDigest, err := digest.Parse(firstLayer.Digest)
	if err != nil {
		log.Fatalf("Failed to parse digest: %v", err)
	}

	// Create Downloader
	downloader := stargzget.NewDownloader(blobAccessor)

	// Download a small file: bin/echo
	targetFile := "./output/bin/echo"
	fmt.Printf("Downloading bin/echo to %s...\n", targetFile)

	err = downloader.DownloadFile(context.Background(), blobDigest, "bin/echo", targetFile, nil)
	if err != nil {
		log.Fatalf("Failed to download file: %v", err)
	}

	// Verify file size
	fileInfo, err := os.Stat(targetFile)
	if err != nil {
		log.Fatalf("Failed to stat file: %v", err)
	}

	fmt.Printf("\nFile downloaded successfully!\n")
	fmt.Printf("File size: %d bytes (%.2f KB)\n", fileInfo.Size(), float64(fileInfo.Size())/1024)

	// Check if it's executable
	fmt.Printf("File permissions: %s\n", fileInfo.Mode())
}
