package workspace

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const maxNameBytes = 255

// StudioAdmin is the principal of the workspace administrator, which creates
// homes and grants access. It is not a user: no service takes it as the
// principal of a chat or a home.
const StudioAdmin = "@studio-admin"

// cleanName normalizes one path segment to NFC and checks the name rules.
func cleanName(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrBadName
	}
	n := norm.NFC.String(s)
	if n == "" || n == "." || n == ".." || len(n) > maxNameBytes {
		return "", ErrBadName
	}
	for _, r := range n {
		if r == '/' || r == 0 || unicode.IsControl(r) {
			return "", ErrBadName
		}
	}
	return n, nil
}

// foldName returns the case-folded form that the sibling rule compares.
// A Caser is not safe for concurrent use, so each call makes its own.
func foldName(n string) string {
	return cases.Fold().String(n)
}

// FoldName returns the store's comparison form of a name: NFC, then case
// folded. Two sibling names are the same when their FoldName values are
// equal. Use it wherever a name must match the store's own rule.
func FoldName(name string) string {
	return foldName(norm.NFC.String(name))
}

// splitPath turns "/a/b", "a/b" or "/a/b/" into clean segments. The root
// ("" or "/") has no segments.
func splitPath(p string) ([]string, error) {
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return nil, nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, len(parts))
	for i, s := range parts {
		n, err := cleanName(s)
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

// joinPath renders segments as an API path.
func joinPath(segs []string) string {
	return "/" + strings.Join(segs, "/")
}

// joinChild returns the path of name inside the folder dir.
func joinChild(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}

// conflictName returns the i-th candidate name for a conflict copy of name,
// for example "report (from chat 12).xlsx" and "report (from chat 12) 2.xlsx".
// It shortens the stem so the result fits the name length limit.
func conflictName(name, label string, i int) string {
	ext := ""
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		ext = name[dot:]
	}
	stem := strings.TrimSuffix(name, ext)
	label = strings.Map(func(r rune) rune {
		if r == '/' || r == 0 || unicode.IsControl(r) {
			return '-'
		}
		return r
	}, label)
	suffix := fmt.Sprintf(" (from %s)", label)
	if i > 1 {
		suffix += fmt.Sprintf(" %d", i)
	}
	for len(stem)+len(suffix)+len(ext) > maxNameBytes && stem != "" {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	return stem + suffix + ext
}
