package pii

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

type responsePIIConfig interface {
	PIIReversibleRedactions() bool
	PIIReversibleTokenPrefix() string
	PIIReversibleTokenSuffix() string
}

const (
	defaultReversibleTokenPrefix = "[REDACTED:"
	defaultReversibleTokenSuffix = "]"
)

type pseudonymizer struct {
	byValue  map[string]string
	original map[string]string
	counts   map[string]int
	prefix   string
	suffix   string
}

func newPseudonymizer(prefix, suffix string) *pseudonymizer {
	return &pseudonymizer{
		byValue:  map[string]string{},
		original: map[string]string{},
		counts:   map[string]int{},
		prefix:   prefix,
		suffix:   suffix,
	}
}

func (p *pseudonymizer) replace(text string, spans []Span) string {
	var b strings.Builder
	last := 0
	for _, span := range spans {
		if span.Action != ActionMask || span.Start < last || span.End > len(text) {
			continue
		}
		b.WriteString(text[last:span.Start])
		value := text[span.Start:span.End]
		token, ok := p.byValue[value]
		if !ok {
			group := pseudonymGroup(span.Pattern)
			p.counts[group]++
			token = fmt.Sprintf("%s%s_%03d%s", p.prefix, group, p.counts[group], p.suffix)
			p.byValue[value] = token
			p.original[token] = value
		}
		b.WriteString(token)
		last = span.End
	}
	b.WriteString(text[last:])
	return b.String()
}

func pseudonymGroup(pattern string) string {
	if i := strings.LastIndexByte(pattern, ':'); i >= 0 {
		pattern = pattern[i+1:]
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(pattern) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "PII"
	}
	return b.String()
}

type restoringWriter struct {
	http.ResponseWriter
	pending      string
	replacements map[string]string
}

func newRestoringWriter(w http.ResponseWriter, originals map[string]string) *restoringWriter {
	replacements := make(map[string]string, len(originals))
	for token, original := range originals {
		encoded, _ := json.Marshal(original)
		replacements[token] = string(encoded[1 : len(encoded)-1])
	}
	return &restoringWriter{ResponseWriter: w, replacements: replacements}
}

func (w *restoringWriter) Write(data []byte) (int, error) {
	w.pending += string(data)
	ready, pending := w.splitReady(w.replace(w.pending))
	w.pending = pending
	if ready != "" {
		if _, err := w.ResponseWriter.Write([]byte(ready)); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (w *restoringWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *restoringWriter) Finish() error {
	if w.pending == "" {
		return nil
	}
	_, err := w.ResponseWriter.Write([]byte(w.replace(w.pending)))
	w.pending = ""
	return err
}

func (w *restoringWriter) replace(s string) string {
	for token, original := range w.replacements {
		s = strings.ReplaceAll(s, token, original)
	}
	return s
}

func (w *restoringWriter) splitReady(s string) (string, string) {
	keep := 0
	for token := range w.replacements {
		limit := min(len(token)-1, len(s))
		for n := 1; n <= limit; n++ {
			if strings.HasSuffix(s, token[:n]) && n > keep {
				keep = n
			}
		}
	}
	return s[:len(s)-keep], s[len(s)-keep:]
}
