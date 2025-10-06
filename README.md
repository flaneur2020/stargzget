# stargz-get

A lightweight CLI tool for downloading files from stargz (seekable tar.gz) container images without pulling & mount the image.

## Features

- **Lazy Loading**: Download individual files or directories without pulling entire container images
- **Smart Filtering**: Use path patterns to select files (e.g., `bin/`, `bin/echo`, `.`)
- **Progress Tracking**: Real-time progress bars with download speed indicators
- **Automatic Retry**: Built-in retry logic (default: 3 retries) for failed downloads
- **Layer Inspection**: List and explore stargz image layers
- **Public Registry Support**: Works with public registries like ghcr.io

## Quick Start

### Installation

```bash
go build -o starget ./cmd/starget
```

### Usage Examples

List image layers:
```bash
starget info ghcr.io/stargz-containers/node:13.13.0-esgz
```

List files in a specific layer:
```bash
starget ls ghcr.io/stargz-containers/node:13.13.0-esgz \
  sha256:c411ef59488b73d06c19343d72eb816549577b3e0429516dcca5789d7a9a4000
```

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

## Commands

### `starget info`

List all layers in an image.

```bash
starget info <REGISTRY>/<IMAGE>:<TAG>
```

### `starget ls`

List files in a specific blob/layer.

```bash
starget ls <REGISTRY>/<IMAGE>:<TAG> <BLOB_DIGEST>
```

### `starget get`

Download files from a blob/layer.

```bash
starget get <REGISTRY>/<IMAGE>:<TAG> <BLOB_DIGEST> <PATH_PATTERN> [OUTPUT_DIR]
```

**Path Patterns:**
- Specific file: `bin/echo`
- Directory: `bin/` or `bin`
- All files: `.` or `/`

**Flags:**
- `--no-progress`: Disable progress bar (useful for scripts)

## Architecture

stargz-get uses a modular architecture with the following components:

- **RegistryClient**: Fetches image manifests from OCI registries
- **ImageAccessor**: Manages lazy access to stargz layers via TOC downloads
- **ImageIndex**: Provides fast file lookup and filtering across layers
- **Downloader**: Orchestrates downloads with progress tracking and retry logic

For detailed architecture and design decisions, see [DESIGN.md](DESIGN.md).

## Development

### Running Tests

```bash
# Run all tests
go test ./stargzget -v

# Run specific test
go test ./stargzget -v -run TestImageIndex_FilterFiles

# Run tests with coverage
go test ./stargzget -cover
```

### Test Coverage

The project has comprehensive test coverage including:
- Unit tests with mocked dependencies
- Integration tests with real stargz images
- Retry logic validation
- Error handling tests

See [DESIGN.md](DESIGN.md#testing-strategy) for more details.

## Project Status

**Current Version**: v0.1.0 (MVP)

### Completed ✅
- Registry manifest access
- Stargz TOC parsing and lazy loading
- File download with pattern matching
- CLI interface with progress tracking
- Automatic retry logic
- Structured error handling
- Comprehensive unit tests

### In Progress 🚧
- Authentication support for private registries
- Multi-threaded downloads
- Checksum verification

See [ROADMAP.md](ROADMAP.md) for detailed development plan.

## How It Works

1. **Fetch Manifest**: Get layer information from the registry
2. **Download TOC**: Fetch only the Table of Contents (typically 0.1-1% of blob size)
3. **Build Index**: Create a fast lookup index of all files across all layers
4. **Filter Files**: Match files based on user's path pattern
5. **Download On-Demand**: Use HTTP range requests to fetch only requested files

This approach is much faster and more efficient than pulling the entire image.

## Design Philosophy

**stargz-get** is designed to be:
- **Simple**: Focused on one task - extracting files from stargz images
- **Lightweight**: No daemon, no complex dependencies
- **Efficient**: Download only what you need using lazy loading

See [DESIGN.md](DESIGN.md#design-philosophy) for more details.

## Limitations

- Only supports stargz/eStargz format images (not regular tar.gz)
- Public registries only (authentication coming soon)
- Sequential downloads (parallel downloads planned)

## Non-goals

- Full OCI image management (use containerd/Docker for that)
- Support for non-stargz image formats
- Image building or pushing

## Contributing

Contributions are welcome! Areas where you can help:
- Implement authentication support
- Add checksum verification
- Improve error messages
- Write additional tests

See [ROADMAP.md](ROADMAP.md#contributing) for good first issues.

## Documentation

- [DESIGN.md](DESIGN.md) - Architecture and design decisions
- [ROADMAP.md](ROADMAP.md) - Development roadmap and future plans
- [errors.go](stargzget/errors.go) - Error handling reference

## References

- [Stargz Snapshotter](https://github.com/containerd/stargz-snapshotter)
- [eStargz Specification](https://github.com/containerd/stargz-snapshotter/blob/main/docs/estargz.md)
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)

## License

This is a learning/demonstration project.
