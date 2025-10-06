package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		Use:   "get <REGISTRY>/<IMAGE>:<TAG> <BLOB> <PATH> [OUTPUT_DIR]",
		Short: "Download file or directory from a blob. Use '.' or '/' for all files, or specify a directory path",
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

	// Get manifest first
	registryClient := stargzget.NewRegistryClient()
	manifest, err := registryClient.GetManifest(context.Background(), imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting manifest: %v\n", err)
		os.Exit(1)
	}

	// Create image accessor
	imageAccessor := stargzget.NewImageAccessor(registryClient, registry, repository, manifest)

	dgst, err := digest.Parse(blobDigest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing digest: %v\n", err)
		os.Exit(1)
	}

	// Get image index
	index, err := imageAccessor.ImageIndex(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting image index: %v\n", err)
		os.Exit(1)
	}

	// Find the layer with the specified blob digest
	var files []string
	for _, layer := range index.Layers {
		if layer.BlobDigest == dgst {
			files = layer.Files
			break
		}
	}

	if files == nil {
		fmt.Fprintf(os.Stderr, "Blob not found: %s\n", blobDigest)
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
	pathPattern := args[2]

	outputDir := "."
	if len(args) > 3 {
		outputDir = args[3]
	}

	ctx := context.Background()

	registry, repository, err := parseImageRef(imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get manifest first
	registryClient := stargzget.NewRegistryClient()
	manifest, err := registryClient.GetManifest(ctx, imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting manifest: %v\n", err)
		os.Exit(1)
	}

	// Create image accessor and downloader
	imageAccessor := stargzget.NewImageAccessor(registryClient, registry, repository, manifest)
	downloader := stargzget.NewDownloader(imageAccessor)

	// Parse blob digest
	dgst, err := digest.Parse(blobDigest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing digest: %v\n", err)
		os.Exit(1)
	}

	// Get image index
	index, err := imageAccessor.ImageIndex(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting image index: %v\n", err)
		os.Exit(1)
	}

	// Normalize path pattern
	if pathPattern == "*" {
		pathPattern = "."
	}

	// Filter files based on pattern and blob digest
	matchedFiles := index.FilterFiles(pathPattern, dgst)
	if len(matchedFiles) == 0 {
		fmt.Fprintf(os.Stderr, "No files matched pattern: %s\n", pathPattern)
		os.Exit(1)
	}

	// Create download jobs
	var jobs []*stargzget.DownloadJob
	for _, fileInfo := range matchedFiles {
		// Determine output path
		var outputPath string
		if len(matchedFiles) == 1 && !strings.HasSuffix(pathPattern, "/") && pathPattern != "." && pathPattern != "/" {
			// Single file download - use outputDir as the file path directly
			outputPath = outputDir
		} else {
			// Multiple files or directory download - maintain directory structure
			cleanPath := filepath.Clean(fileInfo.Path)
			outputPath = filepath.Join(outputDir, cleanPath)
		}

		jobs = append(jobs, &stargzget.DownloadJob{
			Path:       fileInfo.Path,
			BlobDigest: fileInfo.BlobDigest,
			Size:       fileInfo.Size,
			OutputPath: outputPath,
		})
	}

	// Progress bar is enabled by default
	showProgress := !noProgress

	var progressCallback stargzget.ProgressCallback
	var bar *progressbar.ProgressBar
	var initOnce bool

	if showProgress {
		// Create a wrapper callback that initializes the progress bar once we know the total size
		progressCallback = func(current, total int64) {
			if !initOnce && total > 0 {
				if len(jobs) == 1 {
					bar = progressbar.DefaultBytes(total, fmt.Sprintf("Downloading %s", jobs[0].Path))
				} else {
					bar = progressbar.DefaultBytes(total, fmt.Sprintf("Downloading %d files", len(jobs)))
				}
				initOnce = true
			}
			if bar != nil {
				bar.Set64(current)
			}
		}
	}

	// Start download with default options (3 retries)
	stats, err := downloader.StartDownload(ctx, jobs, progressCallback, nil)
	if err != nil {
		if showProgress {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}

	// Print results
	if showProgress && bar != nil {
		fmt.Printf("\nSuccessfully downloaded %d/%d files (%d bytes total)",
			stats.DownloadedFiles, stats.TotalFiles, stats.DownloadedBytes)
		if stats.FailedFiles > 0 {
			fmt.Printf(" (%d failed)", stats.FailedFiles)
		}
		if stats.Retries > 0 {
			fmt.Printf(" (%d retries)", stats.Retries)
		}
		fmt.Println()
	} else {
		fmt.Printf("Successfully downloaded %d/%d files (%d bytes total)",
			stats.DownloadedFiles, stats.TotalFiles, stats.DownloadedBytes)
		if stats.FailedFiles > 0 {
			fmt.Printf(" (%d failed)", stats.FailedFiles)
		}
		if stats.Retries > 0 {
			fmt.Printf(" (%d retries)", stats.Retries)
		}
		fmt.Println()
	}
}
