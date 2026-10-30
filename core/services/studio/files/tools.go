package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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

// toolPath accepts a path the way the model sees it, in the home: "/a.txt",
// "/workspace/home/a.txt", "workspace/home/a.txt" and "home/a.txt" are all
// "/a.txt".
func toolPath(p string) string {
	p = strings.TrimSpace(p)
	guest := session.GuestRoot + "/" + session.GuestHome
	if strings.HasPrefix(p, strings.TrimPrefix(session.GuestRoot, "/")+"/") {
		p = "/" + p
	}
	switch {
	case p == guest, p == session.GuestRoot, p == session.GuestHome:
		return "/"
	case strings.HasPrefix(p, guest+"/"):
		return strings.TrimPrefix(p, guest)
	case strings.HasPrefix(p, session.GuestHome+"/"):
		return strings.TrimPrefix(p, session.GuestHome)
	}
	return p
}

// HomePath turns a path the model wrote into a path inside the user's home,
// with the same spellings and the same refusal of a stray /workspace path as
// the file tools. Other tools that read the user's files (the media tools)
// use it.
func HomePath(p string) (string, error) {
	if err := strayGuestPath(p); err != nil {
		return "", err
	}
	return toolPath(p), nil
}

// strayGuestPath returns an error for a path below /workspace that is no
// place of the chat's view (/workspace/newsapp, say). Taken as a path in the
// home it would make a folder named "workspace" there, so it is refused with
// the right spelling.
func strayGuestPath(p string) error {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, strings.TrimPrefix(session.GuestRoot, "/")+"/") {
		p = "/" + p
	}
	rest, ok := strings.CutPrefix(p, session.GuestRoot+"/")
	if !ok {
		return nil
	}
	first, _, _ := strings.Cut(rest, "/")
	if first == session.GuestHome || strings.HasPrefix(first, session.GuestSharedPrefix) || first == "" {
		return nil
	}
	return fmt.Errorf("%s is not a place of the user's files: files live under %s/%s/. Did you mean %s/%s/%s? "+
		"To make an app the user can open, build a Page: %s/%s/pages/<name>/index.html",
		p, session.GuestRoot, session.GuestHome, session.GuestRoot, session.GuestHome, rest, session.GuestRoot, session.GuestHome)
}

// Errors of a write to a place the model may only read.
var (
	errSharedReadOnly = errors.New("the shared and built-in skills are read-only")
	errFolderReadOnly = errors.New("this shared folder is read-only for you")
)

// target is a path a tool was given, resolved in the workspace.
type target struct {
	abs string
	// write is false for a place the user may only read: the skill layers and
	// a shared folder shared with "Can read". The store checks the grant again.
	write   bool
	readErr error // what a write gets when write is false
	// root is the top of the place (the home, a skill layer or a shared
	// folder), which a move or a delete must never take away.
	root string
	// show turns a workspace path inside the place into the path the model
	// uses for it.
	show func(abs string) string
}

// locate resolves a path a tool was given: in the home, in a skill layer or in
// a shared folder of the user. A path with .. is refused wherever it points.
func (s *Service) locate(ctx context.Context, a workspace.Actor, p string) (target, error) {
	p = strings.TrimSpace(p)
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return target{}, workspace.ErrBadName
		}
	}
	if real, layer, guest := sharedLayer(p); real != "" {
		segs, err := workspace.SplitPath(real)
		if err != nil {
			return target{}, err
		}
		abs := workspace.JoinPath(segs)
		return target{abs: abs, readErr: errSharedReadOnly, root: layer, show: rebase(layer, guest)}, nil
	}
	if err := strayGuestPath(p); err != nil {
		return target{}, err
	}
	if strings.HasPrefix(p, strings.TrimPrefix(session.GuestRoot, "/")+"/") {
		p = "/" + p
	}
	if strings.HasPrefix(p, session.GuestRoot+"/"+session.GuestSharedPrefix) {
		return s.locateShared(ctx, a, p)
	}
	abs, err := s.Abs(a.Principal, toolPath(p))
	if err != nil {
		return target{}, err
	}
	rel := func(abs string) string { return s.Rel(a.Principal, abs) }
	return target{abs: abs, write: true, root: session.HomePath(a.Principal), show: rel}, nil
}

// locateShared resolves /workspace/shared-<name>/... with the names the
// chat's view gives the user's shared folders.
func (s *Service) locateShared(ctx context.Context, a workspace.Actor, p string) (target, error) {
	rest := strings.TrimPrefix(p, session.GuestRoot+"/")
	at, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	roots := s.sharedRoots
	s.mu.Unlock()
	if roots == nil {
		return target{}, workspace.ErrNotFound
	}
	shared, err := roots(ctx, a)
	if err != nil {
		return target{}, err
	}
	v, err := session.BuildView(ctx, s.ws, a, shared, quietLog)
	if err != nil {
		return target{}, err
	}
	for _, m := range v.Mounts {
		if m.At != at || m.At == session.GuestHome {
			continue
		}
		abs := m.Path
		if sub != "" {
			segs, err := workspace.SplitPath("/" + sub)
			if err != nil {
				return target{}, err
			}
			abs = strings.TrimSuffix(m.Path, "/") + strings.TrimSuffix(workspace.JoinPath(segs), "/")
		}
		return target{abs: abs, write: m.Mode >= workspace.ModeWrite, readErr: errFolderReadOnly, root: m.Path,
			show: rebase(m.Path, session.GuestRoot+"/"+m.At)}, nil
	}
	return target{}, workspace.ErrNotFound
}

// rebase returns a function that shows a path below from as the same path
// below to.
func rebase(from, to string) func(string) string {
	return func(abs string) string {
		if abs == from {
			return to
		}
		return to + strings.TrimPrefix(abs, from)
	}
}

// writable resolves a path for a tool that changes it.
func (s *Service) writable(ctx context.Context, a workspace.Actor, p string) (target, error) {
	t, err := s.locate(ctx, a, p)
	if err != nil {
		return target{}, err
	}
	if !t.write {
		return target{}, t.readErr
	}
	return t, nil
}

// Tools returns the file tools of one chat: list, read, write, edit, move and
// delete files, and search them, in the user's home and shared folders. They
// act as the user in that chat, so what they change is an agent change in the
// history, which the user can undo, and a delete goes to the Trash. A
// sub-agent gets the tools of its parent chat.
func (s *Service) Tools(a workspace.Actor) []session.Tool {
	if !s.Available() {
		return nil
	}
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
				t, err := s.locate(ctx, a, in.Path)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(in.Pattern) != "" {
					return s.glob(ctx, a, t, in.Pattern)
				}
				kids, err := s.ws.List(ctx, a, t.abs)
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
		{Name: "read_file", Description: "Read a text file of the user's. Long files are read in parts with offset (first line, 1-based) and limit (number of lines, at most 2000).",
			Schema: obj(pathProp+`,"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1}`, "path"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct {
					Path          string
					Offset, Limit int
				}](raw)
				if err != nil {
					return "", err
				}
				t, err := s.locate(ctx, a, in.Path)
				if err != nil {
					return "", err
				}
				data, _, err := s.readText(ctx, a, t.abs, readMaxBytes)
				if err != nil {
					return "", err
				}
				lines := strings.Split(data, "\n")
				start, limit := in.Offset, in.Limit
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
			})},
		{Name: "write_file", Description: "Write a text file in the user's files, creating folders as needed. An existing file is replaced; the user can undo it.",
			Schema: obj(pathProp+`,"content":{"type":"string"}`, "path", "content"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path, Content string }](raw)
				if err != nil {
					return "", err
				}
				t, err := s.writable(ctx, a, in.Path)
				if err != nil {
					return "", err
				}
				n, err := s.put(ctx, a, t.abs, in.Content, workspace.PutOptions{Overwrite: true}, "Chat wrote ")
				if err != nil {
					return "", err
				}
				changeCard(ctx, n.Rev, t.show(t.abs))
				return fmt.Sprintf("Wrote %d bytes to %s.", len(in.Content), t.show(t.abs)), nil
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
				t, err := s.writable(ctx, a, in.Path)
				if err != nil {
					return "", err
				}
				cur, node, err := s.readText(ctx, a, t.abs, editMaxBytes)
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
				n, err := s.put(ctx, a, t.abs, next, workspace.PutOptions{BaseVersion: node.Version}, "Chat edited ")
				if err != nil {
					return "", err
				}
				if n.ConflictOf != "" {
					changeCard(ctx, n.Rev, t.show(n.Path))
					return fmt.Sprintf("The file changed while it was edited: the edited version was saved as %s and the file was left as it is.", t.show(n.Path)), nil
				}
				changeCard(ctx, n.Rev, t.show(t.abs))
				return fmt.Sprintf("Replaced %d occurrence(s) in %s.", count, t.show(t.abs)), nil
			})},
		{Name: "move_file", Description: "Move or rename a file or folder of the user's.",
			Schema: obj(`"from":{"type":"string"},"to":{"type":"string"}`, "from", "to"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ From, To string }](raw)
				if err != nil {
					return "", err
				}
				from, err := s.writable(ctx, a, in.From)
				if err != nil {
					return "", err
				}
				to, err := s.writable(ctx, a, in.To)
				if err != nil {
					return "", err
				}
				if from.abs == from.root || to.abs == to.root {
					return "", workspace.ErrInvalid
				}
				n, err := s.ws.Move(ctx, a, from.abs, to.abs)
				if err != nil && !errors.Is(err, workspace.ErrCommitted) {
					return "", err
				}
				changeCard(ctx, n.Rev, to.show(to.abs))
				return "Moved.", nil
			})},
		{Name: "delete_file", Description: "Delete a file or folder of the user's. It goes to the Trash, so the user can restore it.",
			Schema: obj(pathProp, "path"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path string }](raw)
				if err != nil {
					return "", err
				}
				t, err := s.writable(ctx, a, in.Path)
				if err != nil {
					return "", err
				}
				if t.abs == t.root {
					return "", workspace.ErrInvalid
				}
				if err := s.ws.Delete(ctx, a, t.abs); err != nil {
					return "", err
				}
				return "Moved to the Trash.", nil
			})},
		{Name: "search_files", Description: "Search the user's files. By default query is plain text, found in file names and inside text files (at most 30 hits). With regex true, query is a regular expression (extended syntax, as grep -E) matched against each line of the text files (at most 200 lines). Case does not matter unless ignore_case is false.",
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
				t, err := s.locate(ctx, a, in.Path)
				if err != nil {
					return "", err
				}
				q, err := newQuery(in.Query, in.Regex, in.IgnoreCase == nil || *in.IgnoreCase)
				if err != nil {
					return "", err
				}
				hits, err := s.search(ctx, a, t.abs, t.show, q)
				if err != nil {
					return "", err
				}
				if len(hits) == 0 {
					return "Nothing found.", nil
				}
				var b strings.Builder
				for _, h := range hits {
					if h.Line == 0 {
						fmt.Fprintf(&b, "%s\n", h.Path)
					} else {
						fmt.Fprintf(&b, "%s:%d: %s\n", h.Path, h.Line, h.Snippet)
					}
				}
				return b.String(), nil
			})},
	}
}

// readText reads a text file of at most max bytes.
func (s *Service) readText(ctx context.Context, a workspace.Actor, abs string, max int64) (string, workspace.Node, error) {
	r, n, err := s.ws.Open(ctx, a, abs)
	if err != nil {
		return "", workspace.Node{}, err
	}
	defer r.Close()
	if n.Size > max {
		return "", n, fmt.Errorf("the file is %d bytes, which is too large to read as text", n.Size)
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return "", n, err
	}
	if !isText(data) || !utf8.Valid(data) {
		return "", n, fmt.Errorf("%s is not a text file (%d bytes)", n.Name, n.Size)
	}
	return string(data), n, nil
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
func (s *Service) glob(ctx context.Context, a workspace.Actor, t target, pattern string) (string, error) {
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
	if err := walk(t.abs, ""); err != nil {
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
const ChangeCardKind = "files_change"

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
	}
	return err
}
