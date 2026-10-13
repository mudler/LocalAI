// SPDX-License-Identifier: MIT
package artifacts

import (
	"mime"
	"strings"
	"unicode"
)

// maxNameRunes bounds the attachment name.
const maxNameRunes = 100

// fallbackName is used when nothing of the declared name is usable.
const fallbackName = "download"

// SafeName turns an addon-declared file name into a name that is safe to put in
// a Content-Disposition header and to save on a user's disk. It drops path
// separators and everything before them, control characters, bidirectional
// overrides and isolates, zero-width and invisible format characters, and
// characters with a meaning in common shells and file systems. It removes
// leading dots and spaces so the name is never hidden or relative, removes
// trailing dots and spaces, prefixes a reserved device name, and bounds the
// length while keeping a short extension.
func SafeName(name string) string {
	// Both separators end a path: only the last element is a file name.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r), r == 0xFFFD:
			// Cf covers the bidirectional overrides and isolates, zero-width
			// characters and the byte order mark.
			continue
		case strings.ContainsRune(`<>:"|?*;&$`+"`", r), r == '%':
			b.WriteByte('_')
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ". ")
	if out == "" {
		return fallbackName
	}
	runes := []rune(out)
	if len(runes) > maxNameRunes {
		ext := ""
		if i := strings.LastIndexByte(out, '.'); i > 0 {
			if e := []rune(out[i:]); len(e) <= 16 {
				ext = string(e)
			}
		}
		keep := maxNameRunes - len([]rune(ext))
		out = strings.TrimRight(string(runes[:keep]), ". ") + ext
	}
	if reserved(out) {
		out = "_" + out
	}
	return out
}

// reserved reports a Windows device name, with or without an extension.
func reserved(name string) bool {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch strings.ToUpper(strings.TrimRight(base, " ")) {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// safeMediaTypes is the whole set of types a download can carry. None of them
// is rendered inline as active content, and the response is an attachment with
// nosniff on top. Everything else is application/octet-stream.
var safeMediaTypes = map[string]bool{
	"text/csv":                 true,
	"text/plain":               true,
	"application/json":         true,
	"application/x-ndjson":     true,
	"application/zip":          true,
	"application/pdf":          true,
	"application/octet-stream": true,
	"image/png":                true,
	"image/jpeg":               true,
}

// SafeMediaType maps a declared media type to the fixed allowlist. Parameters
// are dropped: a declared charset is a claim about bytes the host did not check.
func SafeMediaType(declared string) string {
	t, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return "application/octet-stream"
	}
	t = strings.ToLower(t)
	if safeMediaTypes[t] {
		return t
	}
	return "application/octet-stream"
}
