package estargzutil

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestJTOCFileEntries(t *testing.T) {
	toc := &JTOC{
		Entries: []*TOCEntry{
			{
				Name:        "dir/",
				Type:        "dir",
				Size:        0,
				Offset:      0,
				ChunkOffset: 0,
			},
			{
				Name:        "file.txt",
				Type:        "chunk",
				ChunkOffset: 4,
				ChunkSize:   4,
				Offset:      200,
			},
			{
				Name:        "file.txt",
				Type:        "reg",
				Size:        10,
				ChunkOffset: 0,
				ChunkSize:   4,
				Offset:      100,
			},
			{
				Name:        "file.txt",
				Type:        "chunk",
				ChunkOffset: 8,
				ChunkSize:   0, // should be inferred from file size
				Offset:      300,
			},
		},
	}

	fileEntries := toc.FileEntries()
	if len(fileEntries) != 1 {
		t.Fatalf("expected 1 file entry, got %d", len(fileEntries))
	}

	entry, ok := fileEntries["file.txt"]
	if !ok {
		t.Fatalf("expected file entry for file.txt")
	}
	if entry.Size != 10 {
		t.Fatalf("expected size 10, got %d", entry.Size)
	}
	if len(entry.Chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(entry.Chunks))
	}

	if entry.Chunks[0].Offset != 0 || entry.Chunks[0].Size != 4 {
		t.Fatalf("unexpected first chunk %+v", entry.Chunks[0])
	}
	if entry.Chunks[1].Offset != 4 || entry.Chunks[1].Size != 4 {
		t.Fatalf("unexpected second chunk %+v", entry.Chunks[1])
	}
	if entry.Chunks[2].Offset != 8 || entry.Chunks[2].Size != 2 {
		t.Fatalf("unexpected third chunk %+v", entry.Chunks[2])
	}
}

// TestParseTOCFromRealBlob tests parsing TOC from actual blob files
func TestParseTOCFromRealBlob(t *testing.T) {
	tests := []struct {
		filename      string
		wantMinFiles  int
		wantMinDirs   int
		checkFiles    []string // files that should exist
	}{
		{
			filename:     "000001",
			wantMinFiles: 5000,
			wantMinDirs:  700,
			checkFiles:   []string{"bin/dash", "lib/x86_64-linux-gnu/libc-2.24.so"},
		},
		{
			filename:     "000002",
			wantMinFiles: 600,
			wantMinDirs:  180,
			checkFiles:   []string{"etc/ca-certificates.conf", ".no.prefetch.landmark"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			filePath := filepath.Join("../../testdata", tt.filename)
			file, err := os.Open(filePath)
			if err != nil {
				t.Fatalf("failed to open testdata file %s: %v", tt.filename, err)
			}
			defer file.Close()

			// Get file size
			stat, err := file.Stat()
			if err != nil {
				t.Fatalf("failed to stat file: %v", err)
			}

			// Parse footer
			sr := io.NewSectionReader(file, 0, stat.Size())
			tocOffset, _, err := OpenFooter(sr)
			if err != nil {
				t.Fatalf("failed to parse footer: %v", err)
			}

			t.Logf("TOC offset: %d, blob size: %d", tocOffset, stat.Size())

			// Read TOC data
			tocSize := stat.Size() - tocOffset
			tocData := make([]byte, tocSize)
			if _, err := file.ReadAt(tocData, tocOffset); err != nil {
				t.Fatalf("failed to read TOC data: %v", err)
			}

			// Parse TOC
			toc, err := ParseTOC(tocData)
			if err != nil {
				t.Fatalf("failed to parse TOC: %v", err)
			}

			// Verify TOC structure
			if toc.Version == 0 {
				t.Errorf("TOC version is 0, expected non-zero")
			}
			if len(toc.Entries) == 0 {
				t.Fatalf("TOC has no entries")
			}

			// Count entry types
			var regFiles, dirs, chunks, symlinks int
			for _, entry := range toc.Entries {
				switch entry.Type {
				case "reg":
					regFiles++
				case "dir":
					dirs++
				case "chunk":
					chunks++
				case "symlink":
					symlinks++
				}
			}

			t.Logf("TOC stats: %d entries total, %d regular files, %d directories, %d chunks, %d symlinks",
				len(toc.Entries), regFiles, dirs, chunks, symlinks)

			// Verify minimum counts
			if regFiles < tt.wantMinFiles {
				t.Errorf("expected at least %d regular files, got %d", tt.wantMinFiles, regFiles)
			}
			if dirs < tt.wantMinDirs {
				t.Errorf("expected at least %d directories, got %d", tt.wantMinDirs, dirs)
			}

			// Check that specific files exist
			fileEntries := toc.FileEntries()
			for _, checkFile := range tt.checkFiles {
				if _, ok := fileEntries[checkFile]; !ok {
					t.Errorf("expected file %s not found in TOC", checkFile)
				}
			}
		})
	}
}

// TestFileEntriesWithRealBlob tests FileEntries() with real blob data
func TestFileEntriesWithRealBlob(t *testing.T) {
	filePath := filepath.Join("../../testdata", "000001")
	file, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open testdata file: %v", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	sr := io.NewSectionReader(file, 0, stat.Size())
	tocOffset, _, err := OpenFooter(sr)
	if err != nil {
		t.Fatalf("failed to parse footer: %v", err)
	}

	tocData := make([]byte, stat.Size()-tocOffset)
	if _, err := file.ReadAt(tocData, tocOffset); err != nil {
		t.Fatalf("failed to read TOC data: %v", err)
	}

	toc, err := ParseTOC(tocData)
	if err != nil {
		t.Fatalf("failed to parse TOC: %v", err)
	}

	// Test FileEntries aggregation
	fileEntries := toc.FileEntries()
	if len(fileEntries) == 0 {
		t.Fatalf("FileEntries returned empty map")
	}

	t.Logf("FileEntries extracted %d unique files", len(fileEntries))

	// Verify a known file
	dashEntry, ok := fileEntries["bin/dash"]
	if !ok {
		t.Fatalf("bin/dash not found in FileEntries")
	}

	// bin/dash should have size and chunks
	if dashEntry.Size <= 0 {
		t.Errorf("bin/dash has invalid size: %d", dashEntry.Size)
	}
	if len(dashEntry.Chunks) == 0 {
		t.Errorf("bin/dash has no chunks")
	}

	// Verify chunks are sorted by offset
	for i := 1; i < len(dashEntry.Chunks); i++ {
		if dashEntry.Chunks[i].Offset < dashEntry.Chunks[i-1].Offset {
			t.Errorf("chunks not sorted: chunk[%d].Offset=%d < chunk[%d].Offset=%d",
				i, dashEntry.Chunks[i].Offset, i-1, dashEntry.Chunks[i-1].Offset)
		}
	}

	// Verify chunk sizes are set
	for i, chunk := range dashEntry.Chunks {
		if chunk.Size <= 0 {
			t.Errorf("chunk[%d] has invalid size: %d", i, chunk.Size)
		}
		if chunk.CompressedOffset < 0 {
			t.Errorf("chunk[%d] has invalid CompressedOffset: %d", i, chunk.CompressedOffset)
		}
	}
}

// TestChunksForFile tests the ChunksForFile function
func TestChunksForFile(t *testing.T) {
	filePath := filepath.Join("../../testdata", "000001")
	file, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open testdata file: %v", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	sr := io.NewSectionReader(file, 0, stat.Size())
	tocOffset, _, err := OpenFooter(sr)
	if err != nil {
		t.Fatalf("failed to parse footer: %v", err)
	}

	tocData := make([]byte, stat.Size()-tocOffset)
	if _, err := file.ReadAt(tocData, tocOffset); err != nil {
		t.Fatalf("failed to read TOC data: %v", err)
	}

	toc, err := ParseTOC(tocData)
	if err != nil {
		t.Fatalf("failed to parse TOC: %v", err)
	}

	// Test extracting chunks for a known file
	size, chunks, err := ChunksForFile(toc, "bin/dash")
	if err != nil {
		t.Fatalf("ChunksForFile failed: %v", err)
	}

	if size <= 0 {
		t.Errorf("file size should be positive, got %d", size)
	}
	if len(chunks) == 0 {
		t.Errorf("expected chunks for bin/dash")
	}

	// Verify chunks cover the entire file
	var coveredBytes int64
	for _, chunk := range chunks {
		coveredBytes += chunk.Size
	}
	if coveredBytes != size {
		t.Errorf("chunks cover %d bytes but file size is %d", coveredBytes, size)
	}

	// Test non-existent file
	_, _, err = ChunksForFile(toc, "does/not/exist")
	if err == nil {
		t.Errorf("expected error for non-existent file")
	}
}

// TestTOCEntryTypes tests various entry types in TOC
func TestTOCEntryTypes(t *testing.T) {
	filePath := filepath.Join("../../testdata", "000001")
	file, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("failed to open testdata file: %v", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	sr := io.NewSectionReader(file, 0, stat.Size())
	tocOffset, _, err := OpenFooter(sr)
	if err != nil {
		t.Fatalf("failed to parse footer: %v", err)
	}

	tocData := make([]byte, stat.Size()-tocOffset)
	if _, err := file.ReadAt(tocData, tocOffset); err != nil {
		t.Fatalf("failed to read TOC data: %v", err)
	}

	toc, err := ParseTOC(tocData)
	if err != nil {
		t.Fatalf("failed to parse TOC: %v", err)
	}

	// Find examples of each type
	var foundReg, foundDir, foundSymlink bool
	for _, entry := range toc.Entries {
		switch entry.Type {
		case "reg":
			if !foundReg {
				foundReg = true
				// Verify regular file has required fields
				if entry.Size == 0 {
					t.Logf("Warning: regular file %s has zero size", entry.Name)
				}
				if entry.Offset < 0 {
					t.Errorf("regular file %s has negative offset: %d", entry.Name, entry.Offset)
				}
			}
		case "dir":
			if !foundDir {
				foundDir = true
				// Verify directory
				if entry.Name == "" {
					t.Errorf("directory entry has empty name")
				}
			}
		case "symlink":
			if !foundSymlink {
				foundSymlink = true
				t.Logf("Found symlink: %s", entry.Name)
			}
		}
		if foundReg && foundDir && foundSymlink {
			break
		}
	}

	if !foundReg {
		t.Errorf("no regular files found in TOC")
	}
	if !foundDir {
		t.Errorf("no directories found in TOC")
	}
}
