package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/stargz-snapshotter/estargz"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: local_extract <blob-file> <file-path> <output-dir>")
		os.Exit(1)
	}

	blobPath := os.Args[1]
	filePath := os.Args[2]
	outputDir := os.Args[3]

	// Open blob
	f, err := os.Open(blobPath)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		panic(err)
	}

	fmt.Printf("Opening blob: %s (%d bytes)\n", blobPath, stat.Size())
	sr := io.NewSectionReader(f, 0, stat.Size())

	// Parse as stargz
	reader, err := estargz.Open(sr)
	if err != nil {
		panic(fmt.Errorf("failed to open stargz: %w", err))
	}

	fmt.Printf("TOC digest: %s\n", reader.TOCDigest())

	// Lookup file
	fmt.Printf("Looking up file: %s\n", filePath)
	entry, ok := reader.Lookup(filePath)
	if !ok {
		panic(fmt.Errorf("file not found: %s", filePath))
	}

	fmt.Printf("Found file: type=%s, size=%d bytes\n", entry.Type, entry.Size)

	// Get chunks info
	var offset int64
	var chunks []struct {
		offset int64
		size   int64
	}
	for offset < entry.Size {
		chunkEntry, ok := reader.ChunkEntryForOffset(filePath, offset)
		if !ok {
			break
		}
		compressedOffset := chunkEntry.Offset
		compressedSize := chunkEntry.NextOffset() - chunkEntry.Offset
		chunks = append(chunks, struct {
			offset int64
			size   int64
		}{compressedOffset, compressedSize})
		offset += chunkEntry.ChunkSize
	}

	fmt.Printf("File has %d chunks\n", len(chunks))

	// Extract chunks and decompress
	var fileData bytes.Buffer
	for i, chunk := range chunks {
		fmt.Printf("[%d/%d] Extracting chunk (offset: %d, size: %d bytes)...\n",
			i+1, len(chunks), chunk.offset, chunk.size)

		// Read compressed chunk from blob
		chunkData := make([]byte, chunk.size)
		if _, err := f.ReadAt(chunkData, chunk.offset); err != nil {
			panic(fmt.Errorf("failed to read chunk: %w", err))
		}

		// Decompress
		decompressor := &estargz.GzipDecompressor{}
		decompressedReader, err := decompressor.Reader(bytes.NewReader(chunkData))
		if err != nil {
			panic(fmt.Errorf("failed to decompress: %w", err))
		}

		if _, err := io.Copy(&fileData, decompressedReader); err != nil {
			decompressedReader.Close()
			panic(fmt.Errorf("failed to copy: %w", err))
		}
		decompressedReader.Close()
	}

	// Write to output
	os.MkdirAll(outputDir, 0755)
	outPath := filepath.Join(outputDir, filepath.Base(filePath))
	if err := os.WriteFile(outPath, fileData.Bytes(), 0644); err != nil {
		panic(err)
	}

	fmt.Printf("\n✓ Successfully extracted file to: %s (%d bytes)\n", outPath, fileData.Len())
}
