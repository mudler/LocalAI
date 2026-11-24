package bgjob

import (
	"sync"
	"unicode/utf8"
)

// RingSize is the number of output bytes a job keeps.
const RingSize = 256 << 10

// Ring keeps the last RingSize bytes of the output of a job: standard output
// and standard error together, in the order they were written. Offsets are
// absolute: byte n is the n-th byte the job wrote, kept or not, so a reader
// that holds an offset knows exactly what it missed.
type Ring struct {
	mu    sync.Mutex
	size  int
	buf   []byte // the kept bytes are its last min(len, size) bytes
	total int64
}

// NewRing returns a ring that keeps size bytes (RingSize when size is not
// positive).
func NewRing(size int) *Ring {
	if size <= 0 {
		size = RingSize
	}
	return &Ring{size: size}
}

// Write appends p. It never fails and never blocks on a reader.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total += int64(len(p))
	r.buf = append(r.buf, p...)
	// The buffer may grow to twice the size before it is cut, so a chatty job
	// does not move 256 KiB on every write.
	if len(r.buf) > 2*r.size {
		n := copy(r.buf, r.buf[len(r.buf)-r.size:])
		r.buf = r.buf[:n]
	}
	return len(p), nil
}

// Total is the number of bytes written since the start.
func (r *Ring) Total() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

// Dropped is the number of bytes that are no longer kept.
func (r *Ring) Dropped() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total - int64(r.keptLocked())
}

func (r *Ring) keptLocked() int { return min(len(r.buf), r.size) }

// Page returns at most max bytes from the offset since. An offset that is no
// longer kept moves to the oldest byte kept, and an offset past the end to the
// end. The page never starts or ends in the middle of a UTF-8 character: a
// lost offset skips the rest of a cut character, and a page that would cut one
// ends before it. from is the offset of the first byte of out and next the
// offset to read on from.
func (r *Ring) Page(since int64, max int) (out []byte, from, next int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.keptLocked()
	data := r.buf[len(r.buf)-kept:]
	oldest := r.total - int64(kept)
	from = since
	if from < oldest {
		from = oldest
		// A character the ring cut in two: its tail is not text.
		for from < r.total && !utf8.RuneStart(data[from-oldest]) {
			from++
		}
	}
	if from > r.total {
		from = r.total
	}
	start := int(from - oldest)
	end := len(data)
	if max > 0 && end-start > max {
		end = start + max
		// Never end inside a character: the next byte must start one.
		for end > start && end < len(data) && !utf8.RuneStart(data[end]) {
			end--
		}
	}
	out = append([]byte(nil), data[start:end]...)
	return out, from, from + int64(len(out))
}

// Tail returns the last n bytes, starting at the beginning of a character.
func (r *Ring) Tail(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.keptLocked()
	data := r.buf[len(r.buf)-kept:]
	if n < len(data) {
		data = data[len(data)-n:]
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			data = data[1:]
		}
	}
	return string(data)
}
