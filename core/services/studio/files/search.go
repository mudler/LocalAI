package files

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Limits that keep one search from reading a whole workspace.
const (
	searchMaxNodes     = 20000   // folders and files looked at
	searchMaxFileBytes = 1 << 20 // a larger file is found by name only
	searchMaxTotal     = 64 << 20
	searchPerFile      = 3 // content hits shown for one file
	snippetRunes       = 200
)

// Hit is one search result. A name match has Line 0.
type Hit struct {
	Path    string // inside the user's home
	Kind    workspace.Kind
	Line    int
	Snippet string
}

// Search finds files by name and by the text inside them, among what the user
// may read below dir (default "/"). Name matches come first. Text is searched
// only in files that are valid UTF-8 text and small enough; there is no index,
// so a big workspace is scanned up to the limits above. The match ignores case.
func (s *Service) Search(ctx context.Context, principal, query, dir string, limit int) ([]Hit, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if dir == "" {
		dir = "/"
	}
	root, err := s.Abs(principal, dir)
	if err != nil {
		return nil, err
	}
	q, err := newQuery(query, false, true)
	if err != nil {
		return nil, err
	}
	q.limit = limit
	return s.search(ctx, s.Actor(principal), root, func(abs string) string { return s.Rel(principal, abs) }, q)
}

// query is what one search looks for.
type query struct {
	match   func(string) bool
	names   bool // match file names too (plain text only)
	limit   int
	perFile int // content hits shown for one file
}

// newQuery builds a search: plain text, or a regular expression matched
// against each line.
func newQuery(text string, regex, ignoreCase bool) (query, error) {
	if strings.TrimSpace(text) == "" {
		return query{}, workspace.ErrInvalid
	}
	if len(text) > maxPatternLen {
		return query{}, fmt.Errorf("the query must be at most %d characters", maxPatternLen)
	}
	if regex {
		expr := text
		if ignoreCase {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return query{}, fmt.Errorf("the query is not a valid regular expression: %w", err)
		}
		return query{match: re.MatchString, limit: grepMax, perFile: grepMax}, nil
	}
	needle := strings.TrimSpace(text)
	match := func(s string) bool { return strings.Contains(s, needle) }
	if ignoreCase {
		needle = strings.ToLower(needle)
		match = func(s string) bool { return strings.Contains(strings.ToLower(s), needle) }
	}
	return query{match: match, names: true, limit: searchPlainMax, perFile: searchPerFile}, nil
}

// search runs q below root, a folder or one file, as a. show turns a
// workspace path into the path a hit names.
func (s *Service) search(ctx context.Context, a workspace.Actor, root string, show func(string) string, q query) ([]Hit, error) {
	var names, texts []Hit
	visited, scanned := 0, int64(0)
	full := func() bool { return len(names)+len(texts) >= q.limit*2 }

	look := func(k workspace.Node) {
		if full() || k.Size > searchMaxFileBytes || scanned+k.Size > searchMaxTotal {
			return
		}
		scanned += k.Size
		texts = append(texts, s.scanFile(ctx, a, k, show(k.Path), q)...)
	}
	var walk func(p string) error
	walk = func(p string) error {
		kids, err := s.ws.List(ctx, a, p)
		if err != nil {
			return err
		}
		for _, k := range kids {
			if visited++; visited > searchMaxNodes || ctx.Err() != nil {
				return ctx.Err()
			}
			if q.names && q.match(k.Name) {
				names = append(names, Hit{Path: show(k.Path), Kind: k.Kind})
			}
			if k.Kind == workspace.KindDir {
				if k.Name == ".git" {
					continue
				}
				if err := walk(k.Path); err != nil {
					return err
				}
				continue
			}
			look(k)
		}
		return nil
	}
	n, err := s.ws.Stat(ctx, a, root)
	if err != nil {
		return nil, err
	}
	if n.Kind == workspace.KindDir {
		if err := walk(root); err != nil {
			return nil, err
		}
	} else {
		look(n)
	}
	out := append(names, texts...)
	if len(out) > q.limit {
		out = out[:q.limit]
	}
	return out, nil
}

// scanFile returns the lines of a text file that match q.
func (s *Service) scanFile(ctx context.Context, a workspace.Actor, n workspace.Node, rel string, q query) []Hit {
	r, _, err := s.ws.Open(ctx, a, n.Path)
	if err != nil {
		return nil
	}
	defer r.Close()
	head := make([]byte, 8192)
	k, _ := io.ReadFull(r, head)
	head = head[:k]
	if !isText(head) {
		return nil
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	var hits []Hit
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), searchMaxFileBytes)
	for line := 1; sc.Scan() && len(hits) < q.perFile; line++ {
		text := sc.Text()
		if q.match(text) {
			hits = append(hits, Hit{Path: rel, Kind: workspace.KindFile, Line: line, Snippet: clip(text)})
		}
	}
	return hits
}

// isText reports whether the start of a file looks like UTF-8 text: no NUL
// byte, and no invalid sequence apart from one cut at the end of the sample.
func isText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	if strings.IndexByte(string(b), 0) >= 0 {
		return false
	}
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size <= 1 {
			return len(b) < utf8.UTFMax // a rune cut by the sample's end is fine
		}
		b = b[size:]
	}
	return true
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > snippetRunes {
		return string(r[:snippetRunes]) + "…"
	}
	return s
}
