package stargzget

import (
	"context"

	"github.com/opencontainers/go-digest"
)

type Downloader interface {
	StartDownload(ctx context.Context, blobDigest digest.Digest, fileName string, targetPath string) (*DownloadTask, error)
}

type DownloadTask struct {
	Chunks              []Chunk
	DownloadedChunkIdxs []uint64
}
