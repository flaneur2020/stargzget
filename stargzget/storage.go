package stargzget

import (
	"context"
	"io"

	"github.com/opencontainers/go-digest"
)

// BlobDescriptor describes a blob available in storage.
type BlobDescriptor struct {
	Digest    digest.Digest
	Size      int64
	MediaType string
}

// Storage abstracts blob enumeration and ranged reads.
type Storage interface {
	ListBlobs(ctx context.Context) ([]BlobDescriptor, error)
	ReadBlob(ctx context.Context, digest digest.Digest, offset int64, length int64) (io.ReadCloser, error)
}
