package functions

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode"
)

// legacyMark is a point the legacy JSON scan can resume from: the offset of
// its next step, how many objects the steps before it found, and the end of
// the bytes those steps read.
type legacyMark struct {
	offset int
	objs   int
	extent int
}

// legacyMarkEvery is the distance between the marks kept for rewind.
const legacyMarkEvery = 512

// legacyJSONScan returns parseJSONLegacy of an output that grows, without
// reading the whole output again for each token.
//
// Each step of parseJSONLegacy starts at an offset and decides from the bytes
// that follow it: a byte to skip, or what the decoder makes of the next value.
// When every byte a step read is in the output, and one byte more, bytes
// written later cannot change that step: the decoder looks one byte past a
// value to find its end, and no further. So the scan keeps the objects and the
// offset after its last such step, and the next call starts there.
type legacyJSONScan struct {
	objs  []map[string]any // the objects found before cur.offset
	cur   legacyMark
	marks []legacyMark // earlier points, oldest first, kept only for rewind
	// keepMarks keeps the marks, for an output whose bytes can change.
	keepMarks bool
}

func (l *legacyJSONScan) reset() {
	*l = legacyJSONScan{keepMarks: l.keepMarks}
}

// rewind forgets the steps that read a byte at pos or after, for an output
// whose bytes from pos on changed.
func (l *legacyJSONScan) rewind(pos int) {
	for l.cur.extent > pos {
		if len(l.marks) == 0 {
			l.cur = legacyMark{}
			break
		}
		l.cur = l.marks[len(l.marks)-1]
		l.marks = l.marks[:len(l.marks)-1]
	}
	// A new array: results returned before share the old one.
	l.objs = slices.Clone(l.objs[:l.cur.objs])
}

// stable returns how many leading objects of the last scan no later write
// changes, short of a rewind.
func (l *legacyJSONScan) stable() int { return len(l.objs) }

func (l *legacyJSONScan) advance(next, extent int, obj map[string]any, found bool) {
	if found {
		l.objs = append(l.objs, obj)
	}
	l.cur = legacyMark{offset: next, objs: len(l.objs), extent: extent}
	if !l.keepMarks {
		return
	}
	last := 0
	if len(l.marks) > 0 {
		last = l.marks[len(l.marks)-1].offset
	}
	if l.cur.offset-last >= legacyMarkEvery {
		l.marks = append(l.marks, l.cur)
	}
}

// scan returns parseJSONLegacy(s). s must start with the output of the last
// call, apart from the bytes a rewind gave up.
func (l *legacyJSONScan) scan(s string) ([]map[string]any, error) {
	offset := l.cur.offset
	extent := l.cur.extent
	stable := true
	var tail []map[string]any
	for offset < len(s) {
		var (
			next, end int
			obj       map[string]any
			found     bool
		)
		if n := skipNonJSON(s[offset:]); n > 0 {
			next, end = offset+n, offset+n
		} else {
			decoder := json.NewDecoder(strings.NewReader(s[offset:]))
			err := decoder.Decode(&obj)
			var syntaxErr *json.SyntaxError
			var typeErr *json.UnmarshalTypeError
			switch {
			case errors.Is(err, io.EOF):
				return l.result(tail), nil
			case err == nil:
				next = offset + int(decoder.InputOffset())
				end = next + 1
				found = true
			case errors.As(err, &syntaxErr):
				next = offset + int(syntaxErr.Offset)
				end = next + 1
			case errors.As(err, &typeErr):
				// The decoder read the whole value before it found the type
				// wrong, and the offset can point inside it.
				next = offset + int(typeErr.Offset)
				end = offset + int(decoder.InputOffset()) + 1
			default:
				return l.result(tail), err
			}
		}
		extent = max(extent, end)
		if stable && extent <= len(s) {
			l.advance(next, extent, obj, found)
		} else {
			stable = false
			if found {
				tail = append(tail, obj)
			}
		}
		offset = next
	}
	return l.result(tail), nil
}

// result returns the objects as parseJSONLegacy does, nil when there are
// none. The scan only appends to l.objs, beyond the capacity of the slice it
// returns, and a rewind starts a new array.
func (l *legacyJSONScan) result(tail []map[string]any) []map[string]any {
	if len(l.objs)+len(tail) == 0 {
		return nil
	}
	return append(l.objs[:len(l.objs):len(l.objs)], tail...)
}

// opensWithJSON reports whether the first byte of s that ConsumeSpaces does
// not skip opens a JSON object or array, and whether s has such a byte.
// ParseJSONIterative returns parseJSONLegacy(s) when it has one that does not.
func opensWithJSON(s string) (opens, known bool) {
	for i := 0; i < len(s); i++ {
		if c := s[i]; !unicode.IsSpace(rune(c)) {
			return c == '{' || c == '[', true
		}
	}
	return false, false
}
