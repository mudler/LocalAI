package files

import (
	"bufio"
	"context"
	"io"
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
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, workspace.ErrInvalid
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
	a := s.Actor(principal)
	needle := strings.ToLower(query)
	var names, texts []Hit
	visited, scanned := 0, int64(0)

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
			rel := s.Rel(principal, k.Path)
			if strings.Contains(strings.ToLower(k.Name), needle) {
				names = append(names, Hit{Path: rel, Kind: k.Kind})
			}
			if k.Kind == workspace.KindDir {
				if err := walk(k.Path); err != nil {
					return err
				}
				continue
			}
			if len(names)+len(texts) >= limit*2 || k.Size > searchMaxFileBytes || scanned+k.Size > searchMaxTotal {
				continue
			}
			scanned += k.Size
			texts = append(texts, s.scanFile(ctx, a, k, rel, needle)...)
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	out := append(names, texts...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// scanFile returns the lines of a text file that contain needle.
func (s *Service) scanFile(ctx context.Context, a workspace.Actor, n workspace.Node, rel, needle string) []Hit {
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
	for line := 1; sc.Scan() && len(hits) < searchPerFile; line++ {
		text := sc.Text()
		if strings.Contains(strings.ToLower(text), needle) {
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
