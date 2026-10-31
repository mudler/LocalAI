package files

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/extract"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
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
	// Document is set for a line of the text read from a PDF or an Office
	// file; Line is then a line of that text, as read_file numbers it.
	Document bool
	// Section is the page, sheet or slide of a Document hit, when the
	// document has them.
	Section extract.Section
}

// SearchResult is what a search found.
type SearchResult struct {
	Hits []Hit
	// Indexing counts the documents below the path, readable by the user,
	// whose text is not read yet: a search a moment later may find more.
	Indexing int
}

// Search finds files by name and by the text inside them, among what the user
// may read below dir (default "/"). Name matches come first, then lines of
// text files, then lines of documents. Text files are read on the spot when
// they are valid UTF-8 and small enough, up to the limits above; PDF and
// Office files are searched in the text index. The match ignores case.
func (s *Service) Search(ctx context.Context, principal, query, dir string, limit int) (SearchResult, error) {
	if !s.Available() {
		return SearchResult{}, ErrUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if dir == "" {
		dir = "/"
	}
	root, err := s.Abs(principal, dir)
	if err != nil {
		return SearchResult{}, err
	}
	q, err := newQuery(query, false, true)
	if err != nil {
		return SearchResult{}, err
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
	// contains is the plain text in lower case, which a text that matches
	// must hold; empty for a regular expression.
	contains string
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
	return query{match: match, names: true, limit: searchPlainMax, perFile: searchPerFile, contains: strings.ToLower(needle)}, nil
}

// search runs q below root, a folder or one file, as a. show turns a
// workspace path into the path a hit names.
func (s *Service) search(ctx context.Context, a workspace.Actor, root string, show func(string) string, q query) (SearchResult, error) {
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
		return SearchResult{}, err
	}
	if n.Kind == workspace.KindDir {
		if err := walk(root); err != nil {
			return SearchResult{}, err
		}
	} else {
		look(n)
	}
	out := append(names, texts...)
	// Documents come last but keep up to a third of the hits, so many
	// matching text files do not hide every document.
	docs, indexing := s.searchIndex(ctx, a, root, show, q, q.limit)
	nd := min(len(docs), max(q.limit-len(out), q.limit/3))
	if len(out) > q.limit-nd {
		out = out[:q.limit-nd]
	}
	out = append(out, docs[:nd]...)
	return SearchResult{Hits: out, Indexing: indexing}, nil
}

// indexPage is how many index entries one read of the store returns.
const indexPage = 20

// searchIndex returns up to room lines of the documents below root that
// match q, from the text index, and how many documents there are still
// being read. Each entry is shown only when a may read its file now and the
// file still has the content the text was read from.
func (s *Service) searchIndex(ctx context.Context, a workspace.Actor, root string, show func(string) string, q query, room int) ([]Hit, int) {
	ix := s.textIndex()
	if ix == nil {
		return nil, 0
	}
	st := ix.Store()
	var hits []Hit
	var after int64
	visited, scanned := 0, 0
	for room > 0 && visited < searchMaxNodes && scanned < searchMaxTotal && ctx.Err() == nil {
		page, err := st.Search(ctx, textindex.Query{Prefix: root, Contains: q.contains, AfterID: after, Limit: indexPage})
		if err != nil || len(page) == 0 {
			break
		}
		for _, e := range page {
			after = e.NodeID
			visited++
			scanned += len(e.Text)
			if !s.mayReadEntry(ctx, a, e, true) {
				continue
			}
			found := matchText(e, show(e.Path), q, min(q.perFile, room))
			hits = append(hits, found...)
			if room -= len(found); room <= 0 {
				break
			}
		}
	}
	indexing := 0
	if ix.On() {
		pending, err := st.Pending(ctx, root, 50)
		if err == nil {
			for _, e := range pending {
				if s.mayReadEntry(ctx, a, e, false) {
					indexing++
				}
			}
		}
	}
	return hits, indexing
}

// mayReadEntry reports whether a may read the file of an index entry: the
// path names the same node, and with sameContent, the node still holds the
// content the entry was made from.
func (s *Service) mayReadEntry(ctx context.Context, a workspace.Actor, e textindex.Entry, sameContent bool) bool {
	n, err := s.ws.Stat(ctx, a, e.Path)
	return err == nil && n.Kind == workspace.KindFile && n.ID == e.NodeID && (!sameContent || n.Blob == e.Hash)
}

// matchText returns the lines of an entry's text that match q, at most max.
func matchText(e textindex.Entry, rel string, q query, max int) []Hit {
	var hits []Hit
	for i, line := range strings.Split(e.Text, "\n") {
		if len(hits) >= max {
			break
		}
		if !q.match(line) {
			continue
		}
		h := Hit{Path: rel, Kind: workspace.KindFile, Line: i + 1, Snippet: clip(line), Document: true}
		if sec, ok := extract.SectionAt(e.Sections, i+1); ok {
			h.Section = sec
		}
		hits = append(hits, h)
	}
	return hits
}

// scanFile returns the lines of a text file that match q.
func (s *Service) scanFile(ctx context.Context, a workspace.Actor, n workspace.Node, rel string, q query) []Hit {
	r, _, err := s.ws.Open(ctx, a, n.Path)
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
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
