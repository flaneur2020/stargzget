package estargzutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	TOCTarName = "stargz.index.json"

	FooterSize       = 51
	legacyFooterSize = 47
)

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

// OpenFooter extracts the TOC offset from an eStargz footer. It supports both
// the modern and legacy footer layouts used by containerd's stargz snapshotter.
func OpenFooter(sr *io.SectionReader) (tocOffset int64, footerSize int64, err error) {
	size := sr.Size()
	if size < FooterSize && size < legacyFooterSize {
		return 0, 0, fmt.Errorf("blob size %d is smaller than the footer size", size)
	}

	footerBuf := make([]byte, FooterSize)
	if _, err := sr.ReadAt(footerBuf, size-FooterSize); err != nil {
		return 0, 0, fmt.Errorf("failed to read footer: %w", err)
	}

	if tocOffset, err = parseFooter(footerBuf, false); err == nil {
		return tocOffset, FooterSize, nil
	}

	if tocOffset, err = parseFooter(footerBuf[FooterSize-legacyFooterSize:], true); err == nil {
		return tocOffset, legacyFooterSize, nil
	}

	return 0, 0, fmt.Errorf("failed to parse stargz footer")
}

// ParseTOC parses the gzipped TOC tar section and returns the decoded TOC.
func ParseTOC(data []byte) (*JTOC, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(data))
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

func parseFooter(p []byte, legacy bool) (int64, error) {
	zr, err := gzip.NewReader(bytes.NewReader(p))
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	extra := zr.Extra
	if legacy {
		if len(extra) != 16+len("STARGZ") {
			return 0, fmt.Errorf("legacy footer has invalid extra field")
		}
		if string(extra[16:]) != "STARGZ" {
			return 0, fmt.Errorf("legacy footer missing STARGZ marker")
		}
		return parseHex(extra[:16])
	}

	if len(extra) < 4 {
		return 0, fmt.Errorf("footer extra field truncated")
	}
	if extra[0] != 'S' || extra[1] != 'G' {
		return 0, fmt.Errorf("footer missing SG header")
	}
	length := binary.LittleEndian.Uint16(extra[2:4])
	if int(length) != 16+len("STARGZ") {
		return 0, fmt.Errorf("unexpected footer extra length %d", length)
	}
	if len(extra) < 4+int(length) {
		return 0, fmt.Errorf("footer extra shorter than length")
	}
	payload := extra[4 : 4+int(length)]
	if string(payload[16:]) != "STARGZ" {
		return 0, fmt.Errorf("footer missing STARGZ marker")
	}
	return parseHex(payload[:16])
}

func parseHex(b []byte) (int64, error) {
	var v int64
	for _, c := range b {
		v <<= 4
		switch {
		case '0' <= c && c <= '9':
			v |= int64(c - '0')
		case 'a' <= c && c <= 'f':
			v |= int64(c-'a') + 10
		case 'A' <= c && c <= 'F':
			v |= int64(c-'A') + 10
		default:
			return 0, fmt.Errorf("invalid hex digit %q", c)
		}
	}
	return v, nil
}
