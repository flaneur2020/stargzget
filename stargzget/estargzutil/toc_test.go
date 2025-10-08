package estargzutil

import "testing"

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
