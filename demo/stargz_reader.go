package main

import (
	"fmt"
	"io"

	"github.com/containerd/stargz-snapshotter/estargz"
	digest "github.com/opencontainers/go-digest"
)

// stargzReader wraps estargz.Reader to provide file lookup and chunk information
type stargzReader struct {
	reader *estargz.Reader
}

// chunkInfo represents the location of a chunk in the blob
type chunkInfo struct {
	offset int64 // offset in the blob (compressed)
	size   int64 // compressed size of this chunk
}

// sectionReaderAt is an interface for types that can be used like io.SectionReader
type sectionReaderAt interface {
	io.ReaderAt
	Size() int64
}

// newStargzReader creates a stargzReader from a sectionReaderAt
// The reader should provide access to the stargz blob
func newStargzReader(sr sectionReaderAt, opts ...estargz.OpenOption) (*stargzReader, error) {
	// Convert to io.SectionReader if needed
	var iosr *io.SectionReader
	if s, ok := sr.(*io.SectionReader); ok {
		iosr = s
	} else {
		// Wrap in io.SectionReader
		iosr = io.NewSectionReader(sr, 0, sr.Size())
	}

	r, err := estargz.Open(iosr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to open stargz: %w", err)
	}
	return &stargzReader{reader: r}, nil
}

// lookupFile finds a file in the TOC and returns its TOCEntry
func (sr *stargzReader) lookupFile(fileName string) (*estargz.TOCEntry, error) {
	entry, ok := sr.reader.Lookup(fileName)
	if !ok {
		return nil, fmt.Errorf("file %q not found in TOC", fileName)
	}
	if entry.Type != "reg" {
		return nil, fmt.Errorf("file %q is not a regular file (type: %s)", fileName, entry.Type)
	}
	return entry, nil
}

// getFileChunks returns all chunks for a given file
// Each chunk describes a byte range in the compressed blob that needs to be fetched
func (sr *stargzReader) getFileChunks(fileName string) ([]chunkInfo, error) {
	entry, err := sr.lookupFile(fileName)
	if err != nil {
		return nil, err
	}

	var chunks []chunkInfo

	// If the file has no chunks or is empty
	if entry.Size == 0 {
		return chunks, nil
	}

	// Get all chunks for this file
	var offset int64
	for offset < entry.Size {
		chunkEntry, ok := sr.reader.ChunkEntryForOffset(fileName, offset)
		if !ok {
			break
		}

		// Each chunk has an Offset (where compressed data starts in blob)
		// and NextOffset (where next chunk's compressed data starts)
		compressedOffset := chunkEntry.Offset
		compressedSize := chunkEntry.NextOffset() - chunkEntry.Offset

		chunks = append(chunks, chunkInfo{
			offset: compressedOffset,
			size:   compressedSize,
		})

		offset += chunkEntry.ChunkSize
	}

	return chunks, nil
}

// getTOCDigest returns the digest of the TOC
func (sr *stargzReader) getTOCDigest() digest.Digest {
	return sr.reader.TOCDigest()
}

// close closes the underlying reader
func (sr *stargzReader) close() error {
	// estargz.Reader doesn't have a Close method in current implementation
	// If needed in future, add it here
	return nil
}
