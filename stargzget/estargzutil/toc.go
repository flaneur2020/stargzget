package estargzutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
)

const TOCTarName = "stargz.index.json"

// JTOC models the JSON TOC structure embedded in eStargz blobs.
type JTOC struct {
	Version int         `json:"version"`
	Entries []*TOCEntry `json:"entries"`
}

// TOCEntry represents a single entry in the TOC.
type TOCEntry struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Size        int64             `json:"size,omitempty"`
	Offset      int64             `json:"offset,omitempty"`
	ChunkOffset int64             `json:"chunkOffset,omitempty"`
	ChunkSize   int64             `json:"chunkSize,omitempty"`
	InnerOffset int64             `json:"innerOffset,omitempty"`
	ChunkDigest string            `json:"chunkDigest,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ReadTOC streams and decodes a TOC tarball from the provided reader.
func ReadTOC(r io.Reader) (*JTOC, error) {
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate TOC tar archive: %w", err)
		}

		if header.Name != TOCTarName {
			continue
		}

		tocJSONBytes, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read TOC JSON: %w", err)
		}

		var toc JTOC
		if err := json.Unmarshal(tocJSONBytes, &toc); err != nil {
			return nil, fmt.Errorf("failed to unmarshal TOC JSON: %w", err)
		}
		return &toc, nil
	}

	return nil, fmt.Errorf("%s not found in TOC tar archive", TOCTarName)
}

// ParseTOC parses the gzipped TOC tar section and returns the decoded TOC.
func ParseTOC(data []byte) (*JTOC, error) {
	return ReadTOC(bytes.NewReader(data))
}
