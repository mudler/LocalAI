package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/extract"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

// Limits of what one file tool call reads, changes and returns to the model.
const (
	readMaxBytes = 4 << 20
	readMaxLines = 2000
	// editMaxBytes is the largest file edit_file changes.
	editMaxBytes = 8 << 20
	// globMax is the most paths list_files returns for a pattern.
	globMax = 500
	// grepMax is the most lines search_files returns for a regular expression.
	grepMax = 200
	// searchPlainMax is the most hits search_files returns for plain text.
	searchPlainMax = 30
	// maxPatternLen bounds a pattern or a regular expression from the model.
	maxPatternLen = 1000
)

// Errors of a write to a place the model may only read.
var (
	errSharedReadOnly = errors.New("the shared and built-in skills are read-only")
	errFolderReadOnly = errors.New("this shared folder is read-only for you")
)

// writable resolves a path for a tool that changes it. Every tool resolves
// its paths through the chat's place: in the primary folder (the home, or
// the agent's folder), the chat folder, a shared folder or a skill layer. A
// path with .. is refused wherever it points.
func writable(place session.Place, p string) (session.Target, error) {
	t, err := place.Resolve(p)
	if err != nil {
		return session.Target{}, err
	}
	if !t.Write {
		switch {
		case t.Layer:
			return session.Target{}, errSharedReadOnly
		case t.At == session.GuestHome || t.At == session.GuestChat:
			return session.Target{}, session.ErrReadOnlyPlace
		}
		return session.Target{}, errFolderReadOnly
	}
	return t, nil
}

// Tools returns the file tools of one chat: list, read, write, edit, move and
// delete files, and search them, in the user's home and shared folders. They
// act as the user in that chat, so what they change is an agent change in the
// history, which the user can undo, and a delete goes to the Trash. A
// sub-agent gets the tools of its parent chat. Every path goes through the
// access's place, and every call acts as the access's actor, so a chat whose
// agent has a folder reaches only its folders.
func (s *Service) Tools(x session.Access) []session.Tool {
	if !s.Available() {
		return nil
	}
	a, place := x.Actor(), x.Place()
	call := func(fn func(ctx context.Context, args json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
		return func(ctx context.Context, args json.RawMessage) (string, error) {
			out, err := fn(ctx, args)
			return out, userError(err)
		}
	}
	obj := func(props string, required ...string) json.RawMessage {
		req, _ := json.Marshal(required)
		return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(req) + `}`)
	}
	pathProp := `"path":{"type":"string","description":"A path in the user's files: /workspace/home/... is their home folder (/ means the same), /workspace/shared-<name>/... a shared folder."}`
	return []session.Tool{
		{Name: "list_files", Description: "List a folder of the user's files. With pattern, list every file and folder below path whose path matches it: * matches any characters including /, ? one character. At most 500 results.",
			Schema: obj(pathProp + `,"pattern":{"type":"string","description":"For example *.md or reports/*.xlsx"}`),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path, Pattern string }](raw)
				if err != nil {
					return "", err
				}
				t, err := place.Resolve(in.Path)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Pattern) != "" {
					return s.glob(ctx, a, t, in.Pattern)
				}
				kids, err := s.ws.List(ctx, a, t.Abs)
				if err != nil {
					return "", err
				}
				if len(kids) == 0 {
					return "The folder is empty.", nil
				}
				var b strings.Builder
				for _, k := range kids {
					if k.Kind == workspace.KindDir {
						fmt.Fprintf(&b, "%s/\n", k.Name)
					} else {
						fmt.Fprintf(&b, "%s (%d bytes)\n", k.Name, k.Size)
					}
				}
				return b.String(), nil
			})},
		{Name: "read_file", Description: "Read a file of the user's: a text file, or the text of a PDF, Word, Excel or PowerPoint file (pages, sheets and slides start with a ## heading). Long files are read in parts with offset (first line, 1-based) and limit (number of lines, at most 2000).",
			Schema: obj(pathProp+`,"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1}`, "path"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct {
					Path          string
					Offset, Limit int
				}](raw)
				if err != nil {
					return "", err
				}
				t, err := place.Resolve(in.Path)
				if err != nil {
					return "", err
				}
				var data string
				if extract.Supported(t.Abs) {
					data, err = s.documentText(ctx, a, t.Abs)
				} else {
					data, _, err = s.readText(ctx, a, t.Abs, readMaxBytes)
				}
				if err != nil {
					return "", err
				}
				return pageLines(data, in.Offset, in.Limit)
			})},
		{Name: "write_file", Description: "Write a text file in the user's files, creating folders as needed. An existing file is replaced; the user can undo it. " + workfiles.ToolHint,
			Schema: obj(pathProp+`,"content":{"type":"string"}`, "path", "content"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path, Content string }](raw)
				if err != nil {
					return "", err
				}
				t, err := writable(place, in.Path)
				if err != nil {
					return "", err
				}
				n, err := s.put(ctx, a, t.Abs, in.Content, workspace.PutOptions{Overwrite: true}, "Chat wrote ")
				if err != nil {
					return "", err
				}
				changeCard(ctx, n.Rev, t.Show(t.Abs))
				return fmt.Sprintf("Wrote %d bytes to %s.", len(in.Content), t.Guest(t.Abs)), nil
			})},
		{Name: "edit_file", Description: "Replace text in a text file of the user's. old_string must match the file exactly once, with its spaces and line breaks; add lines around it to make it unique, or set replace_all to replace every match. The user can undo it.",
			Schema: obj(pathProp+`,"old_string":{"type":"string","description":"The exact text to replace."},"new_string":{"type":"string","description":"The text to put in its place."},"replace_all":{"type":"boolean","description":"Replace every match instead of exactly one."}`, "path", "old_string", "new_string"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct {
					Path       string `json:"path"`
					Old        string `json:"old_string"`
					New        string `json:"new_string"`
					ReplaceAll bool   `json:"replace_all"`
				}](raw)
				if err != nil {
					return "", err
				}
				switch {
				case in.Old == "":
					return "", errors.New("old_string must not be empty; use write_file to create a file")
				case in.Old == in.New:
					return "", errors.New("old_string and new_string are the same, so there is nothing to change")
				}
				t, err := writable(place, in.Path)
				if err != nil {
					return "", err
				}
				cur, node, err := s.readText(ctx, a, t.Abs, editMaxBytes)
				if err != nil {
					return "", err
				}
				count := strings.Count(cur, in.Old)
				switch {
				case count == 0:
					return "", errors.New("old_string was not found in the file; read it again and copy the text exactly")
				case count > 1 && !in.ReplaceAll:
					return "", fmt.Errorf("old_string matches %d times; add lines around it to make it unique, or set replace_all", count)
				}
				next := strings.ReplaceAll(cur, in.Old, in.New)
				if len(next) > editMaxBytes {
					return "", fmt.Errorf("the file would be larger than %d MiB", editMaxBytes>>20)
				}
				n, err := s.put(ctx, a, t.Abs, next, workspace.PutOptions{BaseVersion: node.Version}, "Chat edited ")
				if err != nil {
					return "", err
				}
				if n.ConflictOf != "" {
					changeCard(ctx, n.Rev, t.Show(n.Path))
					return fmt.Sprintf("The file changed while it was edited: the edited version was saved as %s and %s was left as it is.", t.Guest(n.Path), t.Guest(t.Abs)), nil
				}
				changeCard(ctx, n.Rev, t.Show(t.Abs))
				return fmt.Sprintf("Replaced %d occurrence(s) in %s.", count, t.Guest(t.Abs)), nil
			})},
		{Name: "move_file", Description: "Move or rename a file or folder of the user's.",
			Schema: obj(`"from":{"type":"string"},"to":{"type":"string"}`, "from", "to"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ From, To string }](raw)
				if err != nil {
					return "", err
				}
				from, err := writable(place, in.From)
				if err != nil {
					return "", err
				}
				to, err := writable(place, in.To)
				if err != nil {
					return "", err
				}
				if from.Abs == from.Root || to.Abs == to.Root {
					return "", workspace.ErrInvalid
				}
				n, err := s.ws.Move(ctx, a, from.Abs, to.Abs)
				if err != nil && !errors.Is(err, workspace.ErrCommitted) {
					return "", err
				}
				changeCard(ctx, n.Rev, to.Show(to.Abs))
				return "Moved to " + to.Guest(to.Abs) + ".", nil
			})},
		{Name: "delete_file", Description: "Delete a file or folder of the user's. It goes to the Trash, so the user can restore it.",
			Schema: obj(pathProp, "path"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path string }](raw)
				if err != nil {
					return "", err
				}
				t, err := writable(place, in.Path)
				if err != nil {
					return "", err
				}
				if t.Abs == t.Root {
					return "", workspace.ErrInvalid
				}
				if err := s.ws.Delete(ctx, a, t.Abs); err != nil {
					return "", err
				}
				return "Moved to the Trash.", nil
			})},
		{Name: "search_files", Description: "Search the user's files, by name and by the words inside them: text files, and the text of PDF, Word, Excel and PowerPoint files, so a document with a meaningless name is found by what it says. By default query is plain text (at most 30 hits). With regex true, query is a regular expression (extended syntax, as grep -E) matched against each line (at most 200 lines). Case does not matter unless ignore_case is false. A hit in a document names its page, sheet or slide and the line to pass to read_file as offset. When the answer says files are still being indexed, search again shortly before you conclude that nothing matches.",
			Schema: obj(`"query":{"type":"string"},"path":{"type":"string","description":"Search only this file, or below this folder."},"regex":{"type":"boolean"},"ignore_case":{"type":"boolean","description":"Default true."}`, "query"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct {
					Query      string `json:"query"`
					Path       string `json:"path"`
					Regex      bool   `json:"regex"`
					IgnoreCase *bool  `json:"ignore_case"`
				}](raw)
				if err != nil {
					return "", err
				}
				t, err := place.Resolve(in.Path)
				if err != nil {
					return "", err
				}
				q, err := newQuery(in.Query, in.Regex, in.IgnoreCase == nil || *in.IgnoreCase)
				if err != nil {
					return "", err
				}
				res, err := s.search(ctx, a, t.Abs, t.Guest, q)
				if err != nil {
					return "", err
				}
				// A scoped chat's own folder is a mount of its own: a search
				// with no path looks there too, after the agent's folder.
				if strings.TrimSpace(in.Path) == "" && place.Chat() != "" && len(res.Hits) < q.limit {
					if ct, err := place.Resolve(session.GuestRoot + "/" + session.GuestChat); err == nil {
						more, err := s.search(ctx, a, ct.Abs, ct.Guest, q)
						if err != nil {
							return "", err
						}
						res.Hits = append(res.Hits, more.Hits...)
						if len(res.Hits) > q.limit {
							res.Hits = res.Hits[:q.limit]
						}
						res.Indexing += more.Indexing
						res.Limited = res.Limited || more.Limited
					}
				}
				return searchAnswer(res), nil
			})},
	}
}

// searchAnswer writes the hits of search_files: a name per line, then
// path:line: text for a text file and path (page N, line L): text for a
// document, and a note when documents are still being indexed.
func searchAnswer(res SearchResult) string {
	var b strings.Builder
	for _, h := range res.Hits {
		switch {
		case h.Line == 0:
			fmt.Fprintf(&b, "%s\n", h.Path)
		case h.Document:
			fmt.Fprintf(&b, "%s (%s): %s\n", h.Path, hitPlace(h), h.Snippet)
		default:
			fmt.Fprintf(&b, "%s:%d: %s\n", h.Path, h.Line, h.Snippet)
		}
	}
	limited := ""
	if res.Limited {
		limited = searchLimitedNote + "\n"
	}
	switch {
	case res.Indexing > 0 && len(res.Hits) == 0:
		return limited + fmt.Sprintf("Nothing found yet: %s still being indexed. Search again in a moment before you conclude that nothing matches.", filesCount(res.Indexing))
	case res.Indexing > 0:
		fmt.Fprintf(&b, "[%s still being indexed; search again in a moment to include them.]\n", filesCount(res.Indexing))
	case len(res.Hits) == 0 && res.Limited:
		return "Nothing found in the part searched. " + searchLimitedNote
	case len(res.Hits) == 0:
		return "Nothing found."
	}
	return b.String() + limited
}

// searchLimitedNote says that a search stopped before it looked at every
// file, so a missing match is no proof.
const searchLimitedNote = "[results limited: the search stopped at its limits before it looked at every file; narrow the path or the query]"

func filesCount(n int) string {
	if n == 1 {
		return "1 file is"
	}
	return fmt.Sprintf("%d files are", n)
}

// hitPlace says where a document hit is: "page 3, line 40".
func hitPlace(h Hit) string {
	line := fmt.Sprintf("line %d", h.Line)
	switch h.Section.Kind {
	case extract.SectionSheet:
		return fmt.Sprintf("sheet %q, %s", h.Section.Name, line)
	case "":
		return line
	}
	return h.Section.Kind + " " + h.Section.Name + ", " + line
}

// pageLines returns lines offset to offset+limit-1 of text (1-based, at most
// readMaxLines), with a note on how to read on.
func pageLines(text string, offset, limit int) (string, error) {
	lines := strings.Split(text, "\n")
	start := offset
	if start < 1 {
		start = 1
	}
	if limit < 1 || limit > readMaxLines {
		limit = readMaxLines
	}
	if start > len(lines) {
		return "", fmt.Errorf("the file has %d lines", len(lines))
	}
	end := start - 1 + limit
	more := ""
	if end < len(lines) {
		more = fmt.Sprintf("\n[%d more lines; continue with offset %d]", len(lines)-end, end+1)
	} else {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n") + more, nil
}

// documentText returns the text of a PDF or Office file a may read: the
// indexed text when it was read from the file as it is now, else the text
// read on the spot. The index keeps only the first part of a long document;
// its whole text, once read, is kept in the index store by content hash, so
// paging through it with read_file reads the file once, not once per page.
func (s *Service) documentText(ctx context.Context, a workspace.Actor, abs string) (string, error) {
	r, n, err := s.ws.Open(ctx, a, abs)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	var keep textindex.Store // where to keep the whole text once read
	if ix := s.textIndex(); ix != nil {
		if e, err := ix.Store().Get(ctx, n.ID); err == nil && e.Hash == n.Blob {
			switch e.Status {
			case textindex.StatusReady:
				if !e.Truncated {
					return e.Text, nil
				}
				// The index keeps part of a long document. The open above
				// proved that a may read this content, and the whole text is
				// keyed by it.
				if text, ok, err := ix.Store().Document(ctx, n.Blob, textindex.ExtractorVersion); err == nil && ok {
					return text, nil
				}
				keep = ix.Store()
			case textindex.StatusSkipped:
				if e.Reason == textindex.ReasonNoText {
					return "", fmt.Errorf("%s is a PDF with no text layer: it is probably a scan, so its words are pictures and there is no text to read", n.Name)
				}
			}
		}
	}
	if n.Size > textindex.DefaultMaxFileBytes {
		return "", fmt.Errorf("%s is %d bytes, which is too large to read as text", n.Name, n.Size)
	}
	data, err := io.ReadAll(io.LimitReader(r, textindex.DefaultMaxFileBytes+1))
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, documentTimeout)
	defer cancel()
	l := extract.DefaultLimits
	l.Text = documentMaxText
	text, _, err := extract.ExtractWith(ctx, n.Name, data, l)
	switch {
	case errors.Is(err, extract.ErrNoText):
		return "", fmt.Errorf("%s is a PDF with no text layer: it is probably a scan, so its words are pictures and there is no text to read", n.Name)
	case errors.Is(err, extract.ErrInvalid):
		return "", fmt.Errorf("%s could not be read as a %s file", n.Name, strings.TrimPrefix(strings.ToLower(path.Ext(n.Name)), "."))
	case errors.Is(err, context.DeadlineExceeded):
		return "", fmt.Errorf("reading %s took too long", n.Name)
	case err != nil:
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("%s contains no text", n.Name)
	}
	if keep != nil {
		// Only for a file the index has an entry of, so a purge or a delete
		// prunes it with the entry.
		if err := keep.PutDocument(ctx, n.Blob, textindex.ExtractorVersion, text); err != nil {
			xlog.Debug("File text index: could not keep the whole text of a document", "error", err)
		}
	}
	return text, nil
}

// documentTimeout bounds reading one document for read_file, and
// documentMaxText the characters of text it reads.
const (
	documentTimeout = 60 * time.Second
	documentMaxText = 16 << 20
)

// readText reads a text file of at most max bytes.
func (s *Service) readText(ctx context.Context, a workspace.Actor, abs string, max int64) (string, workspace.Node, error) {
	r, n, err := s.ws.Open(ctx, a, abs)
	if err != nil {
		return "", workspace.Node{}, err
	}
	defer func() { _ = r.Close() }()
	if n.Size > max {
		return "", n, fmt.Errorf("the file is %d bytes, which is too large to read as text", n.Size)
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return "", n, err
	}
	if !isText(data) || !utf8.Valid(data) {
		return "", n, &NotTextError{Name: n.Name, Size: n.Size}
	}
	return string(data), n, nil
}

// NotTextError means a text tool met a binary file. The host adds a hint about
// the tools the chat has, so the model knows what to do instead.
type NotTextError struct {
	Name string
	Size int64
}

func (e *NotTextError) Error() string {
	return fmt.Sprintf("%s is not a text file (%d bytes)", e.Name, e.Size)
}

// put writes content as one change set of the chat.
func (s *Service) put(ctx context.Context, a workspace.Actor, abs, content string, o workspace.PutOptions, verb string) (workspace.Node, error) {
	tx, err := s.ws.Begin(ctx, a, verb+lastName(abs))
	if err != nil {
		return workspace.Node{}, err
	}
	n, err := tx.Put(ctx, abs, strings.NewReader(content), o)
	if err != nil {
		_ = tx.Rollback()
		return workspace.Node{}, err
	}
	cs, err := tx.Commit()
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return workspace.Node{}, err
	}
	n.Rev = cs.Rev
	s.Written(ctx, n, a.Principal)
	return n, nil
}

// globRE turns a find -path pattern into a regular expression: * matches any
// characters including /, ? one character and [...] a class.
func globRE(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := strings.IndexByte(pattern[i+1:], ']')
			if j < 0 {
				b.WriteString(regexp.QuoteMeta("["))
				continue
			}
			class := pattern[i+1 : i+1+j]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += j + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// glob lists the paths below t that match pattern, relative to t.
func (s *Service) glob(ctx context.Context, a workspace.Actor, t session.Target, pattern string) (string, error) {
	if len(pattern) > maxPatternLen || strings.ContainsRune(pattern, 0) {
		return "", fmt.Errorf("the pattern must be at most %d characters", maxPatternLen)
	}
	re, err := globRE(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(pattern), "./"), "/"))
	if err != nil {
		return "", fmt.Errorf("the pattern is not valid: %w", err)
	}
	var found []string
	visited := 0
	stopped := false
	var walk func(dir, prefix string) error
	walk = func(dir, prefix string) error {
		kids, err := s.ws.List(ctx, a, dir)
		if err != nil {
			return err
		}
		for _, k := range kids {
			if visited++; visited > searchMaxNodes {
				stopped = true
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel := prefix + k.Name
			if re.MatchString(rel) {
				if k.Kind == workspace.KindDir {
					found = append(found, rel+"/")
				} else {
					found = append(found, rel)
				}
			}
			if k.Kind == workspace.KindDir {
				if err := walk(k.Path, rel+"/"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(t.Abs, ""); err != nil {
		return "", err
	}
	if len(found) == 0 {
		if stopped {
			return fmt.Sprintf("No files match in the first %d entries; narrow the path.", searchMaxNodes), nil
		}
		return "No files match.", nil
	}
	sort.Strings(found)
	more := len(found) - globMax
	if more > 0 {
		found = found[:globMax]
	}
	out := strings.Join(found, "\n")
	switch {
	case more > 0:
		out += fmt.Sprintf("\n[%d more; narrow the pattern or the path]", more)
	case stopped:
		out += fmt.Sprintf("\n[stopped after %d entries; narrow the path]", searchMaxNodes)
	}
	return out, nil
}

// ChangeCardKind is the card of a file tool call that changed a file: its ID is
// "<rev>:<path>", the path in the home or, for a shared folder, the path the
// model used. The chat shows a note with Undo under the call, and the Undo
// undoes that change set. The model never sees the card.
const ChangeCardKind = session.ChangeCardKind

func changeCard(ctx context.Context, rev int64, rel string) {
	if rev > 0 && rel != "" {
		session.SetToolCard(ctx, session.ToolCard{Kind: ChangeCardKind, ID: fmt.Sprintf("%d:%s", rev, rel)})
	}
}

func lastName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// userError turns a workspace error into a sentence for the model.
func userError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, workspace.ErrReadOnly):
		return errors.New("not found")
	case errors.Is(err, workspace.ErrBadName):
		return errors.New("the path has a name that is not allowed (no .., no control characters)")
	case errors.Is(err, workspace.ErrExists):
		return errors.New("something with that name exists already")
	case errors.Is(err, workspace.ErrTooLarge):
		return errors.New("the file is too large")
	case errors.Is(err, workspace.ErrInvalid):
		return errors.New("that cannot be done to this path")
	case errors.Is(err, workspace.ErrSharedMove):
		return errors.New("this folder or a folder in it is shared, so it cannot move there; the user must stop sharing it first")
	}
	return err
}
