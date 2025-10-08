package estargzutil

import (
	"compress/gzip"
	"fmt"
	"io"
	"sort"
)

type FileReader struct {
	chunks []Chunk
	r      io.ReadSeekCloser

	size            int64
	pos             int64
	currentChunkIdx int
	currentChunkBuf []byte
}

var _ io.ReadSeekCloser = (*FileReader)(nil)

func NewFileReader(chunks []Chunk, r io.ReadSeekCloser) *FileReader {
	var size int64
	for _, ch := range chunks {
		if end := ch.Offset + ch.Size; end > size {
			size = end
		}
	}

	return &FileReader{
		r:               r,
		chunks:          chunks,
		size:            size,
		currentChunkIdx: -1,
	}
}

func (f *FileReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	if f.pos >= f.size {
		return 0, io.EOF
	}

	readTotal := 0
	for readTotal < len(p) && f.pos < f.size {
		chunkIdx := f.chunkIndexForOffset(f.pos)
		if chunkIdx < 0 {
			break
		}

		if err := f.ensureChunk(chunkIdx); err != nil {
			if readTotal > 0 && err == io.EOF {
				return readTotal, io.EOF
			}
			return readTotal, err
		}

		chunk := f.chunks[chunkIdx]
		offsetInChunk := int(f.pos - chunk.Offset)
		available := len(f.currentChunkBuf) - offsetInChunk
		if available <= 0 {
			// Nothing left in this chunk; move to next chunk
			f.currentChunkIdx = -1
			f.currentChunkBuf = nil
			f.pos = chunk.Offset + chunk.Size
			continue
		}

		toCopy := len(p) - readTotal
		if toCopy > available {
			toCopy = available
		}

		copy(p[readTotal:readTotal+toCopy], f.currentChunkBuf[offsetInChunk:offsetInChunk+toCopy])
		readTotal += toCopy
		f.pos += int64(toCopy)
	}

	if readTotal == 0 {
		return 0, io.EOF
	}

	if f.pos >= f.size {
		return readTotal, io.EOF
	}

	return readTotal, nil
}

func (f *FileReader) Seek(offset int64, whence int) (int64, error) {
	var newPos int64

	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = f.pos + offset
	case io.SeekEnd:
		newPos = f.size + offset
	default:
		return 0, fmt.Errorf("invalid whence %d", whence)
	}

	if newPos < 0 {
		return 0, fmt.Errorf("invalid seek position %d", newPos)
	}

	f.pos = newPos
	if f.currentChunkIdx != -1 {
		chunk := f.chunks[f.currentChunkIdx]
		if newPos < chunk.Offset || newPos >= chunk.Offset+chunk.Size {
			f.currentChunkIdx = -1
			f.currentChunkBuf = nil
		}
	}

	return f.pos, nil
}

func (f *FileReader) Close() error {
	f.chunks = nil
	f.currentChunkBuf = nil
	if f.r == nil {
		return nil
	}
	return f.r.Close()
}

func (f *FileReader) chunkIndexForOffset(offset int64) int {
	if len(f.chunks) == 0 {
		return -1
	}

	if offset < 0 || offset >= f.size {
		return -1
	}

	idx := sort.Search(len(f.chunks), func(i int) bool {
		ch := f.chunks[i]
		return ch.Offset+ch.Size > offset
	})
	if idx >= len(f.chunks) {
		return -1
	}
	return idx
}

func (f *FileReader) ensureChunk(idx int) error {
	if idx == f.currentChunkIdx && f.currentChunkBuf != nil {
		return nil
	}

	if idx < 0 || idx >= len(f.chunks) {
		return io.EOF
	}

	chunk := f.chunks[idx]
	if chunk.Size <= 0 {
		f.currentChunkIdx = idx
		f.currentChunkBuf = nil
		return nil
	}

	if _, err := f.r.Seek(chunk.CompressedOffset, io.SeekStart); err != nil {
		return err
	}

	gz, err := gzip.NewReader(f.r)
	if err != nil {
		return err
	}

	if chunk.InnerOffset > 0 {
		if _, err := io.CopyN(io.Discard, gz, chunk.InnerOffset); err != nil {
			gz.Close()
			return err
		}
	}

	buf := make([]byte, chunk.Size)
	if _, err := io.ReadFull(gz, buf); err != nil {
		gz.Close()
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	f.currentChunkIdx = idx
	f.currentChunkBuf = buf
	return nil
}
