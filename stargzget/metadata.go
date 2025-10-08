package stargzget

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
