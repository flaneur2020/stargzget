package estargzutil

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type fileReadSeekCloser struct {
	*os.File
}

// loadTestDataLayer loads and parses an estargz layer from testdata
func loadTestDataLayer(t *testing.T, filename string) (*JTOC, io.ReadSeekCloser, func()) {
	t.Helper()

	filePath := filepath.Join("../../testdata", filename)
	file, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open testdata file %s: %v", filename, err)
	}

	// Get file size
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		t.Fatalf("failed to stat testdata file %s: %v", filename, err)
	}

	// Create section reader to parse footer
	sr := io.NewSectionReader(file, 0, stat.Size())
	tocOffset, _, err := OpenFooter(sr)
	if err != nil {
		file.Close()
		t.Fatalf("failed to parse footer from %s: %v", filename, err)
	}

	// Read TOC data
	tocData := make([]byte, stat.Size()-tocOffset)
	if _, err := file.ReadAt(tocData, tocOffset); err != nil {
		file.Close()
		t.Fatalf("failed to read TOC data from %s: %v", filename, err)
	}

	// Parse TOC
	toc, err := ParseTOC(tocData)
	if err != nil {
		file.Close()
		t.Fatalf("failed to parse TOC from %s: %v", filename, err)
	}

	// Reset file position to beginning
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		t.Fatalf("failed to seek to beginning of %s: %v", filename, err)
	}

	cleanup := func() {
		file.Close()
	}

	return toc, &fileReadSeekCloser{file}, cleanup
}

func TestFileReader_WithTestData(t *testing.T) {
	tests := []struct {
		filename string
	}{
		{"000001"},
		{"000002"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			toc, r, cleanup := loadTestDataLayer(t, tt.filename)
			defer cleanup()

			// Find a regular file to test with
			var testFile *TOCEntry
			for _, entry := range toc.Entries {
				if entry.Type == "reg" && entry.Size > 0 {
					testFile = entry
					break
				}
			}

			if testFile == nil {
				t.Skipf("no regular files found in %s", tt.filename)
			}

			reader, err := NewFileReader(toc, testFile.Name, r)
			if err != nil {
				t.Fatalf("failed to create file reader for %s: %v", testFile.Name, err)
			}
			defer reader.Close()

			// Test reading the entire file
			buf := make([]byte, testFile.Size)
			n, err := reader.Read(buf)
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Read error = %v, want io.EOF", err)
			}
			if n != int(testFile.Size) {
				t.Fatalf("Read bytes = %d, want %d", n, testFile.Size)
			}

			// Test seeking to beginning and reading again
			if _, err := reader.Seek(0, io.SeekStart); err != nil {
				t.Fatalf("Seek to start error: %v", err)
			}

			buf2 := make([]byte, testFile.Size)
			n2, err := reader.Read(buf2)
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Read after seek error = %v, want io.EOF", err)
			}
			if n2 != int(testFile.Size) {
				t.Fatalf("Read after seek bytes = %d, want %d", n2, testFile.Size)
			}

			// Verify content is the same
			if !bytes.Equal(buf, buf2) {
				t.Fatalf("Content differs after seek and re-read")
			}
		})
	}
}

func TestFileReader_SeekWithTestData(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()

	// Find a regular file to test with
	var testFile *TOCEntry
	for _, entry := range toc.Entries {
		if entry.Type == "reg" && entry.Size > 100 { // Need a file with some size for seeking
			testFile = entry
			break
		}
	}

	if testFile == nil {
		t.Skip("no suitable regular files found in 000001")
	}

	reader, err := NewFileReader(toc, testFile.Name, r)
	if err != nil {
		t.Fatalf("failed to create file reader for %s: %v", testFile.Name, err)
	}
	defer reader.Close()

	// Test seeking to middle of file
	midPos := testFile.Size / 2
	if _, err := reader.Seek(midPos, io.SeekStart); err != nil {
		t.Fatalf("Seek to middle error: %v", err)
	}

	// Read from middle to end
	remaining := testFile.Size - midPos
	buf := make([]byte, remaining)
	n, err := reader.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read from middle error = %v, want io.EOF", err)
	}
	if n != int(remaining) {
		t.Fatalf("Read from middle bytes = %d, want %d", n, remaining)
	}

	// Test seeking from end
	if _, err := reader.Seek(-10, io.SeekEnd); err != nil {
		t.Fatalf("Seek from end error: %v", err)
	}

	buf2 := make([]byte, 10)
	n2, err := reader.Read(buf2)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read from end error = %v, want io.EOF", err)
	}
	if n2 != 10 {
		t.Fatalf("Read from end bytes = %d, want 10", n2)
	}
}

func TestFileReader_ListFilesInTestData(t *testing.T) {
	tests := []struct {
		filename string
	}{
		{"000001"},
		{"000002"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			toc, r, cleanup := loadTestDataLayer(t, tt.filename)
			defer cleanup()
			defer r.Close()

			// Count different types of entries
			var regFiles, dirs, chunks int
			for _, entry := range toc.Entries {
				switch entry.Type {
				case "reg":
					regFiles++
				case "dir":
					dirs++
				case "chunk":
					chunks++
				}
			}

			t.Logf("Layer %s contains: %d regular files, %d directories, %d chunks",
				tt.filename, regFiles, dirs, chunks)

			// Verify we have some content
			if len(toc.Entries) == 0 {
				t.Fatalf("No entries found in %s", tt.filename)
			}

			// Test reading a few files if they exist
			fileCount := 0
			for _, entry := range toc.Entries {
				if entry.Type == "reg" && entry.Size > 0 && fileCount < 3 {
					// Create a new file handle for each reader
					filePath := filepath.Join("../../testdata", tt.filename)
					file, err := os.Open(filePath)
					if err != nil {
						t.Errorf("failed to open file for %s: %v", entry.Name, err)
						continue
					}

					reader, err := NewFileReader(toc, entry.Name, &fileReadSeekCloser{file})
					if err != nil {
						file.Close()
						t.Errorf("failed to create reader for %s: %v", entry.Name, err)
						continue
					}

					// Read first few bytes
					buf := make([]byte, min(100, entry.Size))
					n, err := reader.Read(buf)
					reader.Close()

					if err != nil && !errors.Is(err, io.EOF) {
						t.Errorf("failed to read from %s: %v", entry.Name, err)
					} else {
						t.Logf("Successfully read %d bytes from %s", n, entry.Name)
					}

					fileCount++
				}
			}
		})
	}
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
