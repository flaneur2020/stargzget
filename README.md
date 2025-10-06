# stargz-get

stargz-get is a tool to download stargz images from a registry to a local directory.

## Usage

```bash
starget layers <REGISTRY>/<IMAGE>:<TAG> --credential=<USER:PASSWORD>
starget ls <REGISTRY>/<IMAGE>:<TAG> <BLOB> --credential=<USER:PASSWORD>
starget get <REGISTRY>/<IMAGE>:<TAG> <BLOB> <PATH> --credential=<USER:PASSWORD>
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

### Phase 1: Registry Manifest Access
**Goal**: Get blob layer list from registry (without auth)
- [ ] Implement basic `RegistryClient.GetManifest()` for public registry
- [ ] Parse manifest JSON to extract layer digests and media types
- [ ] Filter stargz layers (media type: `application/vnd.oci.image.layer.v1.tar+gzip`)
- [ ] **Validation**: Print blob digest list from `ghcr.io/stargz-containers/node:13.13.0-esgz`

### Phase 2: Stargz Index Parsing
**Goal**: Get file list from a single blob
- [ ] Implement `BlobAccessor.DownloadIndex()` to fetch stargz TOC (Table of Contents)
- [ ] Parse stargz index JSON to extract file entries (name, offset, size)
- [ ] Build file metadata map (path -> chunks info)
- [ ] **Validation**: Print all file paths in the first blob layer

### Phase 3: Single File Download
**Goal**: Download one complete file with all its chunks
- [ ] Implement `ChunkDownloader.DownloadChunk()` with HTTP range requests
- [ ] Add gzip decompression for chunks
- [ ] Write decompressed data to target file path
- [ ] **Validation**: Download `/usr/bin/node` from the blob, verify file size and checksum

### Phase 4: Full Blob Download
**Goal**: Download all files from a single blob layer
- [ ] Iterate through all files in the blob's file list
- [ ] Create directory structure in target path
- [ ] Download each file sequentially
- [ ] **Validation**: Download entire first blob layer, verify file count and total size

### Phase 5: CLI Interface
**Goal**: Parse command-line arguments
- [ ] Implement argument parser for `<REGISTRY>/<IMAGE>:<TAG>/<PATH> <TARGET_DIR>`
- [ ] Add `--blob-digest` optional flag for single blob download
- [ ] Add basic error handling for invalid inputs
- [ ] **Validation**: Run `stargz-get ghcr.io/stargz-containers/node:13.13.0-esgz / ./output`

### Phase 6: Multi-threaded Downloading
**Goal**: Parallel chunk downloads for performance
- [ ] Implement worker pool with configurable size (default: 4)
- [ ] Add chunk download queue with concurrency control
- [ ] Add progress tracking (downloaded bytes / total bytes)
- [ ] **Validation**: Compare download time vs sequential download

### Phase 7: Authentication Support
**Goal**: Support private registries
- [ ] Implement `--credential=<USER:PASSWORD>` flag parsing
- [ ] Add HTTP Basic Auth header to registry requests
- [ ] Add token-based auth flow (for registries like Docker Hub)
- [ ] **Validation**: Download from a private registry with credentials

### Phase 8: Error Handling & Retry
**Goal**: Robust error recovery
- [ ] Add retry logic with exponential backoff for chunk downloads
- [ ] Handle network timeouts and connection errors
- [ ] Add checksum verification for downloaded chunks (if available in TOC)
- [ ] **Validation**: Simulate network errors and verify retry behavior

### Phase 9: Developer Experience
**Goal**: Better usability
- [ ] Add progress bar for downloads (e.g., using `progressbar` library)
- [ ] Add `--verbose` flag for detailed logging
- [ ] Add `--help` and `--version` flags
- [ ] Improve error messages with actionable suggestions
- [ ] **Validation**: User acceptance testing

### Phase 10: Testing & Optimization
**Goal**: Production readiness
- [ ] Write unit tests for core components
- [ ] Add integration test with `ghcr.io/stargz-containers/node:13.13.0-esgz`
- [ ] Optimize memory usage for large files
- [ ] Add benchmarks for download performance
- [ ] **Validation**: Pass all tests, no memory leaks