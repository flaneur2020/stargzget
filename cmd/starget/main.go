package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/flaneur2020/stargz-get/stargzget"
	"github.com/flaneur2020/stargz-get/stargzget/logger"
	"github.com/opencontainers/go-digest"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var (
	credential  string
	noProgress  bool
	concurrency int
	verbose     bool
	debug       bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "starget",
		Short: "A CLI tool for working with stargz container images",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Set log level based on flags
			if debug {
				logger.SetLogLevel(logger.LogLevelDebug)
			} else if verbose {
				logger.SetLogLevel(logger.LogLevelInfo)
			} else {
				logger.SetLogLevel(logger.LogLevelError)
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&credential, "credential", "", "Registry credential in format USER:PASSWORD")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose logging (INFO level)")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging (DEBUG level)")

	// info command
	infoCmd := &cobra.Command{
		Use:   "info <REGISTRY>/<IMAGE>:<TAG>",
		Short: "List all layers in an image",
		Args:  cobra.ExactArgs(1),
		Run:   runInfo,
	}

	// ls command
	lsCmd := &cobra.Command{
		Use:   "ls <REGISTRY>/<IMAGE>:<TAG> [BLOB]",
		Short: "List files in a blob (or all files if blob is not specified)",
		Args:  cobra.RangeArgs(1, 2),
		Run:   runLs,
	}

	// get command
	getCmd := &cobra.Command{
		Use:   "get <REGISTRY>/<IMAGE>:<TAG> [BLOB] <PATH> [OUTPUT_DIR]",
		Short: "Download file or directory. BLOB is optional (uses top layer if not specified). Use '.' or '/' for all files",
		Args:  cobra.RangeArgs(2, 4),
		Run:   runGet,
	}
	getCmd.Flags().BoolVar(&noProgress, "no-progress", false, "Disable progress bar (progress is enabled by default)")
	getCmd.Flags().IntVar(&concurrency, "concurrency", 4, "Number of concurrent workers (default: 4, set to 1 for sequential)")

	rootCmd.AddCommand(infoCmd, lsCmd, getCmd)

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

func parseCredential(cred string) (string, string, error) {
	parts := strings.SplitN(cred, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid credential format, expected USER:PASSWORD")
	}
	return parts[0], parts[1], nil
}

func runInfo(cmd *cobra.Command, args []string) {
	imageRef := args[0]

	client := stargzget.NewRegistryClient()

	// Apply credentials if provided
	if credential != "" {
		username, password, err := parseCredential(credential)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing credential: %v\n", err)
			os.Exit(1)
		}
		client = client.WithCredential(username, password)
	}

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
	var blobDigest string
	if len(args) > 1 {
		blobDigest = args[1]
	}

	registry, repository, err := parseImageRef(imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get manifest first
	registryClient := stargzget.NewRegistryClient()

	// Apply credentials if provided
	if credential != "" {
		username, password, err := parseCredential(credential)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing credential: %v\n", err)
			os.Exit(1)
		}
		registryClient = registryClient.WithCredential(username, password)
	}

	manifest, err := registryClient.GetManifest(context.Background(), imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting manifest: %v\n", err)
		os.Exit(1)
	}

	storage := registryClient.NewStorage(registry, repository, manifest)
	resolver := stargzget.NewChunkResolver(storage)
	loader := stargzget.NewImageIndexLoader(storage, resolver)

	index, err := loader.Load(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting image index: %v\n", err)
		os.Exit(1)
	}

	// If blob digest is provided, list files in that specific blob
	if blobDigest != "" {
		dgst, err := digest.Parse(blobDigest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing digest: %v\n", err)
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
	} else {
		// No blob digest provided - list all files from all layers (later layers override earlier ones)
		fmt.Printf("All files in %s:\n", imageRef)
		for _, path := range index.AllFiles() {
			fmt.Println(path)
		}
	}
}

func runGet(cmd *cobra.Command, args []string) {
	imageRef := args[0]

	// Parse arguments based on count and whether second arg looks like a digest
	var blobDigest string
	var pathPattern string
	var outputDir string = "."

	// Determine if second argument is a blob digest (starts with sha256: or sha512:)
	hasBlob := len(args) >= 3 && strings.HasPrefix(args[1], "sha")

	if hasBlob {
		// args: imageRef, blob, path, [outputDir]
		blobDigest = args[1]
		pathPattern = args[2]
		if len(args) > 3 {
			outputDir = args[3]
		}
	} else {
		// args: imageRef, path, [outputDir]
		pathPattern = args[1]
		if len(args) > 2 {
			outputDir = args[2]
		}
	}

	ctx := context.Background()

	registry, repository, err := parseImageRef(imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get manifest first
	registryClient := stargzget.NewRegistryClient()

	// Apply credentials if provided
	if credential != "" {
		username, password, err := parseCredential(credential)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing credential: %v\n", err)
			os.Exit(1)
		}
		registryClient = registryClient.WithCredential(username, password)
	}

	manifest, err := registryClient.GetManifest(ctx, imageRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting manifest: %v\n", err)
		os.Exit(1)
	}

	storage := registryClient.NewStorage(registry, repository, manifest)
	resolver := stargzget.NewChunkResolver(storage)
	loader := stargzget.NewImageIndexLoader(storage, resolver)
	downloader := stargzget.NewDownloader(resolver)

	// Parse blob digest if provided
	var dgst digest.Digest
	if blobDigest != "" {
		var err error
		dgst, err = digest.Parse(blobDigest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing digest: %v\n", err)
			os.Exit(1)
		}
	}
	// If blobDigest is empty, dgst will be zero value and FilterFiles will use all layers

	// Get image index
	index, err := loader.Load(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting image index: %v\n", err)
		os.Exit(1)
	}

	// Normalize path pattern
	if pathPattern == "*" {
		pathPattern = "."
	}

	// Filter files based on pattern and blob digest (empty digest means search all layers)
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
	var statusCallback stargzget.StatusCallback
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

		// Status callback to update progress bar description with active files
		statusCallback = func(activeFiles []string, completedFiles, totalFiles int) {
			if bar == nil {
				return
			}

			if len(activeFiles) == 0 {
				// No active files, show completion status
				bar.Describe(fmt.Sprintf("Completed %d/%d files", completedFiles, totalFiles))
			} else if len(jobs) == 1 {
				// Single file download - keep original description
				return
			} else {
				// Multiple files - show active files (up to 3)
				displayFiles := activeFiles
				if len(displayFiles) > 3 {
					displayFiles = displayFiles[:3]
				}

				// Shorten file paths for display (show only basename)
				shortNames := make([]string, len(displayFiles))
				for i, f := range displayFiles {
					shortNames[i] = filepath.Base(f)
				}

				desc := fmt.Sprintf("Downloading %s... (%d/%d files)",
					strings.Join(shortNames, ", "),
					completedFiles,
					totalFiles)
				bar.Describe(desc)
			}
		}
	}

	// Start download with custom options
	opts := &stargzget.DownloadOptions{
		MaxRetries:  3,
		Concurrency: concurrency,
		OnStatus:    statusCallback,
	}
	stats, err := downloader.StartDownload(ctx, jobs, progressCallback, opts)
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
