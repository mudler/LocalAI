// SPDX-License-Identifier: MIT
package tools

import (
	"sync"
	"time"
	"unicode/utf8"
)

// ring keeps the last bytes a job wrote, stdout and stderr together, with
// absolute offsets: the byte at offset o is the o-th byte the job ever wrote.
// The byte at offset o lives at index o % size, so nothing moves on a write.
type ring struct {
	mu    sync.Mutex
	buf   []byte
	total int64
	last  time.Time
	now   func() time.Time
}

func newRing(size int, now func() time.Time) *ring {
	return &ring{buf: make([]byte, size), now: now, last: now()}
}

// Write keeps p; it never fails, so a job never blocks on its output.
func (r *ring) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	size := int64(len(r.buf))
	src := p
	if int64(len(src)) > size {
		// Only the last size bytes can be kept.
		r.total += int64(len(src)) - size
		src = src[len(src)-int(size):]
	}
	for len(src) > 0 {
		i := r.total % size
		k := copy(r.buf[i:], src)
		src = src[k:]
		r.total += int64(k)
	}
	r.last = r.now()
	return n, nil
}

// state returns the bytes written so far and the time of the last write.
func (r *ring) state() (total int64, last time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total, r.last
}

// page returns at most max bytes from offset since on. An offset that is no
// longer kept moves to the oldest byte kept, past any UTF-8 continuation
// bytes; an offset past the end moves to the end. The page does not end in
// the middle of a UTF-8 character unless the output itself is not UTF-8.
// next is the offset to read on.
func (r *ring) page(since int64, max int) (data []byte, from, next int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := int64(len(r.buf))
	oldest := max64(0, r.total-size)
	from = since
	moved := false
	if from < oldest {
		from, moved = oldest, true
	}
	if from > r.total {
		from = r.total
	}
	if moved {
		for k := 0; k < utf8.UTFMax-1 && from < r.total && !utf8.RuneStart(r.at(from)); k++ {
			from++
		}
	}
	end := min(from+int64(max), r.total)
	data = r.span(from, end)
	if end < r.total {
		data = cutPartialRune(data)
	}
	return data, from, from + int64(len(data))
}

// tail returns the last n bytes kept, starting on a character.
func (r *ring) tail(n int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := int64(len(r.buf))
	from := max64(max64(0, r.total-size), r.total-int64(n))
	for k := 0; k < utf8.UTFMax-1 && from < r.total && !utf8.RuneStart(r.at(from)); k++ {
		from++
	}
	return r.span(from, r.total)
}

// at returns the byte at an absolute offset that is kept. The lock is held.
func (r *ring) at(o int64) byte { return r.buf[o%int64(len(r.buf))] }

// span copies the bytes [from, end), all kept. The lock is held.
func (r *ring) span(from, end int64) []byte {
	out := make([]byte, 0, end-from)
	size := int64(len(r.buf))
	for from < end {
		i := from % size
		j := min(size, i+(end-from))
		out = append(out, r.buf[i:j]...)
		from += j - i
	}
	return out
}

// cutPartialRune drops a character cut at the end of b, when the cut bytes
// are the start of a valid character. Bytes that are not UTF-8 stay.
func cutPartialRune(b []byte) []byte {
	for k := 1; k < utf8.UTFMax && k <= len(b); k++ {
		c := b[len(b)-k]
		if !utf8.RuneStart(c) {
			continue
		}
		if c >= utf8.RuneSelf && !utf8.FullRune(b[len(b)-k:]) {
			return b[:len(b)-k]
		}
		return b
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
