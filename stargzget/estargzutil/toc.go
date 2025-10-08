package estargzutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

const TOCTarName = "stargz.index.json"

// JTOC models the JSON TOC structure embedded in eStargz blobs.
type JTOC struct {
	Version int         `json:"version"`
	Entries []*TOCEntry `json:"entries"`
}

// FileEntry aggregates metadata for a regular file listed in the TOC.
type FileEntry struct {
	Size   int64
	Chunks []Chunk
}

// TOCEntry represents a single entry in the TOC.
type TOCEntry struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Size        int64             `json:"size,omitempty"`
	Offset      int64             `json:"offset,omitempty"`
	ChunkOffset int64             `json:"chunkOffset,omitempty"`
	ChunkSize   int64             `json:"chunkSize,omitempty"`
	InnerOffset int64             `json:"innerOffset,omitempty"`
	ChunkDigest string            `json:"chunkDigest,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ReadTOC streams and decodes a TOC tarball from the provided reader.
func ReadTOC(r io.Reader) (*JTOC, error) {
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate TOC tar archive: %w", err)
		}

		if header.Name != TOCTarName {
			continue
		}

		tocJSONBytes, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read TOC JSON: %w", err)
		}

		var toc JTOC
		if err := json.Unmarshal(tocJSONBytes, &toc); err != nil {
			return nil, fmt.Errorf("failed to unmarshal TOC JSON: %w", err)
		}
		return &toc, nil
	}

	return nil, fmt.Errorf("%s not found in TOC tar archive", TOCTarName)
}

// ParseTOC parses the gzipped TOC tar section and returns the decoded TOC.
func ParseTOC(data []byte) (*JTOC, error) {
	return ReadTOC(bytes.NewReader(data))
}

// FileEntries returns a map of file name to aggregated chunk metadata for each file.
func (toc *JTOC) FileEntries() map[string]FileEntry {
	files := make(map[string]FileEntry)
	if toc == nil || len(toc.Entries) == 0 {
		return files
	}

	type fileBuilder struct {
		size   int64
		chunks []Chunk
	}

	builders := make(map[string]*fileBuilder)

	for _, entry := range toc.Entries {
		if entry == nil {
			continue
		}
		if entry.Type != "reg" && entry.Type != "chunk" {
			continue
		}

		builder := builders[entry.Name]
		if builder == nil {
			builder = &fileBuilder{}
			builders[entry.Name] = builder
		}

		if entry.Size > builder.size {
			builder.size = entry.Size
		}

		chunkSize := entry.ChunkSize
		if entry.Type == "reg" && chunkSize == 0 && entry.Size != 0 {
			chunkSize = entry.Size
		}

		ch := Chunk{
			Offset:           entry.ChunkOffset,
			Size:             chunkSize,
			CompressedOffset: entry.Offset,
			InnerOffset:      entry.InnerOffset,
		}

		builder.chunks = append(builder.chunks, ch)

		if chunkSize > 0 {
			if end := entry.ChunkOffset + chunkSize; end > builder.size {
				builder.size = end
			}
		}
	}

	for name, builder := range builders {
		if len(builder.chunks) == 0 {
			continue
		}

		sorted := append([]Chunk(nil), builder.chunks...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].Offset == sorted[j].Offset {
				return sorted[i].InnerOffset < sorted[j].InnerOffset
			}
			return sorted[i].Offset < sorted[j].Offset
		})

		fileSize := builder.size
		for idx := range sorted {
			if sorted[idx].Size == 0 {
				nextOffset := fileSize
				if idx+1 < len(sorted) {
					nextOffset = sorted[idx+1].Offset
				}
				chunkSize := nextOffset - sorted[idx].Offset
				if chunkSize <= 0 {
					chunkSize = fileSize - sorted[idx].Offset
				}
				if chunkSize < 0 {
					chunkSize = 0
				}
				sorted[idx].Size = chunkSize
			}
			if end := sorted[idx].Offset + sorted[idx].Size; end > fileSize {
				fileSize = end
			}
		}

		files[name] = FileEntry{
			Size:   fileSize,
			Chunks: sorted,
		}
	}

	return files
}
