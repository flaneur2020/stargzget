# Stargz Downloader - Single File Download Feature

## 🚀 New Feature: On-demand Single File Download

Now supports downloading **individual files** from stargz images without downloading the entire layer!

### ✨ Core Features

1. **True HTTP Range Requests**: Download only required bytes, no bandwidth waste
2. **Efficient TOC Parsing**: Download only the last 50KB of layer to parse file index
3. **On-demand Chunk Download**: Download only chunks containing the target file, saving significant bandwidth
4. **Automatic Authentication**: Supports Bearer token authentication for Docker registry

### 📊 Performance Comparison

Assume you want to download a 1MB file from a 100MB layer:

- **Traditional Method**: Download entire 100MB layer → extract → retrieve file
- **New Method**:
  1. HEAD request to get layer size (few KB)
  2. Download last 50KB to get TOC
  3. Download corresponding file chunks (approx. 1-2MB compressed data)

**Bandwidth Savings: > 98%** 🎯

---

## 🔧 Usage

### 1. Download Single File

```bash
./stargz-downloader \
  --image ghcr.io/stargz-containers/ubuntu:22.04-esgz \
  --layer sha256:5d0da3dc976460b72c77d94c8a1ad043720b0416bfc16c52c45d4847e53fadb6 \
  --file /usr/bin/bash \
  --output ./output
```

### 2. Download with Authentication

```bash
./stargz-downloader \
  --image myregistry.io/myapp:latest \
  --layer sha256:abc123... \
  --file /app/config.json \
  --username myuser \
  --password mypass \
  --output ./output
```

### 3. Use Insecure Registry

```bash
./stargz-downloader \
  --image localhost:5000/test:latest \
  --layer sha256:def456... \
  --file /data/file.txt \
  --plain-http \
  --insecure \
  --output ./output
```

### 4. Download Entire Image (Original Feature)

```bash
./stargz-downloader \
  --image ghcr.io/stargz-containers/ubuntu:22.04-esgz \
  --output ./all-files \
  --concurrency 10
```

---

## 📋 Parameter Description

| Parameter | Short | Required | Description |
|-----------|-------|----------|-------------|
| `--image` | `-i` | ✅ | Image reference (e.g., ubuntu:latest) |
| `--output` | `-o` | ✅ | Output directory |
| `--file` | `-f` | ⚪ | File path to download (requires --layer) |
| `--layer` | `-l` | ⚪ | Layer digest (requires --file) |
| `--username` | `-u` | ⚪ | Registry username |
| `--password` | `-p` | ⚪ | Registry password |
| `--insecure` | - | ⚪ | Allow insecure HTTPS connections |
| `--plain-http` | - | ⚪ | Use HTTP instead of HTTPS |
| `--concurrency` | - | ⚪ | Concurrent downloads (full image only, default 10) |

---

## 🎯 How to Get Layer Digest

Several ways to get layer digest:

### Method 1: Use `docker manifest inspect`

```bash
docker manifest inspect ghcr.io/stargz-containers/ubuntu:22.04-esgz | jq '.layers[].digest'
```

### Method 2: Use `crane`

```bash
crane manifest ghcr.io/stargz-containers/ubuntu:22.04-esgz | jq '.layers[].digest'
```

### Method 3: Download Full Image First to View TOC

```bash
# Download a small file to view all layers
./stargz-downloader --image <image> --output ./temp
# Check layer digests in log output
```

---

## 🏗️ Architecture Overview

The new architecture is divided into three layers:

```
downloadManager (Business Logic Layer)
  ↓
stargzReader (TOC Parsing Layer)
  - Parse stargz TOC
  - Locate file chunks position and size
  ↓
registryClient (Transport Layer)
  - Use HTTP Range requests to download specific byte ranges
  - Handle Bearer token authentication and retries
```

### Download Process

1. **HEAD Request**: Get blob size (few KB data transfer)
2. **Range Request TOC**: Download last 50KB (contains file index)
3. **Parse TOC**: Find all chunks for target file
4. **Range Request Chunks**: Download only chunks containing the file
5. **Decompression**: gzip decompress each chunk
6. **Concatenate and Write**: Merge all chunk data and write to file

---

## 📝 Example Output

```
=== Single File Download Mode ===
Image: ghcr.io/stargz-containers/ubuntu:22.04-esgz
Layer: sha256:5d0da3dc976460b72c77d94c8a1ad043720b0416bfc16c52c45d4847e53fadb6
File: /usr/bin/bash
Output: ./output

  Getting blob size...
  Blob size: 29318840 bytes (27.96 MB)
  Fetching TOC (last 50KB of blob)...
  TOC size: 51200 bytes
  Parsing TOC...
  Looking up file: /usr/bin/bash
  File has 1 chunks
  Total compressed data to download: 823156 bytes (0.78 MB)
  Savings vs full blob: 97.19%
  [1/1] Downloading chunk (offset: 15234560, size: 823156 bytes)...
  [1/1] Chunk downloaded and decompressed
  Writing file to: ./output/bash (1234567 bytes)

✓ Successfully downloaded file to: ./output/bash
```

---

## 🔬 Technical Details

### Stargz Format

Stargz (Seekable tar.gz) is a special container image format:

- **Regular tar.gz**: Must be decompressed sequentially from start to end
- **Stargz**:
  - Each file is divided into multiple chunks, independently compressed
  - TOC (Table of Contents) at the end, recording all file and chunk positions
  - Supports HTTP Range requests for on-demand downloading

### HTTP Range Requests

```http
GET /v2/library/ubuntu/blobs/sha256:abc123... HTTP/1.1
Range: bytes=15234560-16057715
Authorization: Bearer <token>
```

Server response:
```http
HTTP/1.1 206 Partial Content
Content-Range: bytes 15234560-16057715/29318840
Content-Length: 823156
```

---

## 🐛 Troubleshooting

### Issue 1: `layer digest is required`

Ensure both `--file` and `--layer` parameters are provided.

### Issue 2: `file not found in TOC`

- Check if file path is correct (must be absolute path, e.g., `/usr/bin/bash`)
- Ensure the layer actually contains the file
- Some layers may not be in stargz format

### Issue 3: `authentication failed`

- Check if username and password are correct
- Some registries require `docker login` first
- Try using `--insecure` option (for testing only)

### Issue 4: `unexpected status code 206`

This is normal! 206 indicates Partial Content, meaning Range request succeeded.

---

## 🎓 Best Practices

1. **Small Files First**: On-demand download works best for files smaller than 10MB
2. **Know File Location**: Know the file path in advance
3. **Use esgz Images**: Ensure images are in estargz or stargz format
4. **Cache TOC**: Reuse TOC data if downloading multiple files

---

## 📚 References

- [Stargz Snapshotter Project](https://github.com/containerd/stargz-snapshotter)
- [eStargz Format Specification](https://github.com/containerd/stargz-snapshotter/blob/main/docs/estargz.md)
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)

---

**Author's Note**: This feature is particularly suitable for on-demand pulling of configuration files, binaries, etc. during container startup, which can significantly reduce cold start time! 🚀
