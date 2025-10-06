package stargzget

import (
	"context"

	"github.com/opencontainers/go-digest"
)

type BlobAccessor interface {
	ListFiles(ctx context.Context, blobDigest digest.Digest) ([]string, error)

	GetFileMetadata(ctx context.Context, blobDigest digest.Digest, fileName string) (*FileMetadata, error)
}

type FileMetadata struct {
	Size   int64
	Chunks []Chunk
}

type Chunk struct {
	Offset int64
	Size   int64
}
