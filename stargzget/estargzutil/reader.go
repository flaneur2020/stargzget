package estargzutil

import "io"

type FileReader struct {
	chunks []Chunk
	r      io.ReadSeekCloser
}

var _ io.ReadSeekCloser = (*FileReader)(nil)

func NewFileReader(chunks []Chunk, r io.ReadSeekCloser) *FileReader {
	return &FileReader{
		r:      r,
		chunks: chunks,
	}
}

func (f *FileReader) Read(p []byte) (int, error) {
	// TODO
	return 0, nil
}

func (f *FileReader) Seek(offset int64, whence int) (int64, error) {
	// TODO
	return 0, nil
}

func (f *FileReader) Close() error {
	// TODO
	return nil
}
