package pii

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/utils"
)

// protectedPlaceholder stands in for a protected term while the detector
// runs. It carries no letters a name/address model could read as PII and
// is restored verbatim afterwards, so its exact shape only matters as
// neutral context for the encoder.
const protectedPlaceholder = "[PROTECTED]"

// minProtectedTermRunes is the shortest term that is honoured. Shorter
// terms ("AG", "DB") would shield every matching token in a document,
// including ones that are part of real PII.
const minProtectedTermRunes = 3

// shield maps one protected occurrence between the original text
// [origStart, origEnd) and the scanned text [scanStart, scanEnd).
type shield struct {
	origStart, origEnd int
	scanStart, scanEnd int
}

// File reloads can produce indefinitely many distinct lists. Retain only a
// bounded set of compiled matchers; eviction changes no detection policy.
var protectedMatchers = struct {
	sync.Mutex
	entries map[string]*regexp.Regexp
	order   []string
}{entries: make(map[string]*regexp.Regexp)}

const maxProtectedMatchers = 32

// protectedMatcher caches a case-insensitive matcher for the terms,
// longest first so that "Hotel
// Seeblick Spa" wins over "Hotel Seeblick". Whitespace inside a term
// matches any whitespace run, so a term still matches across a line wrap.
// Returns nil when no usable term remains.
func protectedMatcher(terms []string) *regexp.Regexp {
	var norm []string
	for _, t := range terms {
		t = strings.Join(strings.Fields(t), " ")
		if utf8.RuneCountInString(t) < minProtectedTermRunes {
			continue
		}
		norm = append(norm, t)
	}
	if len(norm) == 0 {
		return nil
	}
	slices.SortFunc(norm, func(a, b string) int {
		if d := len(b) - len(a); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	norm = slices.Compact(norm)
	key := strings.Join(norm, "\x00")
	protectedMatchers.Lock()
	defer protectedMatchers.Unlock()
	if re, ok := protectedMatchers.entries[key]; ok {
		return re
	}
	alts := make([]string, len(norm))
	for i, t := range norm {
		words := strings.Fields(t)
		for j, w := range words {
			words[j] = regexp.QuoteMeta(w)
		}
		alts[i] = strings.Join(words, `\s+`)
	}
	re := regexp.MustCompile(`(?i)(?:` + strings.Join(alts, "|") + `)`)
	if len(protectedMatchers.order) == maxProtectedMatchers {
		delete(protectedMatchers.entries, protectedMatchers.order[0])
		protectedMatchers.order = protectedMatchers.order[1:]
	}
	protectedMatchers.entries[key] = re
	protectedMatchers.order = append(protectedMatchers.order, key)
	return re
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// shieldProtected replaces every whole-word occurrence of a protected term
// with protectedPlaceholder and returns the text the detector should scan
// plus the mapping back to the original. With no matcher or no match the
// text is returned unchanged and regions is nil.
func shieldProtected(text string, re *regexp.Regexp) (string, []shield) {
	if re == nil || text == "" {
		return text, nil
	}
	var regions []shield
	var b strings.Builder
	cursor := 0
	for _, m := range re.FindAllStringIndex(text, -1) {
		s, e := m[0], m[1]
		if s > 0 {
			if r, _ := utf8.DecodeLastRuneInString(text[:s]); isWordRune(r) {
				continue
			}
		}
		if e < len(text) {
			if r, _ := utf8.DecodeRuneInString(text[e:]); isWordRune(r) {
				continue
			}
		}
		b.WriteString(text[cursor:s])
		scanStart := b.Len()
		b.WriteString(protectedPlaceholder)
		regions = append(regions, shield{origStart: s, origEnd: e, scanStart: scanStart, scanEnd: b.Len()})
		cursor = e
	}
	if regions == nil {
		return text, nil
	}
	b.WriteString(text[cursor:])
	return b.String(), regions
}

// unshieldSpan maps a detection [start, end) in the scanned text back to
// the original text. The parts that fall on a placeholder are cut out —
// a protected term is never masked or blocked — so one detection can come
// back as zero, one or two pieces.
func unshieldSpan(start, end int, regions []shield) [][2]int {
	toOrig := func(p int) int {
		delta := 0
		for _, r := range regions {
			if r.scanEnd > p {
				break
			}
			delta += (r.origEnd - r.origStart) - (r.scanEnd - r.scanStart)
		}
		return p + delta
	}
	var out [][2]int
	cur := start
	for _, r := range regions {
		if r.scanEnd <= cur {
			continue
		}
		if r.scanStart >= end {
			break
		}
		if r.scanStart > cur {
			out = append(out, [2]int{toOrig(cur), toOrig(r.scanStart)})
		}
		cur = r.scanEnd
	}
	if cur < end {
		out = append(out, [2]int{toOrig(cur), toOrig(end)})
	}
	return out
}

// keepExtension returns extended when the word extension [end, extended)
// does not run into a protected occurrence, and end otherwise: a protected
// term is never masked, and the extension is dropped rather than cut so
// no dangling whitespace is swallowed.
func keepExtension(end, extended int, regions []shield) int {
	for _, r := range regions {
		if r.origStart >= end && r.origStart < extended {
			return end
		}
	}
	return extended
}

type protectedFileEntry struct {
	modTime time.Time
	size    int64
	terms   []string
}

var (
	protectedFilesMu sync.Mutex
	protectedFiles   = map[string]protectedFileEntry{}
)

// LoadProtectedTerms returns the inline terms plus the terms read from
// each file (one term per line; blank lines and lines starting with '#'
// are skipped). File paths resolve against baseDir (the models path) and
// must stay inside it. A file is re-read only when its size or modification time
// changes, so an external process can keep it current without a restart.
//
// A missing or unreadable file is logged and skipped. That is the safe
// direction for a PII filter: fewer protected terms means more masking,
// never less.
func LoadProtectedTerms(inline, files []string, baseDir string) []string {
	out := append([]string(nil), inline...)
	for _, f := range files {
		if err := utils.VerifyPath(f, baseDir); err != nil {
			xlog.Warn("pii: protected terms file outside the models path; skipping", "file", f, "error", err)
			continue
		}
		path := filepath.Join(baseDir, f)
		terms, err := readProtectedFile(path)
		if err != nil {
			xlog.Warn("pii: protected terms file not readable; skipping", "file", path, "error", err)
			continue
		}
		out = append(out, terms...)
	}
	return out
}

func readProtectedFile(path string) ([]string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	protectedFilesMu.Lock()
	defer protectedFilesMu.Unlock()
	if c, ok := protectedFiles[path]; ok && c.modTime.Equal(st.ModTime()) && c.size == st.Size() {
		return c.terms, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var terms []string
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		terms = append(terms, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	protectedFiles[path] = protectedFileEntry{modTime: st.ModTime(), size: st.Size(), terms: terms}
	return terms, nil
}
