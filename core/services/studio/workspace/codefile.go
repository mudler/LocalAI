package workspace

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// codeExt are the extensions of files that a browser runs or renders.
var codeExt = map[string]bool{
	"js": true, "mjs": true, "cjs": true, "css": true, "html": true, "htm": true,
	"svg": true, "xml": true, "xhtml": true, "wasm": true,
}

// pageMetaFile is the name of the file that holds the title and icon of a page.
const pageMetaFile = "page.json"

// IsCodeFile says whether a path names code or a document instead of data: a
// file with a code extension (.js .mjs .cjs .css .html .htm .svg .xml .xhtml
// .wasm), a file named index or page.json. A grant with ModeData refuses to
// change such a file, and the page bridge refuses to write one.
//
// It judges the final segment only, and judges what the store and the page
// loader would see: the path is percent-decoded (up to three layers), taken in
// NFKC and case folded, backslashes count as separators, lookalikes of the dot
// count as dots, and trailing dots, spaces and a stream suffix after ':' are
// dropped.
func IsCodeFile(rel string) bool {
	s := rel
	for range 3 {
		d, err := url.PathUnescape(s)
		if err != nil || d == s || !utf8.ValidString(d) {
			break
		}
		s = d
	}
	s = cases.Fold().String(norm.NFKC.String(s))
	s = strings.NewReplacer("\\", "/", "。", ".", "｡", ".").Replace(s)
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, " .\t\r\n\x00")
	if s == "index" || s == pageMetaFile {
		return true
	}
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		return codeExt[s[i+1:]]
	}
	return false
}
