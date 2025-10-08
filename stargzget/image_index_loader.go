package stargzget

import (
	"context"
	"fmt"
	"strings"

	stargzerrors "github.com/flaneur2020/stargz-get/stargzget/errors"
	"github.com/flaneur2020/stargz-get/stargzget/logger"
	stor "github.com/flaneur2020/stargz-get/stargzget/storage"
	"github.com/opencontainers/go-digest"
)

type ImageIndexLoader interface {
	Load(ctx context.Context) (*ImageIndex, error)
}

type imageIndexLoader struct {
	storage  stor.Storage
	resolver ChunkResolver
}

func NewImageIndexLoader(storage stor.Storage, resolver ChunkResolver) ImageIndexLoader {
	return &imageIndexLoader{
		storage:  storage,
		resolver: resolver,
	}
}

func (l *imageIndexLoader) Load(ctx context.Context) (*ImageIndex, error) {
	blobs, err := l.storage.ListBlobs(ctx)
	if err != nil {
		return nil, err
	}

	if err := validateBlobDescriptors(blobs); err != nil {
		return nil, err
	}

	index := &ImageIndex{
		Layers: make([]*LayerInfo, 0, len(blobs)),
		files:  make(map[string]*FileInfo),
	}

	for _, blob := range blobs {
		toc, err := l.resolver.TOC(ctx, blob.Digest)
		if err != nil {
			logger.Warn("Skipping blob %s: %v", blob.Digest.String(), err)
			continue
		}

		layerInfo := &LayerInfo{
			BlobDigest: blob.Digest,
			Files:      make([]string, 0, len(toc.Entries)),
			FileSizes:  make(map[string]int64),
		}

		for _, entry := range toc.Entries {
			if entry.Type != "reg" {
				continue
			}

			layerInfo.Files = append(layerInfo.Files, entry.Name)
			layerInfo.FileSizes[entry.Name] = entry.Size
			index.files[entry.Name] = &FileInfo{
				Path:       entry.Name,
				BlobDigest: blob.Digest,
				Size:       entry.Size,
			}
		}

		index.Layers = append(index.Layers, layerInfo)
	}

	return index, nil
}

type FileInfo struct {
	Path       string
	BlobDigest digest.Digest
	Size       int64
}

type LayerInfo struct {
	BlobDigest digest.Digest
	Files      []string
	FileSizes  map[string]int64
}

type ImageIndex struct {
	Layers []*LayerInfo
	files  map[string]*FileInfo
}

func (idx *ImageIndex) AllFiles() []string {
	paths := make([]string, 0, len(idx.files))
	for path := range idx.files {
		paths = append(paths, path)
	}
	return paths
}

func (idx *ImageIndex) FindFile(path string, blobDigest digest.Digest) (*FileInfo, error) {
	if blobDigest.String() == "" {
		info, ok := idx.files[path]
		if !ok {
			return nil, stargzerrors.ErrFileNotFound.WithDetail("path", path)
		}
		return info, nil
	}

	for _, layer := range idx.Layers {
		if layer.BlobDigest == blobDigest {
			if size, ok := layer.FileSizes[path]; ok {
				return &FileInfo{
					Path:       path,
					BlobDigest: blobDigest,
					Size:       size,
				}, nil
			}
			return nil, stargzerrors.ErrFileNotFound.WithDetail("path", path).WithDetail("blobDigest", blobDigest.String())
		}
	}
	return nil, stargzerrors.ErrBlobNotFound.WithDetail("blobDigest", blobDigest.String())
}

func (idx *ImageIndex) FilterFiles(pathPattern string, blobDigest digest.Digest) []*FileInfo {
	matcher := newPathMatcher(pathPattern)
	var results []*FileInfo

	if blobDigest == "" {
		for _, info := range idx.files {
			if matcher.matches(info.Path) {
				results = append(results, info)
			}
		}
		return results
	}

	for _, layer := range idx.Layers {
		if layer.BlobDigest != blobDigest {
			continue
		}
		for _, filePath := range layer.Files {
			if matcher.matches(filePath) {
				results = append(results, &FileInfo{
					Path:       filePath,
					BlobDigest: layer.BlobDigest,
					Size:       layer.FileSizes[filePath],
				})
			}
		}
	}
	return results
}

type pathMatcher struct {
	matchAll  bool
	pattern   string
	dirPrefix bool
}

func newPathMatcher(pattern string) pathMatcher {
	if pattern == "." || pattern == "/" || pattern == "" {
		return pathMatcher{matchAll: true}
	}

	dirPrefix := strings.HasSuffix(pattern, "/")
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}

	return pathMatcher{
		pattern:   pattern,
		dirPrefix: dirPrefix,
	}
}

func (m pathMatcher) matches(path string) bool {
	if m.matchAll {
		return true
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if m.dirPrefix {
		return strings.HasPrefix(path, m.pattern)
	}

	return path == m.pattern || strings.HasPrefix(path, m.pattern+"/")
}

func validateBlobDescriptors(blobs []stor.BlobDescriptor) error {
	if len(blobs) == 0 {
		return fmt.Errorf("no blobs found in storage")
	}
	return nil
}
