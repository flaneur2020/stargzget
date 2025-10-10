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

// TestFileReader_ReadFullFile tests reading entire files in one go
func TestFileReader_ReadFullFile(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()

	// Test with bin/dash
	reader, err := NewFileReader(toc, "bin/dash", r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}
	defer reader.Close()

	// Get expected file size from TOC
	fileEntries := toc.FileEntries()
	dashEntry := fileEntries["bin/dash"]

	// Read entire file
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read all: %v", err)
	}

	if int64(len(data)) != dashEntry.Size {
		t.Errorf("read %d bytes, expected %d", len(data), dashEntry.Size)
	}

	t.Logf("Successfully read entire file bin/dash: %d bytes", len(data))
}

// TestFileReader_PartialReads tests reading file in small chunks
func TestFileReader_PartialReads(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()

	reader, err := NewFileReader(toc, "bin/dash", r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}
	defer reader.Close()

	fileEntries := toc.FileEntries()
	dashEntry := fileEntries["bin/dash"]

	// Read in 1KB chunks
	var totalRead int64
	buf := make([]byte, 1024)
	for {
		n, err := reader.Read(buf)
		totalRead += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read error: %v", err)
		}
	}

	if totalRead != dashEntry.Size {
		t.Errorf("read %d bytes in chunks, expected %d", totalRead, dashEntry.Size)
	}

	t.Logf("Successfully read file in chunks: %d bytes total", totalRead)
}

// TestFileReader_SeekAndRead tests seeking to various positions and reading
func TestFileReader_SeekAndRead(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()

	reader, err := NewFileReader(toc, "lib/x86_64-linux-gnu/libc-2.24.so", r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}
	defer reader.Close()

	fileEntries := toc.FileEntries()
	fileEntry := fileEntries["lib/x86_64-linux-gnu/libc-2.24.so"]

	tests := []struct {
		name       string
		offset     int64
		whence     int
		want       int64
		resetFirst bool // whether to reset to start before seek
	}{
		{"seek to start", 0, io.SeekStart, 0, false},
		{"seek to 100", 100, io.SeekStart, 100, false},
		{"seek forward 50", 50, io.SeekCurrent, 150, true}, // reset first, then seek to 100, then seek forward 50
		{"seek to end", 0, io.SeekEnd, fileEntry.Size, false},
		{"seek back 100 from end", -100, io.SeekEnd, fileEntry.Size - 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset position if needed
			if tt.resetFirst {
				if _, err := reader.Seek(100, io.SeekStart); err != nil {
					t.Fatalf("Reset seek failed: %v", err)
				}
			}

			pos, err := reader.Seek(tt.offset, tt.whence)
			if err != nil {
				t.Fatalf("Seek failed: %v", err)
			}
			if pos != tt.want {
				t.Errorf("Seek position = %d, want %d", pos, tt.want)
			}

			// Try reading a bit
			if pos < fileEntry.Size {
				buf := make([]byte, 10)
				n, err := reader.Read(buf)
				if err != nil && err != io.EOF {
					t.Errorf("Read after seek failed: %v", err)
				}
				if n > 0 {
					t.Logf("Read %d bytes at position %d", n, pos)
				}
			}
		})
	}
}

// TestFileReader_MultipleFiles tests reading multiple files from same blob
func TestFileReader_MultipleFiles(t *testing.T) {
	tests := []struct {
		filename string
		files    []string
	}{
		{
			"000001",
			[]string{"bin/dash", "lib/x86_64-linux-gnu/libc-2.24.so", "bin/cat"},
		},
		{
			"000002",
			[]string{"etc/ca-certificates.conf", "etc/gss/mech.d/README"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			toc, _, cleanup := loadTestDataLayer(t, tt.filename)
			defer cleanup()

			fileEntries := toc.FileEntries()

			for _, file := range tt.files {
				entry, ok := fileEntries[file]
				if !ok {
					t.Logf("Skipping %s (not in TOC)", file)
					continue
				}

				// Open a new file handle for each reader
				filePath := filepath.Join("../../testdata", tt.filename)
				f, err := os.Open(filePath)
				if err != nil {
					t.Fatalf("failed to open file: %v", err)
				}

				reader, err := NewFileReader(toc, file, &fileReadSeekCloser{f})
				if err != nil {
					f.Close()
					t.Fatalf("failed to create reader for %s: %v", file, err)
				}

				data, err := io.ReadAll(reader)
				reader.Close()

				if err != nil {
					t.Errorf("failed to read %s: %v", file, err)
					continue
				}

				if int64(len(data)) != entry.Size {
					t.Errorf("%s: read %d bytes, expected %d", file, len(data), entry.Size)
				} else {
					t.Logf("Successfully read %s: %d bytes", file, len(data))
				}
			}
		})
	}
}

// TestFileReader_EmptyFile tests reading empty files
func TestFileReader_EmptyFile(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000002")
	defer cleanup()

	// Find an empty file
	var emptyFile string
	for _, entry := range toc.Entries {
		if entry.Type == "reg" && entry.Size == 0 {
			emptyFile = entry.Name
			break
		}
	}

	if emptyFile == "" {
		t.Skip("no empty files in testdata")
	}

	reader, err := NewFileReader(toc, emptyFile, r)
	if err != nil {
		t.Fatalf("failed to create reader for empty file: %v", err)
	}
	defer reader.Close()

	// Reading should immediately return EOF
	buf := make([]byte, 10)
	n, err := reader.Read(buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes, got %d", n)
	}
}

// TestFileReader_SmallFile tests reading very small files (< 100 bytes)
func TestFileReader_SmallFile(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000002")
	defer cleanup()

	// .no.prefetch.landmark is known to be 1 byte
	reader, err := NewFileReader(toc, ".no.prefetch.landmark", r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read small file: %v", err)
	}

	fileEntries := toc.FileEntries()
	entry := fileEntries[".no.prefetch.landmark"]

	if int64(len(data)) != entry.Size {
		t.Errorf("read %d bytes, expected %d", len(data), entry.Size)
	}

	t.Logf("Small file content length: %d", len(data))
}

// TestFileReader_LargeFile tests reading large files
func TestFileReader_LargeFile(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()

	// lib/x86_64-linux-gnu/libc-2.24.so is usually large (>1MB)
	reader, err := NewFileReader(toc, "lib/x86_64-linux-gnu/libc-2.24.so", r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}
	defer reader.Close()

	fileEntries := toc.FileEntries()
	entry := fileEntries["lib/x86_64-linux-gnu/libc-2.24.so"]

	t.Logf("Testing large file: %d bytes, %d chunks", entry.Size, len(entry.Chunks))

	// Read in 64KB chunks
	buf := make([]byte, 64*1024)
	var totalRead int64
	chunkCount := 0

	for {
		n, err := reader.Read(buf)
		totalRead += int64(n)
		chunkCount++

		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read error at chunk %d: %v", chunkCount, err)
		}
	}

	if totalRead != entry.Size {
		t.Errorf("read %d bytes, expected %d", totalRead, entry.Size)
	}

	t.Logf("Read large file in %d chunks, total %d bytes", chunkCount, totalRead)
}

// TestFileReader_ConcurrentReaders tests multiple readers on same blob
func TestFileReader_ConcurrentReaders(t *testing.T) {
	toc, _, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()

	files := []string{"bin/dash", "bin/cat", "bin/ls"}

	// Create separate readers for each file
	var readers []*FileReader
	for _, file := range files {
		filePath := filepath.Join("../../testdata", "000001")
		f, err := os.Open(filePath)
		if err != nil {
			t.Fatalf("failed to open file: %v", err)
		}
		defer f.Close()

		reader, err := NewFileReader(toc, file, &fileReadSeekCloser{f})
		if err != nil {
			t.Fatalf("failed to create reader for %s: %v", file, err)
		}
		defer reader.Close()
		readers = append(readers, reader)
	}

	// Read from all readers
	fileEntries := toc.FileEntries()
	for i, reader := range readers {
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Errorf("failed to read from reader %d: %v", i, err)
			continue
		}

		expectedSize := fileEntries[files[i]].Size
		if int64(len(data)) != expectedSize {
			t.Errorf("reader %d: read %d bytes, expected %d", i, len(data), expectedSize)
		}
	}
}

// TestFileReader_InvalidSeek tests error handling for invalid seeks
func TestFileReader_InvalidSeek(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()

	reader, err := NewFileReader(toc, "bin/dash", r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}
	defer reader.Close()

	// Test negative seek from start
	_, err = reader.Seek(-10, io.SeekStart)
	if err == nil {
		t.Error("expected error for negative seek from start")
	}

	// Test invalid whence
	_, err = reader.Seek(0, 999)
	if err == nil {
		t.Error("expected error for invalid whence")
	}
}

// TestFileReader_NotFound tests error handling for non-existent files
func TestFileReader_NotFound(t *testing.T) {
	toc, r, cleanup := loadTestDataLayer(t, "000001")
	defer cleanup()
	defer r.Close()

	_, err := NewFileReader(toc, "does/not/exist.txt", r)
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}
