package transcriptindex

import (
	"bufio"
	"io"
)

// Record is one JSONL line with its exact byte offsets in the transcript file.
// Offsets include the trailing newline so they are directly usable with
// os.File.Seek; Bytes excludes the newline (and a trailing '\r' if present).
type Record struct {
	Start int64  // offset of the first byte of the line
	End   int64  // offset after the line's trailing newline
	Bytes []byte // raw line bytes, newline excluded
}

// scanTranscript reads r line by line, invoking fn for each non-empty line with
// exact byte offsets. Offsets account for '\n' and '\r\n' terminators so a
// later os.File.Seek(Start) lands at the start of the intended record.
func scanTranscript(r io.Reader, fn func(Record) error) error {
	br := bufio.NewReader(r)
	var offset int64
	for {
		line, err := br.ReadString('\n')
		eof := err == io.EOF

		body := line
		if n := len(body); n > 0 && body[n-1] == '\n' {
			body = body[:n-1]
		}
		if n := len(body); n > 0 && body[n-1] == '\r' {
			body = body[:n-1]
		}

		start := offset
		offset += int64(len(line))
		if len(body) > 0 {
			if ferr := fn(Record{Start: start, End: offset, Bytes: append([]byte(nil), body...)}); ferr != nil {
				return ferr
			}
		}
		if eof {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
