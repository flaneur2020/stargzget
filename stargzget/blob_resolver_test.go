package stargzget

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"

	"github.com/flaneur2020/stargz-get/stargzget/estargzutil"
	stor "github.com/flaneur2020/stargz-get/stargzget/storage"
	"github.com/opencontainers/go-digest"
)

type stubStorage struct {
	data []byte
}

func (s *stubStorage) ListBlobs(ctx context.Context) ([]stor.BlobDescriptor, error) {
	return nil, nil
}

func (s *stubStorage) ReadBlob(ctx context.Context, dgst digest.Digest, offset int64, length int64) (io.ReadCloser, error) {
	if offset < 0 || offset > int64(len(s.data)) {
		return nil, io.ErrUnexpectedEOF
	}
	end := int64(len(s.data))
	if length > 0 && offset+length <= end {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(s.data[offset:end])), nil
}

func TestBlobResolver_FileMetadata(t *testing.T) {
	dgst := digest.FromString("blob")

	resolver := &blobResolver{
		tocCache: map[digest.Digest]*estargzutil.JTOC{
			dgst: {
				Entries: []*estargzutil.TOCEntry{
					{
						Name:        "usr/bin/bash",
						Type:        "reg",
						Size:        5,
						Offset:      0,
						ChunkOffset: 0,
						ChunkSize:   5,
					},
				},
			},
		},
	}

	meta, err := resolver.FileMetadata(context.Background(), dgst, "usr/bin/bash")
	if err != nil {
		t.Fatalf("FileMetadata() error = %v", err)
	}

	if meta.Size != 5 {
		t.Fatalf("Size = %d, want 5", meta.Size)
	}
	if len(meta.Chunks) != 1 {
		t.Fatalf("Chunks len = %d, want 1", len(meta.Chunks))
	}
	ch := meta.Chunks[0]
	if ch.Offset != 0 || ch.Size != 5 {
		t.Fatalf("Chunk = %+v, want offset 0 size 5", ch)
	}
}

func TestBlobResolver_ReadChunk(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("hello"))
	gz.Close()

	storage := &stubStorage{data: buf.Bytes()}
	resolver := &blobResolver{storage: storage}

	chunk := Chunk{
		Offset:           0,
		Size:             5,
		CompressedOffset: 0,
		InnerOffset:      0,
	}

	data, err := resolver.ReadChunk(context.Background(), digest.FromString("blob"), "usr/bin/bash", chunk)
	if err != nil {
		t.Fatalf("ReadChunk() error = %v", err)
	}

	if string(data) != "hello" {
		t.Fatalf("ReadChunk() = %q, want %q", string(data), "hello")
	}
}

func TestBlobResolver_TOC_UsesCache(t *testing.T) {
	dgst := digest.FromString("blob")
	toc := &estargzutil.JTOC{}

	resolver := &blobResolver{
		tocCache: map[digest.Digest]*estargzutil.JTOC{
			dgst: toc,
		},
	}

	got, err := resolver.TOC(context.Background(), dgst)
	if err != nil {
		t.Fatalf("TOC() error = %v", err)
	}
	if got != toc {
		t.Fatalf("TOC() returned different pointer")
	}
}
