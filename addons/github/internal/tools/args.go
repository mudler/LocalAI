// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/protocol"
)

// bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds text from GitHub or from the token.
func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrBadRequest, fmt.Sprintf(format, a...))
}

var (
	repoShape = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
	refShape  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
)

// checkRepo accepts the form owner/name and nothing else.
func checkRepo(repo string) error {
	if !repoShape.MatchString(repo) || strings.Contains(repo, "..") {
		return bad("repo must be owner/name")
	}
	return nil
}

// checkRef accepts a branch, tag or commit name.
func checkRef(ref string) error {
	if ref != "" && (!refShape.MatchString(ref) || strings.Contains(ref, "..")) {
		return bad("ref is not a branch tag or commit")
	}
	return nil
}

// checkPath accepts a path inside the repository: no empty, dot or dot-dot
// segment, no .git segment, no backslash, no control character. An empty path
// is the root when allowRoot.
func checkPath(p string, allowRoot bool) error {
	if p == "" {
		if allowRoot {
			return nil
		}
		return bad("path is required")
	}
	if len(p) > 400 || !utf8.ValidString(p) || strings.Contains(p, `\`) || strings.HasPrefix(p, "/") {
		return bad("path is not valid")
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return bad("path is not valid")
		}
	}
	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." || strings.EqualFold(seg, ".git") {
			return bad("path is not valid")
		}
	}
	return nil
}

// forbiddenQualifiers widen a code search beyond the one repository. They are
// refused wherever they appear, quoted or not.
var forbiddenQualifiers = []string{"repo:", "org:", "user:", "owner:", "enterprise:"}

func checkQuery(q string) error {
	q = strings.TrimSpace(q)
	if q == "" || utf8.RuneCountInString(q) > 256 || !utf8.ValidString(q) {
		return bad("query has 1 to 256 characters")
	}
	for _, r := range q {
		if unicode.IsControl(r) {
			return bad("query has a control character")
		}
	}
	l := strings.ToLower(q)
	for _, f := range forbiddenQualifiers {
		if strings.Contains(l, f) {
			return bad("query may not name a repo or an owner")
		}
	}
	return nil
}

// decodeArgs reads the arguments of a tool, less the tool name, into v. An
// unknown field is a refusal: the host checked the schema, so one here is a
// bug to surface and not input to guess at.
func decodeArgs(raw map[string]json.RawMessage, v any) error {
	b, _ := json.Marshal(raw)
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bad("the arguments do not match the tool")
	}
	return nil
}

// cut shortens s to at most max bytes at a character boundary and says how
// much was left out.
func cut(s string, max int) (out string, omitted int) {
	if len(s) <= max {
		return s, 0
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}
