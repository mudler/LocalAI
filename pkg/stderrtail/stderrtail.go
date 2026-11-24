// Package stderrtail keeps the last bytes a child process wrote to its error
// stream, so that a failure to start or to answer can tell the user why.
//
// A Buffer is an io.Writer for exec.Cmd.Stderr. It never grows past its
// limit, so a server that logs without end costs a fixed amount of memory.
package stderrtail

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// DefaultSize is the number of bytes a Buffer keeps when New gets no size.
const DefaultSize = 4096

// Marker is the line that introduces the output in an error message. The
// first line of the message stays the original error.
const Marker = "command output:"

// Buffer keeps the last bytes written to it. It is safe for concurrent use.
type Buffer struct {
	mu      sync.Mutex
	max     int
	buf     []byte
	written int // bytes written in all, to tell when output is still arriving
}

// New returns a Buffer that keeps the last size bytes. A size that is not
// positive gives DefaultSize.
func New(size int) *Buffer {
	if size <= 0 {
		size = DefaultSize
	}
	return &Buffer{max: size}
}

// Write keeps the last bytes of p. It never fails and never blocks on the
// size of the output.
func (b *Buffer) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.written += n
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		return n, nil
	}
	if over := len(b.buf) + len(p) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

// Text returns what the buffer holds, ready to show to a person: invalid
// UTF-8 (a cut in the middle of a character), terminal escape sequences and
// control characters are removed, and the ends are trimmed. A nil Buffer
// holds nothing.
func (b *Buffer) Text() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	raw := string(b.buf)
	b.mu.Unlock()
	return Clean(raw)
}

// Settle waits until nothing has been written for a short moment, but at
// most for limit. The pipe of a child is copied by a goroutine, so right
// after the child fails its last lines may not be in the buffer yet.
func (b *Buffer) Settle(limit time.Duration) {
	if b == nil {
		return
	}
	const quiet = 20 * time.Millisecond
	deadline := time.Now().Add(limit)
	b.mu.Lock()
	last := b.written
	b.mu.Unlock()
	for time.Now().Before(deadline) {
		time.Sleep(quiet)
		b.mu.Lock()
		now := b.written
		b.mu.Unlock()
		if now == last {
			return
		}
		last = now
	}
}

var (
	oscSeq = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	csiSeq = regexp.MustCompile("\x1b\\[[0-9;?<=>]*[ -/]*[@-~]")
)

// Clean makes text from a child process safe to show: it removes terminal
// escape sequences, invalid UTF-8 and control characters other than newline
// and tab, turns carriage returns into nothing, and trims the ends.
func Clean(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = oscSeq.ReplaceAllString(s, "")
	s = csiSeq.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// RedactValues replaces every value of secrets that has at least three
// characters with "***". Shorter values would hide too much of the text.
func RedactValues(s string, secrets map[string]string) string {
	for _, v := range secrets {
		if len(v) >= 3 {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	return s
}

// Error is an error with the output the child process wrote. Its message is
// the message of Err, then a line with Marker and the output.
type Error struct {
	Err    error
	Output string
}

func (e *Error) Error() string {
	if e.Output == "" {
		return e.Err.Error()
	}
	return e.Err.Error() + "\n" + Marker + "\n" + e.Output
}

func (e *Error) Unwrap() error { return e.Err }

// Wrap returns err with output attached. Empty output gives err back as it
// is, and so does a nil err.
func Wrap(err error, output string) error {
	if err == nil || output == "" {
		return err
	}
	return &Error{Err: err, Output: output}
}

// Split returns the message of err without the output, and the output.
func Split(err error) (msg, output string) {
	var e *Error
	if errors.As(err, &e) {
		return strings.TrimSuffix(err.Error(), "\n"+Marker+"\n"+e.Output), e.Output
	}
	return err.Error(), ""
}
