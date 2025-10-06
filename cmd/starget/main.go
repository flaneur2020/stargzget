package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/flaneur2020/stargz-get/stargzget"
	"github.com/opencontainers/go-digest"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	ctx := context.Background()

	switch subcommand {
	case "layers":
		handleLayers(ctx, os.Args[2:])
	case "ls":
		handleLs(ctx, os.Args[2:])
	case "get":
		handleGet(ctx, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  starget layers <REGISTRY>/<IMAGE>:<TAG> [--credential=<USER:PASSWORD>]")
	fmt.Println("  starget ls <REGISTRY>/<IMAGE>:<TAG> <BLOB> [--credential=<USER:PASSWORD>]")
	fmt.Println("  starget get <REGISTRY>/<IMAGE>:<TAG> <BLOB> <PATH> [--credential=<USER:PASSWORD>]")
}

func parseCredential(args []string) (string, []string) {
	for i, arg := range args {
		if strings.HasPrefix(arg, "--credential=") {
			cred := strings.TrimPrefix(arg, "--credential=")
			// Remove this arg from the slice
			newArgs := append(args[:i], args[i+1:]...)
			return cred, newArgs
		}
	}
	return "", args
}

func parseImageRef(imageRef string) (string, string, error) {
	parts := strings.SplitN(imageRef, "/", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid image ref: %s", imageRef)
	}

	registry := parts[0]
	rest := parts[1]

	repoParts := strings.Split(rest, ":")
	if len(repoParts) < 2 {
		return "", "", fmt.Errorf("missing tag in image ref: %s", imageRef)
	}

	repository := strings.Join(repoParts[:len(repoParts)-1], ":")

	return registry, repository, nil
}

func handleLayers(ctx context.Context, args []string) {
	cred, args := parseCredential(args)
	_ = cred // TODO: use credential for authentication

	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: missing image reference")
		printUsage()
		os.Exit(1)
	}

	imageRef := args[0]

	client := stargzget.NewRegistryClient()
	manifest, err := client.GetManifest(ctx, imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Layers for %s:\n", imageRef)
	for i, layer := range manifest.Layers {
		fmt.Printf("%d: %s (size: %d bytes, type: %s)\n",
			i, layer.Digest, layer.Size, layer.MediaType)
	}
}

func handleLs(ctx context.Context, args []string) {
	cred, args := parseCredential(args)
	_ = cred // TODO: use credential for authentication

	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Error: missing image reference or blob digest")
		printUsage()
		os.Exit(1)
	}

	imageRef := args[0]
	blobDigest := args[1]

	registry, repository, err := parseImageRef(imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	registryClient := stargzget.NewRegistryClient()
	blobAccessor := stargzget.NewBlobAccessor(registryClient, registry, repository)

	dgst, err := digest.Parse(blobDigest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing digest: %v\n", err)
		os.Exit(1)
	}

	files, err := blobAccessor.ListFiles(ctx, dgst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Files in blob %s:\n", blobDigest)
	for _, file := range files {
		fmt.Println(file)
	}
}

func handleGet(ctx context.Context, args []string) {
	cred, args := parseCredential(args)
	_ = cred // TODO: use credential for authentication

	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Error: missing arguments")
		printUsage()
		os.Exit(1)
	}

	imageRef := args[0]
	blobDigest := args[1]
	filePath := args[2]

	registry, repository, err := parseImageRef(imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	registryClient := stargzget.NewRegistryClient()
	blobAccessor := stargzget.NewBlobAccessor(registryClient, registry, repository)
	downloader := stargzget.NewDownloader(blobAccessor)

	dgst, err := digest.Parse(blobDigest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing digest: %v\n", err)
		os.Exit(1)
	}

	// Extract file name from path for output
	outputPath := filePath
	if len(args) > 3 {
		outputPath = args[3]
	}

	err = downloader.DownloadFile(ctx, dgst, filePath, outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
