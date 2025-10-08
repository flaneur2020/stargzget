package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/opencontainers/go-digest"
)

// MockStorage is a simple in-memory Storage implementation for tests.
type MockStorage struct {
	mu         sync.RWMutex
	blobs      map[digest.Digest][]byte
	mediaTypes map[digest.Digest]string
}

// NewMockStorage constructs an empty MockStorage.
func NewMockStorage() *MockStorage {
	return &MockStorage{
		blobs:      make(map[digest.Digest][]byte),
		mediaTypes: make(map[digest.Digest]string),
	}
}

// ListBlobs returns descriptors for all stored blobs.
func (m *MockStorage) ListBlobs(ctx context.Context) ([]BlobDescriptor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	descs := make([]BlobDescriptor, 0, len(m.blobs))
	for dgst, data := range m.blobs {
		descs = append(descs, BlobDescriptor{
			Digest:    dgst,
			Size:      int64(len(data)),
			MediaType: m.mediaTypes[dgst],
		})
	}
	return descs, nil
}

// ReadBlob returns a reader over the requested byte range.
func (m *MockStorage) ReadBlob(ctx context.Context, digest digest.Digest, offset int64, length int64) (io.ReadCloser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, ok := m.blobs[digest]
	if !ok {
		return nil, fmt.Errorf("mock storage: blob not found: %s", digest)
	}

	if offset < 0 || offset > int64(len(data)) {
		return nil, fmt.Errorf("mock storage: invalid offset %d for blob %s", offset, digest)
	}

	end := int64(len(data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	slice := data[offset:end]
	return io.NopCloser(bytes.NewReader(slice)), nil
}

// AddBlob adds blob content to the mock storage.
func (m *MockStorage) AddBlob(mediaType string, data []byte) digest.Digest {
	m.mu.Lock()
	defer m.mu.Unlock()

	dgst := digest.FromBytes(data)
	m.blobs[dgst] = append([]byte(nil), data...)
	m.mediaTypes[dgst] = mediaType
	return dgst
}
