// zstd_reader.go adapts the pure-Go klauspost zstd decoder to the session
// log reader (harness web artifacts are .jsonl.zstd; design §2.2). The
// dependency stays isolated to this file.
package dsh

import (
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// newZstdReader builds a single-threaded decoder: listing/history reads are
// latency-insensitive and never contended enough to want worker goroutines.
func newZstdReader(r io.Reader) (*zstd.Decoder, error) {
	return zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
}

// zstdLogReader pairs the decoder with its underlying file so one Close
// releases both.
type zstdLogReader struct {
	file *os.File
	zr   *zstd.Decoder
}

func (z *zstdLogReader) Read(p []byte) (int, error) { return z.zr.Read(p) }

func (z *zstdLogReader) Close() error {
	z.zr.Close()
	return z.file.Close()
}
