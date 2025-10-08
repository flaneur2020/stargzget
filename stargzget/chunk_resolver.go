package stargzget

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"sync"

	stargzerrors "github.com/flaneur2020/stargz-get/stargzget/errors"
	"github.com/flaneur2020/stargz-get/stargzget/estargzutil"
	"github.com/opencontainers/go-digest"
)

// ChunkResolver resolves file metadata and chunk contents using Storage.
type ChunkResolver interface {
	FileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error)
	ReadChunk(ctx context.Context, blobDigest digest.Digest, path string, chunk Chunk) ([]byte, error)
	TOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error)
}

func NewChunkResolver(storage Storage) ChunkResolver {
	return &chunkResolver{
		storage:  storage,
		tocCache: make(map[digest.Digest]*estargzutil.JTOC),
	}
}

type chunkResolver struct {
	storage   Storage
	mu        sync.Mutex
	blobSizes map[digest.Digest]int64
	tocCache  map[digest.Digest]*estargzutil.JTOC
}

func (r *chunkResolver) FileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error) {
	toc, err := r.loadTOC(ctx, blobDigest)
	if err != nil {
		return nil, err
	}

	size, chunks, err := estargzutil.ChunksForFile(toc, path)
	if err != nil {
		return nil, err
	}

	result := &FileMetadata{
		Size:   size,
		Chunks: make([]Chunk, len(chunks)),
	}

	for i, ch := range chunks {
		result.Chunks[i] = Chunk{
			Offset:           ch.Offset,
			Size:             ch.Size,
			CompressedOffset: ch.CompressedOffset,
			InnerOffset:      ch.InnerOffset,
		}
	}

	return result, nil
}

func (r *chunkResolver) ReadChunk(ctx context.Context, blobDigest digest.Digest, path string, chunk Chunk) ([]byte, error) {
	reader, err := r.storage.ReadBlob(ctx, blobDigest, chunk.CompressedOffset, 0)
	if err != nil {
		return nil, stargzerrors.ErrDownloadFailed.WithCause(err)
	}
	defer reader.Close()

	gz, err := gzip.NewReader(reader)
	if err != nil {
		return nil, stargzerrors.ErrDownloadFailed.WithCause(err)
	}
	defer gz.Close()

	if chunk.InnerOffset > 0 {
		if _, err := io.CopyN(io.Discard, gz, chunk.InnerOffset); err != nil {
			return nil, stargzerrors.ErrDownloadFailed.WithCause(err)
		}
	}

	buf := make([]byte, chunk.Size)
	n, err := io.ReadFull(gz, buf)
	if err != nil && err != io.EOF {
		return nil, stargzerrors.ErrDownloadFailed.WithCause(err)
	}
	if int64(n) != chunk.Size {
		return nil, stargzerrors.ErrDownloadFailed.WithCause(io.ErrUnexpectedEOF)
	}

	return buf, nil
}

func (r *chunkResolver) loadTOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error) {
	r.mu.Lock()
	if toc, ok := r.tocCache[blobDigest]; ok {
		r.mu.Unlock()
		return toc, nil
	}
	r.mu.Unlock()

	if err := r.ensureBlobSizes(ctx); err != nil {
		return nil, err
	}

	size, ok := r.blobSizes[blobDigest]
	if !ok {
		return nil, fmt.Errorf("unknown blob: %s", blobDigest)
	}

	footerLength := int64(estargzutil.FooterSize)
	if size < footerLength {
		footerLength = size
	}

	footerReader, err := r.storage.ReadBlob(ctx, blobDigest, size-footerLength, footerLength)
	if err != nil {
		return nil, stargzerrors.ErrTOCDownload.WithDetail("blobDigest", blobDigest.String()).WithCause(err)
	}
	footerBytes, err := io.ReadAll(footerReader)
	footerReader.Close()
	if err != nil {
		return nil, stargzerrors.ErrTOCDownload.WithDetail("blobDigest", blobDigest.String()).WithCause(err)
	}

	tocOffset, footerSize, err := estargzutil.ParseFooter(footerBytes)
	if err != nil {
		return nil, stargzerrors.ErrTOCDownload.WithDetail("blobDigest", blobDigest.String()).WithCause(err)
	}

	tocStart := tocOffset
	tocLength := size - tocOffset
	if tocLength <= 0 {
		return nil, stargzerrors.ErrTOCDownload.WithDetail("blobDigest", blobDigest.String()).WithCause(fmt.Errorf("invalid TOC length"))
	}

	reader, err := r.storage.ReadBlob(ctx, blobDigest, tocStart, tocLength+footerSize)
	if err != nil {
		return nil, stargzerrors.ErrTOCDownload.WithDetail("blobDigest", blobDigest.String()).WithCause(err)
	}
	defer reader.Close()

	tocBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, stargzerrors.ErrTOCDownload.WithDetail("blobDigest", blobDigest.String()).WithCause(err)
	}

	toc, err := estargzutil.ParseTOC(tocBytes)
	if err != nil {
		return nil, stargzerrors.ErrTOCDownload.WithDetail("blobDigest", blobDigest.String()).WithCause(err)
	}

	r.mu.Lock()
	r.tocCache[blobDigest] = toc
	r.mu.Unlock()

	return toc, nil
}

func (r *chunkResolver) TOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error) {
	return r.loadTOC(ctx, blobDigest)
}

func (r *chunkResolver) ensureBlobSizes(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.blobSizes != nil {
		return nil
	}

	blobs, err := r.storage.ListBlobs(ctx)
	if err != nil {
		return err
	}

	r.blobSizes = make(map[digest.Digest]int64, len(blobs))
	for _, blob := range blobs {
		r.blobSizes[blob.Digest] = blob.Size
	}
	return nil
}
