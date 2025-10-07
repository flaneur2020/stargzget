# Project Roadmap

## Completed Phases ✅

### Phase 1: Registry Manifest Access ✅
**Goal**: Get blob layer list from registry (without auth)

**Completed**:
- [x] Implement basic `RegistryClient.GetManifest()` for public registry
- [x] Parse manifest JSON to extract layer digests and media types
- [x] Filter stargz layers (media type: `application/vnd.oci.image.layer.v1.tar+gzip`)
- [x] **Validation**: Print blob digest list from `ghcr.io/stargz-containers/node:13.13.0-esgz`

**Status**: Fully implemented and tested

---

### Phase 2: Stargz Index Parsing ✅
**Goal**: Get file list from a single blob

**Completed**:
- [x] Implement TOC download using estargz library
- [x] Parse stargz index JSON to extract file entries (name, offset, size)
- [x] Build file metadata map (path -> file info)
- [x] **Validation**: Print all file paths in blob layers

**Key Achievement**: TOC-only download (lazy loading), not full blob

**Status**: Fully implemented and tested

---

### Phase 3: File Download ✅
**Goal**: Download individual files and directories

**Completed**:
- [x] Implement file download using HTTP range requests
- [x] Add gzip decompression using estargz library
- [x] Write decompressed data to target file path
- [x] Support directory downloads with pattern matching
- [x] Unified download interface with `DownloadJob`
- [x] **Validation**: Successfully download and verify files

**Key Features**:
- Single file download
- Directory download (recursive)
- Pattern-based filtering (`.`, `/`, `bin/`, `bin/echo`)
- Unified job-based API

**Status**: Fully implemented and tested

---

### Phase 4: Image Index & Filtering ✅
**Goal**: Fast file lookup across all layers

**Completed**:
- [x] Implement `ImageIndex` with layer information
- [x] Support `FindFile()` with optional blob digest
- [x] Support `FilterFiles()` with pattern matching
- [x] Layer override semantics (later layers win)
- [x] Comprehensive unit tests

**Status**: Fully implemented and tested

---

### Phase 5: CLI Interface ✅
**Goal**: User-friendly command-line interface

**Completed**:
- [x] Implement Cobra-based CLI with subcommands
- [x] `starget info` - List all layers in image
- [x] `starget ls` - List files in a blob
- [x] `starget get` - Download files with pattern matching
- [x] Add `--no-progress` flag
- [x] Add help text for all commands
- [x] **Validation**: Full CLI workflow tested

**Status**: Fully implemented and tested

---

### Phase 8: Error Handling & Retry ✅
**Goal**: Robust error recovery

**Completed**:
- [x] Add retry logic with configurable max retries (default: 3)
- [x] Handle network errors gracefully
- [x] Track failed files and retry counts in statistics
- [x] Structured error types with error codes
- [x] Error context (cause, details)
- [x] **Validation**: Retry logic verified with simulated failures

**Status**: Fully implemented and tested

---

### Phase 9: Developer Experience ✅
**Goal**: Better usability and feedback

**Completed**:
- [x] Add progress bar for downloads using progressbar library
- [x] Show download speed indicator
- [x] Add `--no-progress` flag for script usage
- [x] Improve error messages with context
- [x] Add `--help` for all commands
- [x] **Validation**: User acceptance tested

**Status**: Fully implemented and tested

---

### Phase 10: Testing ✅
**Goal**: Production readiness through testing

**Completed**:
- [x] Write unit tests for core components
- [x] Mock-based testing for Downloader
- [x] Integration tests with real stargz images
- [x] Test coverage for error scenarios
- [x] Retry logic testing with simulated failures
- [x] **Validation**: All tests passing (22/22)

**Test Coverage**:
- `TestImageIndex_FilterFiles`: 9 test cases
- `TestImageIndex_FindFile`: 4 test cases
- `TestDownloader_StartDownload`: 3 test cases
- `TestDownloader_StartDownload_WithRetries`: 5 test cases
- `TestStargzError_*`: 10+ test cases

**Status**: Fully implemented and tested

---

### Phase 6: Multi-threaded Downloading ✅
**Goal**: Parallel file downloads for performance

**Completed Features**:
- [x] Implement worker pool with configurable size (default: 4)
- [x] Add job queue with concurrency control via channels
- [x] Coordinate progress tracking across workers with mutex protection
- [x] Pre-allocate progress offsets for each file to avoid race conditions
- [x] Add `--concurrency` flag to CLI
- [x] Comprehensive concurrency testing (1/2/4/8 workers)
- [x] Race detector validation passed
- [x] **Validation**: All tests passing (30/30)

**Implementation Details**:
- Worker pool using goroutines and channels
- Mutex-protected stats updates
- Pre-computed baseOffset for accurate progress tracking
- Supports both sequential (concurrency=1) and parallel downloads

**Status**: Fully implemented and tested (multi-file download only; single-file chunking not implemented)

---

### Phase 13: Verbose Logging ✅
**Goal**: Better debugging and troubleshooting

**Completed Features**:
- [x] Add `--verbose` flag for detailed logging (INFO level)
- [x] Add `--debug` flag for developer debugging (DEBUG level)
- [x] Log HTTP requests and responses in RegistryClient
- [x] Log TOC download and parsing steps in ImageAccessor
- [x] Log retry attempts with reasons in Downloader
- [x] Redact sensitive info (auth tokens, passwords)
- [x] **Validation**: Tested with real stargz images

**Implementation Details**:
- Created standalone `logger` package with log levels: DEBUG, INFO, WARN, ERROR, SILENT
- Timestamp-based log format: `[HH:MM:SS.mmm] LEVEL: message`
- Automatic redaction of Authorization headers and token parameters
- Global log level controlled by CLI flags

**Status**: Fully implemented and tested

---

## Future Enhancements 🚧

### Phase 6.1: Single File Chunked Download (Not Implemented)
**Goal**: Parallel chunk downloads for single large files

**Planned Features**:
- [ ] Detect file size threshold (e.g., > 10MB)
- [ ] Split into chunks (e.g., 4MB each)
- [ ] Use HTTP Range requests for concurrent chunk downloads
- [ ] Reassemble chunks in correct order
- [ ] Handle chunk retry independently
- [ ] **Validation**: Compare download time vs sequential download

**Design Considerations**:
- Use `sync.WaitGroup` for worker coordination
- Channel-based job distribution
- Mutex-protected progress updates
- Configurable via `--concurrency` flag
- For single file parallel download:
  - Detect file size threshold (e.g., > 10MB)
  - Split into chunks (e.g., 4MB each)
  - Use HTTP Range requests for concurrent chunk downloads
  - Reassemble chunks in correct order
  - Handle chunk retry independently

**Estimated Effort**: Medium (2-3 days)

---

### Phase 7: Authentication Support ✅ (Partial)
**Goal**: Support private registries

**Completed Features**:
- [x] Implement `--credential=<USER:PASSWORD>` flag parsing
- [x] Add HTTP Basic Auth header to registry requests
- [x] Token-based auth flow (Bearer token support)
- [x] Basic mocked tests for authentication

**Remaining Features**:
- [ ] Support Docker config.json credential helper
- [ ] Add credential caching
- [ ] Integration tests with real private registry

**Design Considerations**:
- Secure credential handling (no logging) ✅
- Token refresh logic ✅
- Support multiple auth schemes (Basic + Bearer) ✅
- Integration with Docker credential helpers (TODO)

**Status**: Basic Auth and Bearer token support implemented and tested

---

### Phase 11: Checksum Verification
**Goal**: Verify download integrity

**Planned Features**:
- [ ] Extract checksums from stargz TOC
- [ ] Verify file content after download
- [ ] Report verification errors clearly
- [ ] Add `--skip-verify` flag for fast downloads
- [ ] **Validation**: Detect and report corrupted downloads

**Design Considerations**:
- Use checksums from TOC entries if available
- Compute SHA256 during download (streaming)
- Compare with expected checksum
- Fail fast on mismatch

**Estimated Effort**: Small (1 day)

---

### Phase 12: Exponential Backoff
**Goal**: Smarter retry delays

**Planned Features**:
- [ ] Implement exponential backoff for retries
- [ ] Add jitter to avoid thundering herd
- [ ] Configurable backoff parameters
- [ ] Respect Retry-After headers
- [ ] **Validation**: Test with simulated transient failures

**Formula**: `delay = min(base * 2^attempt + jitter, maxDelay)`

**Estimated Effort**: Small (1 day)

---

### Phase 14: Configuration File
**Goal**: Persistent configuration

**Planned Features**:
- [ ] Support `~/.stargz-get/config.yaml`
- [ ] Configure default options (concurrency, retries, etc.)
- [ ] Per-registry configurations
- [ ] Credential storage (encrypted)
- [ ] **Validation**: Load config and apply defaults

**Config Format**:
```yaml
defaults:
  concurrency: 4
  max_retries: 3
  no_progress: false

registries:
  ghcr.io:
    auth_type: token
  docker.io:
    auth_type: basic
```

**Estimated Effort**: Medium (2 days)

---

### Phase 15: Resume Support
**Goal**: Resume interrupted downloads

**Planned Features**:
- [ ] Track partial downloads in state file
- [ ] Resume from last known offset
- [ ] Verify partial file integrity
- [ ] Clean up orphaned partial files
- [ ] **Validation**: Resume after interruption

**Design Considerations**:
- Use `.partial` extension for incomplete files
- Store state in `~/.stargz-get/state/`
- Checksum partial content before resume

**Estimated Effort**: Large (3-5 days)

---

### Phase 16: Shell Completion
**Goal**: Better CLI experience

**Planned Features**:
- [ ] Bash completion
- [ ] Zsh completion
- [ ] Fish completion
- [ ] Complete image references from registry
- [ ] Complete file paths from TOC
- [ ] **Validation**: Test completion in different shells

**Estimated Effort**: Small (1 day)

---

### Phase 17: Performance Optimization
**Goal**: Faster downloads and lower resource usage

**Planned Features**:
- [ ] Profile memory usage and optimize allocations
- [ ] Benchmark download performance
- [ ] Optimize TOC parsing
- [ ] Reduce HTTP roundtrips
- [ ] Add connection pooling
- [ ] **Validation**: Performance benchmarks

**Target Metrics**:
- Reduce TOC download time by 30%
- Reduce memory usage by 20%
- Support 10+ concurrent downloads

**Estimated Effort**: Large (5+ days)

---

## Non-Goals 🚫

These features are explicitly **out of scope** for this project:

- ❌ Full OCI image management (use containerd/Docker)
- ❌ Support for non-stargz image formats (regular tar.gz, zstd, etc.)
- ❌ Image building or pushing to registries
- ❌ Container runtime integration
- ❌ Image signing and verification (cosign, notary)
- ❌ GUI or web interface

---

## Version Plan

### v0.1.0 - MVP ✅ (Current)
- [x] Basic download functionality
- [x] CLI interface
- [x] Progress tracking
- [x] Retry logic
- [x] Unit tests

### v0.2.0 - Enhanced (Planned)
- [ ] Authentication support
- [ ] Multi-threaded downloads
- [ ] Checksum verification
- [ ] Verbose logging

### v0.3.0 - Production (Future)
- [ ] Configuration file
- [ ] Resume support
- [ ] Shell completion
- [ ] Performance optimization
- [ ] Comprehensive documentation

### v1.0.0 - Stable (Future)
- [ ] Full feature set
- [ ] Production-ready
- [ ] Stable API
- [ ] Comprehensive test coverage
- [ ] Performance benchmarks

---

## Contributing

See areas for contribution in the Future Enhancements section. Good first issues:
- Phase 11: Checksum Verification
- Phase 12: Exponential Backoff
- Phase 13: Verbose Logging

For larger features (Phase 6, 7, 15), please open an issue for discussion before implementation.
