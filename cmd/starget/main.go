package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/flaneur2020/stargz-get/stargzget"
	"github.com/opencontainers/go-digest"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var (
	credential string
	noProgress bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "starget",
		Short: "A CLI tool for working with stargz container images",
	}

	rootCmd.PersistentFlags().StringVar(&credential, "credential", "", "Registry credential in format USER:PASSWORD")

	// layers command
	layersCmd := &cobra.Command{
		Use:   "layers <REGISTRY>/<IMAGE>:<TAG>",
		Short: "List all layers in an image",
		Args:  cobra.ExactArgs(1),
		Run:   runLayers,
	}

	// ls command
	lsCmd := &cobra.Command{
		Use:   "ls <REGISTRY>/<IMAGE>:<TAG> <BLOB>",
		Short: "List files in a blob",
		Args:  cobra.ExactArgs(2),
		Run:   runLs,
	}

	// get command
	getCmd := &cobra.Command{
		Use:   "get <REGISTRY>/<IMAGE>:<TAG> <BLOB> <FILE_PATH> [OUTPUT_DIR]",
		Short: "Download file(s) from a blob. Use '.' or '*' as FILE_PATH to download all files",
		Args:  cobra.RangeArgs(3, 4),
		Run:   runGet,
	}
	getCmd.Flags().BoolVar(&noProgress, "no-progress", false, "Disable progress bar (progress is enabled by default)")

	rootCmd.AddCommand(layersCmd, lsCmd, getCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
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

func runLayers(cmd *cobra.Command, args []string) {
	imageRef := args[0]

	client := stargzget.NewRegistryClient()
	manifest, err := client.GetManifest(context.Background(), imageRef)
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

func runLs(cmd *cobra.Command, args []string) {
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

	files, err := blobAccessor.ListFiles(context.Background(), dgst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Files in blob %s:\n", blobDigest)
	for _, file := range files {
		fmt.Println(file)
	}
}

func runGet(cmd *cobra.Command, args []string) {
	imageRef := args[0]
	blobDigest := args[1]
	filePath := args[2]

	outputPath := filePath
	if len(args) > 3 {
		outputPath = args[3]
	}

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

	// Check if downloading all files
	downloadAll := filePath == "." || filePath == "*"

	if downloadAll {
		runGetAll(blobAccessor, downloader, dgst, outputPath)
	} else {
		runGetSingle(blobAccessor, downloader, dgst, filePath, outputPath)
	}
}

func runGetSingle(blobAccessor stargzget.BlobAccessor, downloader stargzget.Downloader, dgst digest.Digest, filePath, outputPath string) {
	ctx := context.Background()

	// Get file metadata first to know the size
	metadata, err := blobAccessor.GetFileMetadata(ctx, dgst, filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting file metadata: %v\n", err)
		os.Exit(1)
	}

	// Progress bar is enabled by default
	showProgress := !noProgress

	var progressCallback stargzget.ProgressCallback
	if showProgress {
		// Create progress bar
		bar := progressbar.DefaultBytes(
			metadata.Size,
			fmt.Sprintf("Downloading %s", filePath),
		)
		progressCallback = func(current, total int64) {
			bar.Set64(current)
		}
	} else {
		// Simple log
		fmt.Printf("Downloading %s (%d bytes)...\n", filePath, metadata.Size)
	}

	// Download with progress callback
	err = downloader.DownloadFile(ctx, dgst, filePath, outputPath, progressCallback)

	if err != nil {
		if showProgress {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}

	if showProgress {
		fmt.Printf("\nSuccessfully downloaded %s (%d bytes)\n", filePath, metadata.Size)
	} else {
		fmt.Printf("Successfully downloaded %s (%d bytes)\n", filePath, metadata.Size)
	}
}

func runGetAll(blobAccessor stargzget.BlobAccessor, downloader stargzget.Downloader, dgst digest.Digest, outputDir string) {
	ctx := context.Background()

	// Progress bar is enabled by default
	showProgress := !noProgress

	var bar *progressbar.ProgressBar
	var progressCallback stargzget.ProgressCallback
	var totalSizeKnown bool
	var initOnce bool

	if showProgress {
		// Create a wrapper callback that initializes the progress bar once we know the total size
		progressCallback = func(current, total int64) {
			if !initOnce && total > 0 {
				bar = progressbar.DefaultBytes(total, "Downloading all files")
				initOnce = true
				totalSizeKnown = true
			}
			if bar != nil {
				bar.Set64(current)
			}
		}
	}

	stats, err := downloader.DownloadAll(ctx, dgst, outputDir, progressCallback)
	if err != nil {
		if showProgress {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}

	if stats.TotalFiles == 0 {
		fmt.Println("No files to download")
		return
	}

	if showProgress {
		if !totalSizeKnown {
			fmt.Printf("\nDownloaded %d files (%d bytes total)\n", stats.DownloadedFiles, stats.DownloadedBytes)
		} else {
			fmt.Printf("\nSuccessfully downloaded %d/%d files (%d bytes total)\n", stats.DownloadedFiles, stats.TotalFiles, stats.DownloadedBytes)
		}
	} else {
		fmt.Printf("Successfully downloaded %d/%d files (%d bytes total)\n", stats.DownloadedFiles, stats.TotalFiles, stats.DownloadedBytes)
	}
}
