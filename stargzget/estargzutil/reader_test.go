package estargzutil

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"
)

type nopReadSeekCloser struct {
	*bytes.Reader
}

func (n *nopReadSeekCloser) Close() error {
	return nil
}

func buildTestFileReader(t *testing.T, content []byte, chunkSizes []int64) (*FileReader, func()) {
	t.Helper()

	var (
		buf            bytes.Buffer
		chunks         []Chunk
		uncompressedAt int64
		compressedAt   int64
	)

	for _, chunkLen := range chunkSizes {
		if uncompressedAt >= int64(len(content)) {
			break
		}
		if chunkLen == 0 {
			chunks = append(chunks, Chunk{
				Offset:           uncompressedAt,
				Size:             0,
				CompressedOffset: compressedAt,
			})
			continue
		}

		end := uncompressedAt + chunkLen
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		data := content[uncompressedAt:end]
		compressed := gzipCompress(t, data)
		if _, err := buf.Write(compressed); err != nil {
			t.Fatalf("failed to assemble compressed blob: %v", err)
		}

		chunks = append(chunks, Chunk{
			Offset:           uncompressedAt,
			Size:             int64(len(data)),
			CompressedOffset: compressedAt,
			InnerOffset:      0,
		})

		uncompressedAt = end
		compressedAt += int64(len(compressed))
	}

	if uncompressedAt < int64(len(content)) {
		data := content[uncompressedAt:]
		compressed := gzipCompress(t, data)
		if _, err := buf.Write(compressed); err != nil {
			t.Fatalf("failed to assemble compressed blob: %v", err)
		}
		chunks = append(chunks, Chunk{
			Offset:           uncompressedAt,
			Size:             int64(len(data)),
			CompressedOffset: compressedAt,
			InnerOffset:      0,
		})
		uncompressedAt = int64(len(content))
		compressedAt += int64(len(compressed))
	}

	reader := &nopReadSeekCloser{Reader: bytes.NewReader(buf.Bytes())}
	return NewFileReader(chunks, reader), func() {}
}

func gzipCompress(t *testing.T, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		t.Fatalf("failed to gzip data: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("failed to finalize gzip: %v", err)
	}
	return buf.Bytes()
}

func TestFileReader_ReadSequential(t *testing.T) {
	content := []byte("abcdefghijklmnopqrstuvwxyz")
	reader, cleanup := buildTestFileReader(t, content, []int64{5, 7, 14})
	defer cleanup()
	defer reader.Close()

	buf := make([]byte, len(content))
	n, err := reader.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read error = %v, want io.EOF", err)
	}
	if n != len(content) {
		t.Fatalf("Read bytes = %d, want %d", n, len(content))
	}
	if string(buf) != string(content) {
		t.Fatalf("Read content = %q, want %q", string(buf), string(content))
	}
}

func TestFileReader_ReadAcrossChunks(t *testing.T) {
	content := []byte("0123456789abcdef")
	reader, cleanup := buildTestFileReader(t, content, []int64{4, 4, 4, 4})
	defer cleanup()
	defer reader.Close()

	var (
		readBuf   [3]byte
		collected bytes.Buffer
	)

	for {
		n, err := reader.Read(readBuf[:])
		if n > 0 {
			if _, writeErr := collected.Write(readBuf[:n]); writeErr != nil {
				t.Fatalf("failed to grow buffer: %v", writeErr)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
	}

	if collected.String() != string(content) {
		t.Fatalf("Collected data = %q, want %q", collected.String(), string(content))
	}
}

func TestFileReader_SeekAndRead(t *testing.T) {
	content := []byte("the quick brown fox jumps over the lazy dog")
	reader, cleanup := buildTestFileReader(t, content, []int64{10, 10, 10, 9})
	defer cleanup()
	defer reader.Close()

	// Seek to "brown"
	if _, err := reader.Seek(10, io.SeekStart); err != nil {
		t.Fatalf("Seek error: %v", err)
	}

	buf := make([]byte, 5)
	if _, err := reader.Read(buf); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Read error: %v", err)
	}
	if string(buf) != "brown" {
		t.Fatalf("Read content = %q, want %q", string(buf), "brown")
	}

	// Seek forward relative and read next word
	if _, err := reader.Seek(1, io.SeekCurrent); err != nil {
		t.Fatalf("Seek relative error: %v", err)
	}
	buf = make([]byte, 3)
	if _, err := reader.Read(buf); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Read error: %v", err)
	}
	if string(buf) != "fox" {
		t.Fatalf("Read content = %q, want %q", string(buf), "fox")
	}

	// Seek backwards from end
	if _, err := reader.Seek(-3, io.SeekEnd); err != nil {
		t.Fatalf("Seek from end error: %v", err)
	}
	buf = make([]byte, 3)
	n, err := reader.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read error after SeekEnd = %v, want io.EOF", err)
	}
	if n != 3 || string(buf) != "dog" {
		t.Fatalf("Read content = %q, want %q", string(buf), "dog")
	}
}

func TestFileReader_SeekInvalid(t *testing.T) {
	content := []byte("hello")
	reader, cleanup := buildTestFileReader(t, content, []int64{5})
	defer cleanup()
	defer reader.Close()

	if _, err := reader.Seek(-1, io.SeekStart); err == nil {
		t.Fatalf("expected error for negative seek")
	}

	if _, err := reader.Seek(int64(len(content))+10, io.SeekStart); err != nil {
		t.Fatalf("Seek past end should succeed, got error: %v", err)
	}

	buf := make([]byte, 1)
	n, err := reader.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("Read after seek past end = (%d, %v), want (0, io.EOF)", n, err)
	}
}

func TestFileReader_InnerOffset(t *testing.T) {
	payload := []byte("header:actual-data")
	inner := int64(len("header:"))

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(payload); err != nil {
		t.Fatalf("failed to write gzip payload: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	chunks := []Chunk{
		{
			Offset:           0,
			Size:             int64(len(payload)) - inner,
			CompressedOffset: 0,
			InnerOffset:      inner,
		},
	}

	reader := NewFileReader(chunks, &nopReadSeekCloser{Reader: bytes.NewReader(buf.Bytes())})
	defer reader.Close()

	out := make([]byte, len(payload)-int(inner))
	n, err := reader.Read(out)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read error = %v, want io.EOF", err)
	}
	if n != len(out) {
		t.Fatalf("Read bytes = %d, want %d", n, len(out))
	}
	if string(out) != "actual-data" {
		t.Fatalf("Read content = %q, want %q", string(out), "actual-data")
	}
}
