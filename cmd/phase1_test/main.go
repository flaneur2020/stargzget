package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/flaneur2020/stargz-get/stargzget"
)

func main() {
	client := stargzget.NewRegistryClient()

	imageRef := "ghcr.io/stargz-containers/node:13.13.0-esgz"
	fmt.Printf("Fetching manifest for: %s\n\n", imageRef)

	manifest, err := client.GetManifest(context.Background(), imageRef)
	if err != nil {
		log.Fatalf("Failed to get manifest: %v", err)
	}

	// Debug: print raw JSON
	jsonData, _ := json.MarshalIndent(manifest, "", "  ")
	fmt.Printf("Raw manifest JSON:\n%s\n\n", string(jsonData))

	fmt.Printf("Manifest Schema Version: %d\n", manifest.SchemaVersion)
	fmt.Printf("Media Type: %s\n\n", manifest.MediaType)

	fmt.Printf("Total layers: %d\n\n", len(manifest.Layers))

	// Print all layers with their media types
	fmt.Println("All layers:")
	for i, layer := range manifest.Layers {
		stargzMarker := ""
		if layer.IsStargz() {
			stargzMarker = " [STARGZ]"
		}
		fmt.Printf("  [%d] %s%s\n", i, layer.Digest, stargzMarker)
		fmt.Printf("      Type: %s\n", layer.MediaType)
		fmt.Printf("      Size: %d bytes\n", layer.Size)
	}

	// Filter and print only stargz layers
	fmt.Println("\nStargz layers only:")
	stargzCount := 0
	for i, layer := range manifest.Layers {
		if layer.IsStargz() {
			fmt.Printf("  [%d] %s\n", i, layer.Digest)
			fmt.Printf("      Size: %d bytes\n", layer.Size)
			stargzCount++
		}
	}

	fmt.Printf("\nTotal stargz layers: %d\n", stargzCount)
}
