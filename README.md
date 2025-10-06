# stargz-get

A lightweight CLI tool for downloading files from stargz (seekable tar.gz) container images without pulling the entire image.

## Features

- **Lazy Loading**: Download individual files or directories without pulling entire container images
- **Smart Filtering**: Use path patterns to select files (e.g., `bin/`, `bin/echo`, `.`)
- **Progress Tracking**: Real-time progress bars with download speed indicators
- **Automatic Retry**: Built-in retry logic (default: 3 retries) for failed downloads
- **Layer Inspection**: List and explore stargz image layers
- **Public Registry Support**: Works with public registries like ghcr.io

## Installation

```bash
go build -o starget ./cmd/starget
```

## Usage

### List Image Layers

```bash
starget layers <REGISTRY>/<IMAGE>:<TAG>
```

Example:
```bash
starget layers ghcr.io/stargz-containers/node:13.13.0-esgz
```

### List Files in a Blob

```bash
starget ls <REGISTRY>/<IMAGE>:<TAG> <BLOB_DIGEST>
```

Example:
```bash
starget ls ghcr.io/stargz-containers/node:13.13.0-esgz sha256:c411ef59488b73d06c19343d72eb816549577b3e0429516dcca5789d7a9a4000
```

### Download Files

```bash
starget get <REGISTRY>/<IMAGE>:<TAG> <BLOB_DIGEST> <PATH_PATTERN> [OUTPUT_DIR]
```

**Path Pattern Options:**
- Specific file: `bin/echo`
- Directory: `bin/` or `bin`
- All files: `.` or `/`

**Examples:**

Download a single file:
```bash
starget get ghcr.io/stargz-containers/node:13.13.0-esgz \
  sha256:c411ef59488b73d06c19343d72eb816549577b3e0429516dcca5789d7a9a4000 \
  bin/echo output/echo
```

Download a directory:
```bash
starget get ghcr.io/stargz-containers/node:13.13.0-esgz \
  sha256:c411ef59488b73d06c19343d72eb816549577b3e0429516dcca5789d7a9a4000 \
  bin/ output/
```

Download all files:
```bash
starget get ghcr.io/stargz-containers/node:13.13.0-esgz \
  sha256:c411ef59488b73d06c19343d72eb816549577b3e0429516dcca5789d7a9a4000 \
  . output/
```

### Flags

- `--no-progress`: Disable progress bar (progress is enabled by default)

## Architecture

### Core Components

#### `RegistryClient`
Handles OCI registry communication:
- Fetches image manifests
- Supports public registries

#### `ImageAccessor`
Manages stargz image access:
- **`ImageIndex()`**: Builds an index of all files across all layers
- **`OpenFile()`**: Opens a specific file for reading

Key features:
- Downloads only the stargz TOC (Table of Contents), not the entire blob
- Caches TOC for efficient repeated access
- Uses HTTP range requests for lazy loading

#### `ImageIndex`
Provides file lookup and filtering:
- **`FindFile(path, blobDigest)`**: Finds a specific file (blobDigest optional)
- **`FilterFiles(pattern, blobDigest)`**: Filters files by pattern and optional blob digest

#### `Downloader`
Unified download interface:
- **`StartDownload(jobs, progress, options)`**: Downloads multiple files with progress tracking
- **`DownloadJob`**: Represents a single file download task
- **`DownloadOptions`**: Configures retry behavior (default: 3 retries)

Features:
- Automatic retry on failure
- Progress tracking across all downloads
- Concurrent-safe progress reporting

## Development

### Running Tests

```bash
# Run all tests
go test ./stargzget -v

# Run specific test
go test ./stargzget -v -run TestImageIndex_FilterFiles
```

### Test Coverage

- `TestImageIndex_FilterFiles`: Tests file pattern matching and filtering
- `TestImageIndex_FindFile`: Tests file lookup with optional blob digest
- `TestDownloader_StartDownload`: Tests download workflow and progress tracking
- `TestDownloader_StartDownload_WithRetries`: Tests retry logic with simulated failures

## Project Status

### ✅ Completed Features

- [x] **Phase 1**: Registry Manifest Access
  - RegistryClient.GetManifest() for public registries
  - Manifest parsing and layer extraction

- [x] **Phase 2**: Stargz Index Parsing
  - TOC download and parsing using estargz library
  - File metadata extraction
  - ImageIndex for fast file lookup

- [x] **Phase 3**: File Download
  - HTTP range requests for lazy loading
  - Single file and directory downloads
  - Unified download interface with DownloadJob

- [x] **Phase 5**: CLI Interface
  - Cobra-based CLI with subcommands
  - Pattern-based file filtering
  - Progress bar integration

- [x] **Phase 8**: Error Handling & Retry
  - Automatic retry with configurable max retries
  - Graceful error handling
  - Failed file tracking in statistics

- [x] **Phase 9**: Developer Experience
  - Progress bar with download speed
  - `--no-progress` flag
  - Detailed download statistics
  - Help text for all commands

- [x] **Phase 10**: Testing
  - Comprehensive unit tests with mocks
  - Test coverage for core components
  - Retry logic validation

### 🚧 Future Enhancements

- [ ] **Authentication Support**: Private registry access with credentials
- [ ] **Multi-threaded Downloading**: Parallel chunk downloads for performance
- [ ] **Checksum Verification**: Verify downloaded file integrity
- [ ] **Exponential Backoff**: Smarter retry delays
- [ ] **Verbose Logging**: `--verbose` flag for debugging

## Design Philosophy

**stargz-get** is designed to be:
- **Simple**: Focused on one task - extracting files from stargz images
- **Lightweight**: No daemon, no complex dependencies
- **Efficient**: Download only what you need using lazy loading

## Non-goals

- Full OCI image management (use containerd/Docker for that)
- Support for non-stargz image formats
- Image building or pushing

## License

This is a learning/demonstration project.

## References

- [Stargz Snapshotter](https://github.com/containerd/stargz-snapshotter)
- [eStargz Specification](https://github.com/containerd/stargz-snapshotter/blob/main/docs/estargz.md)
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)
