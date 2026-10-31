package pages

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// codeExt are the extensions of files that a browser runs or renders. A page
// loads its own scripts, modules and styles, so what it could write with these
// names would run on the next load.
var codeExt = map[string]bool{
	"js": true, "mjs": true, "cjs": true, "css": true, "html": true, "htm": true,
	"svg": true, "xml": true, "xhtml": true, "wasm": true,
}

// IsCodeFile says whether a path written by a page names code or a document
// instead of data: a file with a code extension, a file named index or
// page.json. The page bridge refuses these writes; the person and the
// assistant write the same files through the Files page and the file tools.
//
// It judges the final segment only, and judges what the store and the page
// loader would see: the path is percent-decoded (up to three layers), taken in
// NFKC and case folded, backslashes count as separators, lookalikes of the dot
// count as dots, and trailing dots, spaces and a stream suffix after ':' are
// dropped. The web host has the same function (isCodeFile in pagesBridge.js);
// both run testdata/code-files.json so they cannot drift.
func IsCodeFile(rel string) bool {
	s := rel
	for i := 0; i < 3; i++ {
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
	if s == "index" || s == MetaFile {
		return true
	}
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		return codeExt[s[i+1:]]
	}
	return false
}
