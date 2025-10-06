# stargz-downloader

## About

`stargz-downloader` is a CLI tool that downloads all files from a stargz/eStargz format container image to a local directory. It extracts the complete filesystem from stargz layers while preserving the original directory structure, file permissions, and symlinks.

**Key Features:**
- Downloads all files from stargz images to local filesystem
- Supports basic authentication for private registries
- Concurrent file extraction for improved performance
- Preserves file metadata (permissions, symlinks, hardlinks)

## Design

The tool follows a simple three-stage pipeline:

1. **Image Resolution**: Parses the image reference, resolves the manifest from the registry using containerd's docker resolver, and authenticates if credentials are provided.

2. **Layer Download**: Fetches each layer blob and temporarily stores it locally. Opens the blob as an eStargz reader to access the TOC (Table of Contents).

3. **Concurrent Extraction**: Recursively traverses the TOC tree from the root entry, collecting all file entries. Extracts files concurrently using a worker pool (default 10 workers) to maximize throughput.

**Architecture:**

```
Image Reference → Registry Resolver → Manifest Parser
                                           ↓
                                     Layer Blobs
                                           ↓
                                   eStargz Reader
                                           ↓
                                    TOC Traversal
                                           ↓
                              Concurrent File Extraction
                                           ↓
                                  Local Filesystem
```

## TODOs

- [ ] Streaming extraction without temp files
- [ ] Progress bar for large downloads
- [ ] Selective file extraction (filter by path patterns)
- [ ] Verification of layer digests
