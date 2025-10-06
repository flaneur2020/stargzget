package stargzget

import (
	"testing"

	"github.com/opencontainers/go-digest"
)

func TestImageIndex_FilterFiles(t *testing.T) {
	// Create a mock image index
	digest1 := digest.FromString("layer1")
	digest2 := digest.FromString("layer2")

	index := &ImageIndex{
		Layers: []*LayerInfo{
			{
				BlobDigest: digest1,
				Files:      []string{"bin/echo", "bin/cat", "bin/ls", "lib/libc.so"},
				FileSizes: map[string]int64{
					"bin/echo":   100,
					"bin/cat":    200,
					"bin/ls":     300,
					"lib/libc.so": 400,
				},
			},
			{
				BlobDigest: digest2,
				Files:      []string{"usr/bin/python", "usr/lib/python.so", "etc/config"},
				FileSizes: map[string]int64{
					"usr/bin/python":  1000,
					"usr/lib/python.so": 2000,
					"etc/config":      500,
				},
			},
		},
		files: map[string]*FileInfo{
			"bin/echo": {Path: "bin/echo", BlobDigest: digest1, Size: 100},
			"bin/cat":  {Path: "bin/cat", BlobDigest: digest1, Size: 200},
			"bin/ls":   {Path: "bin/ls", BlobDigest: digest1, Size: 300},
			"lib/libc.so": {Path: "lib/libc.so", BlobDigest: digest1, Size: 400},
			"usr/bin/python":  {Path: "usr/bin/python", BlobDigest: digest2, Size: 1000},
			"usr/lib/python.so": {Path: "usr/lib/python.so", BlobDigest: digest2, Size: 2000},
			"etc/config": {Path: "etc/config", BlobDigest: digest2, Size: 500},
		},
	}

	tests := []struct {
		name        string
		pathPattern string
		blobDigest  digest.Digest
		wantCount   int
		wantPaths   []string
	}{
		{
			name:        "match all files",
			pathPattern: "",
			blobDigest:  "",
			wantCount:   7,
			wantPaths:   []string{"bin/echo", "bin/cat", "bin/ls", "lib/libc.so", "usr/bin/python", "usr/lib/python.so", "etc/config"},
		},
		{
			name:        "match all files with dot",
			pathPattern: ".",
			blobDigest:  "",
			wantCount:   7,
			wantPaths:   []string{"bin/echo", "bin/cat", "bin/ls", "lib/libc.so", "usr/bin/python", "usr/lib/python.so", "etc/config"},
		},
		{
			name:        "match directory bin/",
			pathPattern: "bin/",
			blobDigest:  "",
			wantCount:   3,
			wantPaths:   []string{"bin/echo", "bin/cat", "bin/ls"},
		},
		{
			name:        "match directory bin without slash",
			pathPattern: "bin",
			blobDigest:  "",
			wantCount:   3,
			wantPaths:   []string{"bin/echo", "bin/cat", "bin/ls"},
		},
		{
			name:        "match specific file",
			pathPattern: "bin/echo",
			blobDigest:  "",
			wantCount:   1,
			wantPaths:   []string{"bin/echo"},
		},
		{
			name:        "match usr/bin directory",
			pathPattern: "usr/bin",
			blobDigest:  "",
			wantCount:   1,
			wantPaths:   []string{"usr/bin/python"},
		},
		{
			name:        "match with specific blob digest",
			pathPattern: "bin/",
			blobDigest:  digest1,
			wantCount:   3,
			wantPaths:   []string{"bin/echo", "bin/cat", "bin/ls"},
		},
		{
			name:        "match with specific blob digest - layer 2",
			pathPattern: "",
			blobDigest:  digest2,
			wantCount:   3,
			wantPaths:   []string{"usr/bin/python", "usr/lib/python.so", "etc/config"},
		},
		{
			name:        "no match - wrong directory",
			pathPattern: "nonexistent/",
			blobDigest:  "",
			wantCount:   0,
			wantPaths:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := index.FilterFiles(tt.pathPattern, tt.blobDigest)

			if len(results) != tt.wantCount {
				t.Errorf("FilterFiles() returned %d files, want %d", len(results), tt.wantCount)
			}

			// Check that all expected paths are present
			resultPaths := make(map[string]bool)
			for _, r := range results {
				resultPaths[r.Path] = true
			}

			for _, wantPath := range tt.wantPaths {
				if !resultPaths[wantPath] {
					t.Errorf("FilterFiles() missing expected path: %s", wantPath)
				}
			}
		})
	}
}

func TestImageIndex_FindFile(t *testing.T) {
	digest1 := digest.FromString("layer1")
	digest2 := digest.FromString("layer2")

	index := &ImageIndex{
		Layers: []*LayerInfo{
			{
				BlobDigest: digest1,
				Files:      []string{"bin/echo"},
				FileSizes: map[string]int64{
					"bin/echo": 100,
				},
			},
			{
				BlobDigest: digest2,
				Files:      []string{"bin/cat"},
				FileSizes: map[string]int64{
					"bin/cat": 200,
				},
			},
		},
		files: map[string]*FileInfo{
			"bin/echo": {Path: "bin/echo", BlobDigest: digest1, Size: 100},
			"bin/cat":  {Path: "bin/cat", BlobDigest: digest2, Size: 200},
		},
	}

	tests := []struct {
		name       string
		path       string
		blobDigest digest.Digest
		wantErr    bool
		wantSize   int64
	}{
		{
			name:       "find file without blob digest",
			path:       "bin/echo",
			blobDigest: "",
			wantErr:    false,
			wantSize:   100,
		},
		{
			name:       "find file with correct blob digest",
			path:       "bin/echo",
			blobDigest: digest1,
			wantErr:    false,
			wantSize:   100,
		},
		{
			name:       "find file with wrong blob digest",
			path:       "bin/echo",
			blobDigest: digest2,
			wantErr:    true,
		},
		{
			name:       "file not found",
			path:       "nonexistent",
			blobDigest: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := index.FindFile(tt.path, tt.blobDigest)

			if tt.wantErr {
				if err == nil {
					t.Errorf("FindFile() expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("FindFile() unexpected error: %v", err)
				return
			}

			if result.Size != tt.wantSize {
				t.Errorf("FindFile() size = %d, want %d", result.Size, tt.wantSize)
			}
		})
	}
}
