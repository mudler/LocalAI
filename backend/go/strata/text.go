package main

import (
	"strings"
	"unicode/utf8"
)

// utf8Joiner turns token bytes into text. Byte-level BPE can split a
// multi-byte character across tokens, so the incomplete tail is held back
// until the next token completes it.
type utf8Joiner struct{ pending []byte }

// write adds bytes and returns the longest prefix that is whole characters.
func (j *utf8Joiner) write(b []byte) string {
	j.pending = append(j.pending, b...)
	n := len(j.pending)
	// Hold back at most 3 trailing bytes that start an incomplete sequence.
	cut := n
	for i := 1; i <= 3 && i <= n; i++ {
		c := j.pending[n-i]
		if utf8.RuneStart(c) {
			if c >= 0xC0 && !utf8.FullRune(j.pending[n-i:]) {
				cut = n - i
			}
			break
		}
	}
	out := string(j.pending[:cut])
	j.pending = append(j.pending[:0], j.pending[cut:]...)
	return out
}

// flush returns whatever is left, invalid bytes included.
func (j *utf8Joiner) flush() string {
	s := string(j.pending)
	j.pending = j.pending[:0]
	return s
}

// stopMatcher cuts the stream at the first stop string. It holds back the
// last len(longest stop)-1 bytes so a stop string split across tokens is
// still found.
type stopMatcher struct {
	stops []string
	buf   string
	hold  int
	hit   bool
}

func newStopMatcher(stops []string) *stopMatcher {
	m := &stopMatcher{}
	for _, s := range stops {
		if s == "" {
			continue
		}
		m.stops = append(m.stops, s)
		if len(s)-1 > m.hold {
			m.hold = len(s) - 1
		}
	}
	return m
}

// push adds text and returns what is safe to emit. After a hit, push returns "".
func (m *stopMatcher) push(s string) string {
	if m.hit {
		return ""
	}
	if len(m.stops) == 0 {
		return s
	}
	m.buf += s
	best := -1
	for _, st := range m.stops {
		if i := strings.Index(m.buf, st); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best >= 0 {
		m.hit = true
		out := m.buf[:best]
		m.buf = ""
		return out
	}
	if len(m.buf) <= m.hold {
		return ""
	}
	cut := len(m.buf) - m.hold
	// do not split a character
	for cut > 0 && !utf8.RuneStart(m.buf[cut]) {
		cut--
	}
	out := m.buf[:cut]
	m.buf = m.buf[cut:]
	return out
}

// flush returns the held-back tail when the stream ends without a hit.
func (m *stopMatcher) flush() string {
	if m.hit {
		return ""
	}
	out := m.buf
	m.buf = ""
	return out
}
