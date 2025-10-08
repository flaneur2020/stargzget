package stargzget

import (
	"context"
	"fmt"
	"io"
	"sync"

	stargzerrors "github.com/flaneur2020/stargz-get/stargzget/errors"
	"github.com/flaneur2020/stargz-get/stargzget/estargzutil"
	stor "github.com/flaneur2020/stargz-get/stargzget/storage"
	"github.com/opencontainers/go-digest"
)

// BlobResolver resolves file metadata and chunk contents using Storage.
type BlobResolver interface {
	FileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error)
	TOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error)
}

// FileMetadata describes a file's size and chunk layout.
type FileMetadata struct {
	Size   int64
	Chunks []Chunk
}

// Chunk represents a logical chunk of file data.
type Chunk struct {
	Offset           int64
	Size             int64
	CompressedOffset int64
	InnerOffset      int64
}

func NewBlobResolver(storage stor.Storage) BlobResolver {
	return &blobResolver{
		storage:  storage,
		tocCache: make(map[digest.Digest]*estargzutil.JTOC),
	}
}

type blobResolver struct {
	storage   stor.Storage
	mu        sync.Mutex
	blobSizes map[digest.Digest]int64
	tocCache  map[digest.Digest]*estargzutil.JTOC
}

func (r *blobResolver) FileMetadata(ctx context.Context, blobDigest digest.Digest, path string) (*FileMetadata, error) {
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

func (r *blobResolver) loadTOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error) {
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

func (r *blobResolver) TOC(ctx context.Context, blobDigest digest.Digest) (*estargzutil.JTOC, error) {
	return r.loadTOC(ctx, blobDigest)
}

func (r *blobResolver) ensureBlobSizes(ctx context.Context) error {
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
