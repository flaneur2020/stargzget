# stargz-get

stargz-get is a tool to download stargz images from a registry to a local directory.

## Usage

```bash
stargz-get <REGISTRY>/<IMAGE>:<TAG>/<PATH> <TARGET_DIR> --credential=<USER:PASSWORD>
```

## Features

- Download stargz images from a registry to a local directory.
- Support HTTP Basic authentication for registry.
- Uncompress on the fly.
- Multi-threaded downloading.

## Non-goals

- Support other OCI image formats.

## Design

stargz-get is designed to be a simple and lightweight tool. It is not designed to be a full-featured OCI image manager. It is designed to be a tool to download stargz images from a registry to a local directory.

in the codebase, it contains these components:

- `RegistryClient`: to list the manifest of the image layers.
- `BlobAccessor`: to read the manifest data from stargz index, it downloads the stargz manifest index only, manages the access of the files list, chunk list, etc.
- `ChunkDownloader`: manages the download of the chunks, it uses `http.Client` to download the chunks, and uncompress the chunks on the fly, and directly write the uncompressed chunks to the target path of the local directory.

## Roadmap

### Phase 1: Core Functionality (MVP)
- [ ] **Basic stargz image downloader**
  - [ ] Implement registry API client for stargz images
  - [ ] Add support for manifest parsing and layer extraction
  - [ ] Create basic CLI interface with argument parsing
  - [ ] Implement HTTP Basic authentication
  - [ ] Add progress indicators for downloads

### Phase 2: Performance & Reliability
- [ ] **Multi-threaded downloading**
  - [ ] Implement concurrent layer downloads
  - [ ] Add configurable thread pool size
  - [ ] Implement retry logic with exponential backoff
  - [ ] Add resume capability for interrupted downloads
- [ ] **Streaming and memory optimization**
  - [ ] Implement streaming decompression
  - [ ] Add memory-efficient processing for large images
  - [ ] Optimize disk I/O operations

### Phase 4: Developer Experience
- [ ] **Configuration and usability**
  - [ ] Verbose and quiet output modes
  - [ ] Add progress bar for downloads
  - [ ] Better error messages and troubleshooting guides
  - [ ] Add `--help` and `--version` flags
- [ ] **Testing and validation**
  - [ ] Comprehensive test suite
  - [ ] An integration test with a real public stargz image

### Phase 5: Advanced Features
- [ ] **Caching and optimization**
  - [ ] Local layer caching to avoid re-downloading
  - [ ] Delta downloads for updated images
  - [ ] Checksum verification for downloaded layers
- [ ] **Monitoring and observability**
  - [ ] Download statistics and metrics for prometheus
  - [ ] Logging with configurable levels