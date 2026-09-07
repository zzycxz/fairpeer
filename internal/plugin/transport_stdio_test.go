package plugin

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

// countingReader yields chunks of 'a' (never a newline) at most max per Read,
// tracking how many bytes were actually pulled from the source. It proves
// readBoundedLine aborts without draining (let alone buffering) an over-cap
// line.
type countingReader struct {
	total    int
	max      int
	consumed int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n := len(p)
	if n > r.max {
		n = r.max
	}
	if n > r.total {
		n = r.total
	}
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	r.total -= n
	r.consumed += n
	return n, nil
}

func TestReadBoundedLineUnderCap(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("hello\nworld\n"))
	line, err := readBoundedLine(r, 1024)
	if err != nil || string(line) != "hello\n" {
		t.Fatalf("first readBoundedLine = %q, %v; want \"hello\\n\", nil", line, err)
	}
	line, err = readBoundedLine(r, 1024)
	if err != nil || string(line) != "world\n" {
		t.Fatalf("second readBoundedLine = %q, %v; want \"world\\n\", nil", line, err)
	}
}

func TestReadBoundedLineEOFWithoutNewline(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("abc"))
	line, err := readBoundedLine(r, 1024)
	if string(line) != "abc" || !errors.Is(err, io.EOF) {
		t.Fatalf("readBoundedLine = %q, %v; want \"abc\", io.EOF", line, err)
	}
}

// TestReadBoundedLineOverCapStreams checks the cap is enforced while reading:
// with a 64-byte cap and a 1 MiB unterminated line delivered in 100-byte
// chunks, the function must fail after a couple of chunks — long before the
// old implementation would have buffered the whole line — and never return data.
func TestReadBoundedLineOverCapStreams(t *testing.T) {
	src := &countingReader{total: 1 << 20, max: 100}
	r := bufio.NewReader(src)
	line, err := readBoundedLine(r, 64)
	if err == nil || !strings.Contains(err.Error(), "exceeded 64-byte limit") {
		t.Fatalf("want over-limit error, got %q, %v", line, err)
	}
	if len(line) != 0 {
		t.Fatalf("over-cap read returned %d bytes of data; want none", len(line))
	}
	// The cap tripped after ~2 chunks; the remaining ~1 MiB was never pulled.
	if src.consumed > 4096 {
		t.Fatalf("read %d source bytes before aborting; the whole line must not be drained (want <= 4096)", src.consumed)
	}
}
