# Design Document

## Overview

**stargz-get** is a lightweight CLI tool for downloading individual files from stargz (seekable tar.gz) container images without pulling the entire image. It leverages the lazy-loading capabilities of stargz format to fetch only the required data.

## Design Philosophy

The tool is designed around three core principles:

1. **Simplicity**: Focus on one task - extracting files from stargz images
2. **Lightweight**: No daemon, no complex dependencies, minimal resource usage
3. **Efficiency**: Download only what you need using HTTP range requests

## Architecture

### Component Diagram

```
┌─────────────┐
│   CLI       │
│ (starget)   │
└──────┬──────┘
       │
       ├──────────────────────────────────────┐
       │                                      │
       v                                      v
┌──────────────┐                     ┌────────────────┐
│ Registry     │                     │  Downloader    │
│ Client       │                     │                │
└──────┬───────┘                     └───────┬────────┘
       │                                     │
       │ GetManifest()                       │ StartDownload()
       │                                     │
       v                                     v
┌──────────────┐                     ┌────────────────┐
│  Image       │◄────────────────────┤  ImageIndex    │
│  Accessor    │                     │                │
└──────┬───────┘                     └────────────────┘
       │
       │ ImageIndex()
       │ OpenFile()
       │
       v
┌──────────────┐
│  HTTP Client │
│  (Range      │
│   Requests)  │
└──────────────┘
```

### Core Components

#### 1. RegistryClient

**Responsibility**: Communication with OCI-compliant registries

**Key Methods**:
- `GetManifest(imageRef) (*Manifest, error)`: Fetches the image manifest

**Design Decisions**:
- Uses standard HTTP client for simplicity
- Supports Bearer token authentication
- Parses WWW-Authenticate headers for token URLs
- Caches authentication tokens to reduce requests

**Implementation Details**:
```go
type RegistryClient interface {
    GetManifest(ctx context.Context, imageRef string) (*Manifest, error)
}

type Manifest struct {
    SchemaVersion int
    MediaType     string
    Config        Descriptor
    Layers        []Layer
}
```

#### 2. ImageAccessor

**Responsibility**: Manages access to stargz image layers

**Key Methods**:
- `ImageIndex(ctx) (*ImageIndex, error)`: Builds an index of all files in the image
- `OpenFile(ctx, path, blobDigest) (*io.SectionReader, error)`: Opens a file for reading

**Design Decisions**:
- **Lazy TOC Loading**: Downloads only the stargz Table of Contents (TOC), not the entire blob
- **TOC Caching**: Caches downloaded TOCs to avoid redundant network requests
- **HTTP Range Requests**: Uses `Range` headers to fetch only the TOC section at the end of blobs
- **Deferred Content Download**: File content is fetched on-demand via `OpenFile()`

**TOC Download Process**:
1. Send HEAD request to get blob size
2. Calculate TOC offset using `estargz.OpenFooter()`
3. Use Range request to fetch TOC section only
4. Parse TOC as gzipped tar, extract `stargz.index.json`
5. Unmarshal JSON to get file metadata

**Implementation Details**:
```go
type ImageAccessor interface {
    ImageIndex(ctx context.Context) (*ImageIndex, error)
    OpenFile(ctx context.Context, path string, blobDigest digest.Digest) (*io.SectionReader, error)
}

// Internal structure
type imageAccessor struct {
    httpClient     *http.Client
    registryClient RegistryClient
    registry       string
    repository     string
    manifest       *Manifest
    tocCache       map[string]*estargz.JTOC  // Caches TOCs
    authToken      string
    index          *ImageIndex
}
```

#### 3. ImageIndex

**Responsibility**: Provides fast file lookup and filtering across all layers

**Key Methods**:
- `FindFile(path, blobDigest) (*FileInfo, error)`: Finds a specific file
- `FilterFiles(pattern, blobDigest) []*FileInfo`: Filters files by pattern

**Design Decisions**:
- **Dual Indexing**: Maintains both layer-specific and global file maps
- **Later Layer Wins**: When the same file exists in multiple layers, uses the topmost layer (simulating overlay filesystem)
- **Pattern Matching**: Supports exact file match, directory prefix match, and wildcard
- **Optional Blob Filtering**: Can filter to specific layers or search globally

**Data Structure**:
```go
type ImageIndex struct {
    Layers []*LayerInfo                  // Per-layer information
    files  map[string]*FileInfo          // Global file map
}

type LayerInfo struct {
    BlobDigest digest.Digest
    Files      []string
    FileSizes  map[string]int64
}

type FileInfo struct {
    Path       string
    BlobDigest digest.Digest
    Size       int64
}
```

#### 4. Downloader

**Responsibility**: Orchestrates file downloads with progress tracking and retry logic

**Key Methods**:
- `StartDownload(ctx, jobs, progress, options) (*DownloadStats, error)`: Downloads multiple files

**Design Decisions**:
- **Job-Based API**: Uses `DownloadJob` objects for flexibility
- **Automatic Retry**: Retries failed downloads with configurable max attempts
- **Progress Aggregation**: Tracks progress across all files in a single callback
- **Graceful Degradation**: Continues downloading remaining files if some fail

**Download Flow**:
1. Calculate total size from all jobs
2. For each job:
   - Try download with retry loop
   - Create output directory if needed
   - Open file via ImageAccessor
   - Copy content with progress tracking
   - Retry on failure (up to MaxRetries)
3. Return statistics (success/failed/retries)

**Implementation Details**:
```go
type DownloadJob struct {
    Path       string
    BlobDigest digest.Digest
    Size       int64
    OutputPath string
}

type DownloadOptions struct {
    MaxRetries int  // Default: 3
}

type DownloadStats struct {
    TotalFiles      int
    TotalBytes      int64
    DownloadedFiles int
    DownloadedBytes int64
    FailedFiles     int
    Retries         int
}
```

#### 5. Error Handling

**Responsibility**: Structured error types for better error handling

**Design Decisions**:
- **Error Codes**: Machine-readable error codes for programmatic handling
- **Error Context**: Supports additional details and wrapped causes
- **Error Helpers**: Factory functions for common error scenarios

**Error Types**:
```go
type StargzError struct {
    Code    string                      // e.g., "BLOB_NOT_FOUND"
    Message string                      // Human-readable message
    Cause   error                       // Wrapped underlying error
    Details map[string]interface{}      // Additional context
}

// Predefined errors
var (
    ErrBlobNotFound    *StargzError
    ErrFileNotFound    *StargzError
    ErrManifestFetch   *StargzError
    ErrTOCDownload     *StargzError
    ErrAuthFailed      *StargzError
    ErrInvalidDigest   *StargzError
    ErrDownloadFailed  *StargzError
)
```

### Data Flow

#### Listing Files in a Blob

```
User → CLI
  ↓
  starget ls <image> <blob>
  ↓
CLI → RegistryClient: GetManifest()
  ↓
CLI → ImageAccessor: NewImageAccessor(manifest)
  ↓
CLI → ImageAccessor: ImageIndex()
  ↓
ImageAccessor → Registry: Download TOC (Range request)
  ↓
ImageAccessor: Parse TOC, build index
  ↓
CLI: Filter files by blob digest
  ↓
User ← List of files
```

#### Downloading Files

```
User → CLI
  ↓
  starget get <image> <blob> <pattern> <output>
  ↓
CLI → RegistryClient: GetManifest()
  ↓
CLI → ImageAccessor: NewImageAccessor(manifest)
  ↓
CLI → ImageAccessor: ImageIndex()
  ↓
CLI → ImageIndex: FilterFiles(pattern, blob)
  ↓
CLI: Create DownloadJob list
  ↓
CLI → Downloader: StartDownload(jobs, progress, opts)
  ↓
Downloader (for each job):
  ├─→ ImageAccessor: OpenFile(path, blob)
  │   ├─→ Registry: Range request for file chunks
  │   └─→ Decompress & return SectionReader
  ├─→ Write to output file
  └─→ Update progress
  ↓
User ← Download complete with stats
```

## Key Design Patterns

### 1. Lazy Loading

**Problem**: Container images can be very large (hundreds of MB to GBs)

**Solution**:
- Download only the TOC (typically a few KB)
- Fetch file content on-demand via HTTP range requests
- Use estargz library to handle chunk-level lazy loading

**Benefits**:
- Fast startup (no full image download)
- Low bandwidth usage (only download what's needed)
- Low disk usage (no local image cache)

### 2. HTTP Range Requests

**Problem**: Need to fetch specific parts of remote blobs efficiently

**Solution**:
- Use `Range: bytes=start-end` headers
- Fetch TOC from end of blob
- Fetch file chunks on demand

**Implementation**:
```go
type httpBlobReader struct {
    client    *http.Client
    url       string
    authToken *string
}

func (r *httpBlobReader) ReadAt(p []byte, off int64) (int, error) {
    rangeHeader := fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1)
    req.Header.Set("Range", rangeHeader)
    // ... perform request
}
```

### 3. Caching Strategy

**TOC Caching**:
- Cache at ImageAccessor level (in-memory)
- Keyed by blob digest
- Persists for the lifetime of the accessor

**Why not cache file content?**
- Files can be large (memory constraints)
- Use case is typically one-time extraction
- OS filesystem cache handles repeated reads

### 4. Progress Tracking

**Problem**: Users need feedback for long-running downloads

**Solution**:
- Callback-based progress reporting
- Aggregate progress across multiple files
- Lazy progress bar initialization (only when total size is known)

**Implementation**:
```go
type ProgressCallback func(current int64, total int64)

// In Downloader
progressReader := &progressReader{
    reader: fileReader,
    total:  job.Size,
    callback: func(current, total int64) {
        progress(currentTotal + current, totalSize)
    },
}
```

## Security Considerations

### 1. Authentication

**Current State**:
- Supports Bearer token authentication
- Parses WWW-Authenticate headers
- Caches tokens to reduce auth requests

**Limitations**:
- No support for HTTP Basic Auth
- No support for Docker Hub token exchange
- Credentials not yet supported via CLI flags

### 2. Digest Verification

**Current State**:
- Uses blob digests from manifest (trusted source)
- Relies on registry to serve correct content

**Future Enhancement**:
- Verify downloaded file checksums if provided in TOC
- Validate blob digest after full download

### 3. Error Handling

**Current State**:
- Structured errors with context
- Retry logic for transient failures
- Graceful degradation (continue on partial failures)

**Best Practices**:
- Don't expose sensitive info in error messages
- Log errors with sufficient context for debugging
- Return user-friendly messages to CLI

## Performance Characteristics

### Network Efficiency

**TOC Size**: Typically 0.1-1% of blob size
- Example: 100 MB blob → ~500 KB TOC

**Download Overhead**:
- Manifest fetch: 1 request (~5 KB)
- TOC fetch per layer: 1 Range request per layer
- File fetch: 1-N Range requests depending on file size and chunking

### Memory Usage

**Bounded Memory**:
- TOC cache: ~500 KB per layer (limited by number of layers)
- File buffer: Streaming via io.Copy (no full file in memory)
- Progress tracking: Minimal overhead

**Typical Usage** (for 10-layer image):
- TOC cache: ~5 MB
- Active download buffers: ~32 KB per file
- Total: < 10 MB

### Concurrency

**Current State**: Sequential downloads
- Simple implementation
- Predictable progress tracking
- No rate limiting issues

**Future Enhancement**: Parallel downloads
- Worker pool pattern
- Concurrent downloads of different files
- Respect registry rate limits

## Testing Strategy

### Unit Tests

**Coverage Areas**:
1. **ImageIndex**: File filtering and lookup logic
2. **Downloader**: Download workflow and retry logic
3. **Error Handling**: Structured error creation and unwrapping

**Mocking Strategy**:
- Mock `ImageAccessor` for downloader tests
- Mock file content with in-memory readers
- Simulate failures for retry testing

### Integration Tests

**Approach**:
- Use public `ghcr.io/stargz-containers/node:13.13.0-esgz` image
- Verify real TOC downloads
- Validate actual file content

**Trade-offs**:
- Network dependency (acceptable for integration tests)
- Test stability (public registry availability)

## Future Enhancements

### 1. Parallel Downloads

**Design**:
- Worker pool with configurable size
- Job queue for files
- Synchronized progress tracking

**Challenges**:
- Progress bar updates from multiple goroutines
- Registry rate limiting
- Memory usage with concurrent buffers

### 2. Checksum Verification

**Design**:
- Extract checksums from TOC entries
- Verify after download
- Report verification errors

**Benefits**:
- Detect corruption
- Ensure integrity
- Trust verification

### 3. Authentication Support

**Design**:
- Add `--credential` flag to CLI
- Support HTTP Basic Auth
- Support Docker config.json

**Challenges**:
- Secure credential storage
- Token refresh logic
- Multi-registry support

## References

- [eStargz Specification](https://github.com/containerd/stargz-snapshotter/blob/main/docs/estargz.md)
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)
- [HTTP Range Requests (RFC 7233)](https://www.rfc-editor.org/rfc/rfc7233)
