package estargzutil

import (
	"fmt"
	"sort"
)

// Chunk describes a single uncompressed chunk inside a file entry.
type Chunk struct {
	Offset           int64
	Size             int64
	CompressedOffset int64
	InnerOffset      int64
}

// ChunksForFile extracts the chunk list for a specific file entry.
func ChunksForFile(toc *JTOC, fileName string) (int64, []Chunk, error) {
	var (
		size    int64
		found   bool
		chunks  []Chunk
		entries = toc.Entries
	)

	for _, entry := range entries {
		if entry.Name != fileName {
			continue
		}

		switch entry.Type {
		case "reg":
			found = true
			size = entry.Size
			chunkSize := entry.ChunkSize
			if chunkSize == 0 && entry.Size != 0 {
				chunkSize = entry.Size
			}
			chunks = append(chunks, Chunk{
				Offset:           entry.ChunkOffset,
				Size:             chunkSize,
				CompressedOffset: entry.Offset,
				InnerOffset:      entry.InnerOffset,
			})
		case "chunk":
			found = true
			chunkSize := entry.ChunkSize
			if chunkSize == 0 && size != 0 {
				chunkSize = size - entry.ChunkOffset
			}
			chunks = append(chunks, Chunk{
				Offset:           entry.ChunkOffset,
				Size:             chunkSize,
				CompressedOffset: entry.Offset,
				InnerOffset:      entry.InnerOffset,
			})
		}
	}

	if !found {
		return 0, nil, fmt.Errorf("file not found: %s", fileName)
	}

	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].Offset == chunks[j].Offset {
			return chunks[i].InnerOffset < chunks[j].InnerOffset
		}
		return chunks[i].Offset < chunks[j].Offset
	})

	for idx := range chunks {
		if chunks[idx].Size == 0 {
			nextOffset := size
			if idx+1 < len(chunks) {
				nextOffset = chunks[idx+1].Offset
			}
			chunkSize := nextOffset - chunks[idx].Offset
			if chunkSize <= 0 {
				chunkSize = size - chunks[idx].Offset
			}
			if chunkSize < 0 {
				chunkSize = 0
			}
			chunks[idx].Size = chunkSize
		}
	}

	return size, chunks, nil
}
