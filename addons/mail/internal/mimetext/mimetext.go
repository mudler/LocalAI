// SPDX-License-Identifier: MIT

// Package mimetext turns the parts of a mail message into text a model can
// read without being steered by it: it undoes the transfer encoding and the
// character set, converts HTML to plain text with no images, links or styles,
// and removes characters that hide or reorder text (control characters, zero
// width and bidirectional marks, and the tag characters that some attacks use
// to smuggle invisible instructions).
package mimetext

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/web/extract"
	"golang.org/x/net/html/charset"
)

// DecodeTransfer undoes a Content-Transfer-Encoding. An unknown or plain
// encoding returns the bytes as they are. The decoders are lenient: mail in
// the wild has broken line ends and padding.
func DecodeTransfer(encoding string, raw []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		clean := make([]byte, 0, len(raw))
		for _, c := range raw {
			if c == '=' || c == '+' || c == '/' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
				clean = append(clean, c)
			}
		}
		// A cut part ends inside a group of four: drop the partial group.
		if i := bytes.IndexByte(clean, '='); i >= 0 {
			clean = clean[:min(len(clean), (i+3)/4*4)]
		} else {
			clean = clean[:len(clean)/4*4]
		}
		out, _ := base64.StdEncoding.DecodeString(string(clean))
		if out == nil {
			out, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(string(clean), "="))
		}
		return out
	case "quoted-printable":
		out, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil && len(out) == 0 {
			return raw
		}
		return out
	}
	return raw
}

// ToUTF8 converts text of the named character set to UTF-8. An unknown name is
// read as UTF-8, and bytes that are not valid become U+FFFD.
func ToUTF8(label string, b []byte) string {
	if label = strings.TrimSpace(label); label != "" && !strings.EqualFold(label, "utf-8") && !strings.EqualFold(label, "us-ascii") {
		if r, err := charset.NewReaderLabel(label, bytes.NewReader(b)); err == nil {
			if out, err := io.ReadAll(r); err == nil {
				return strings.ToValidUTF8(string(out), "�")
			}
		}
	}
	return strings.ToValidUTF8(string(b), "�")
}

// DecodeWords decodes RFC 2047 encoded words of a header value.
func DecodeWords(s string) string {
	dec := &mime.WordDecoder{CharsetReader: func(label string, r io.Reader) (io.Reader, error) {
		return charset.NewReaderLabel(label, r)
	}}
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// HTMLText converts HTML to plain text. Scripts, styles and hidden elements
// are dropped, and neither image addresses nor link targets are kept.
func HTMLText(src string) string {
	_, body, err := extract.HTML(strings.NewReader(src), nil, extract.FormatText)
	if err != nil {
		return ""
	}
	return body
}

// Clean makes text safe to show: CRLF becomes LF, control characters other
// than tab and line feed are dropped, so are format characters (zero width
// joiners, bidirectional overrides, tag characters), runs of blank lines
// shrink to one blank line and trailing spaces go.
func Clean(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == utf8.RuneError, unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == ' ', r == ' ':
			if r == utf8.RuneError {
				b.WriteRune(r)
			}
		default:
			b.WriteRune(r)
		}
	}
	lines := strings.Split(b.String(), "\n")
	out := lines[:0]
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Line cleans a value that must stay on one line, such as a subject or a file
// name, and cuts it to max runes.
func Line(s string, max int) string {
	s = Clean(s)
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		return string(r[:max])
	}
	return s
}

// Cut shortens s to at most max bytes at a character boundary and returns how
// many bytes were left out.
func Cut(s string, max int) (string, int) {
	if len(s) <= max {
		return s, 0
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}
