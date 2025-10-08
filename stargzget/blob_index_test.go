package stargzget

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/flaneur2020/stargz-get/stargzget/estargzutil"
	stor "github.com/flaneur2020/stargz-get/stargzget/storage"
	"github.com/opencontainers/go-digest"
)

type stubBlobResolver struct {
	toc *estargzutil.JTOC
}

func (s *stubBlobResolver) FileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error) {
	return nil, nil
}

func (s *stubBlobResolver) ReadChunk(ctx context.Context, blobDigest digest.Digest, path string, chunk Chunk) ([]byte, error) {
	return nil, nil
}

func (s *stubBlobResolver) TOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error) {
	return s.toc, nil
}

type stubIndexStorage struct {
	blobs []stor.BlobDescriptor
}

func (s *stubIndexStorage) ListBlobs(ctx context.Context) ([]stor.BlobDescriptor, error) {
	return s.blobs, nil
}

func (s *stubIndexStorage) ReadBlob(ctx context.Context, dgst digest.Digest, offset int64, length int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func TestBlobIndexLoader_Load(t *testing.T) {
	dgst := digest.FromString("blob")
	toc := &estargzutil.JTOC{
		Entries: []*estargzutil.TOCEntry{
			{Name: "bin/bash", Type: "reg", Size: 5},
			{Name: "lib/libc.so", Type: "reg", Size: 3},
		},
	}

	storage := &stubIndexStorage{
		blobs: []stor.BlobDescriptor{{Digest: dgst, Size: 8}},
	}
	resolver := &stubBlobResolver{toc: toc}

	loader := NewBlobIndexLoader(storage, resolver)
	index, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(index.Layers) != 1 {
		t.Fatalf("Layers len = %d, want 1", len(index.Layers))
	}

	if len(index.files) != 2 {
		t.Fatalf("files len = %d, want 2", len(index.files))
	}

	if _, err := index.FindFile("bin/bash", dgst); err != nil {
		t.Fatalf("FindFile() returned error: %v", err)
	}

	all := index.AllFiles()
	if len(all) != 2 {
		t.Fatalf("AllFiles len = %d, want 2", len(all))
	}
}
