package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Limits of what one file tool call returns to the model.
const (
	readMaxBytes = 4 << 20
	readMaxLines = 2000
)

// toolPath accepts a path the way the model sees it: inside the home ("/a.txt")
// or in the sandbox's view ("/workspace/home/a.txt").
func toolPath(p string) string {
	p = strings.TrimSpace(p)
	guest := session.GuestRoot + "/" + session.GuestHome
	switch {
	case p == guest:
		return "/"
	case strings.HasPrefix(p, guest+"/"):
		return strings.TrimPrefix(p, guest)
	}
	return p
}

// errSharedReadOnly is what a write to the shared or built-in skills gets.
var errSharedReadOnly = errors.New("the shared and built-in skills are read-only")

// readPath resolves a path a read tool was given: inside the home, or in a
// shared skill layer. A path with .. is refused either way.
func (s *Service) readPath(principal, p string) (string, error) {
	p = strings.TrimSpace(p)
	if shared := SharedGuestPath(p); shared != "" {
		segs, err := workspace.SplitPath(shared)
		if err != nil {
			return "", err
		}
		return workspace.JoinPath(segs), nil
	}
	return s.Abs(principal, toolPath(p))
}

// writePath resolves a path a write tool was given: inside the home only.
func (s *Service) writePath(principal, p string) (string, error) {
	if SharedGuestPath(strings.TrimSpace(p)) != "" {
		return "", errSharedReadOnly
	}
	return s.Abs(principal, toolPath(p))
}

// Tools returns the file tools of one chat: list, read, write, move and delete
// files in the user's home, and search them. They act as the user in that chat,
// so what they change is an agent change in the history, which the user can
// undo, and a delete goes to the Trash. They are not given to sub-agents.
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
	pathProp := `"path":{"type":"string","description":"A path in the user's files. / is their home folder."}`
	return []session.Tool{
		{Name: "list_files", Description: "List a folder of the user's files.",
			Schema: obj(pathProp),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				var in struct{ Path string }
				_ = json.Unmarshal(raw, &in)
				abs, err := s.readPath(a.Principal, in.Path)
				if err != nil {
					return "", err
				}
				kids, err := s.ws.List(ctx, a, abs)
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
		{Name: "read_file", Description: "Read a text file of the user's. Long files are read in parts with offset (first line, 1-based) and limit (number of lines).",
			Schema: obj(pathProp+`,"offset":{"type":"integer"},"limit":{"type":"integer"}`, "path"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				var in struct {
					Path          string
					Offset, Limit int
				}
				_ = json.Unmarshal(raw, &in)
				abs, err := s.readPath(a.Principal, in.Path)
				if err != nil {
					return "", err
				}
				r, n, err := s.ws.Open(ctx, a, abs)
				if err != nil {
					return "", err
				}
				defer r.Close()
				if n.Size > readMaxBytes {
					return "", fmt.Errorf("the file is %d bytes, which is too large to read as text", n.Size)
				}
				data, err := io.ReadAll(r)
				if err != nil {
					return "", err
				}
				if !isText(data) {
					return "", fmt.Errorf("%s is not a text file (%d bytes)", n.Name, n.Size)
				}
				lines := strings.Split(string(data), "\n")
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
				var in struct{ Path, Content string }
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", workspace.ErrInvalid
				}
				abs, err := s.writePath(a.Principal, in.Path)
				if err != nil {
					return "", err
				}
				tx, err := s.ws.Begin(ctx, a, "Chat wrote "+lastName(abs))
				if err != nil {
					return "", err
				}
				if _, err := tx.Put(ctx, abs, strings.NewReader(in.Content), workspace.PutOptions{Overwrite: true}); err != nil {
					_ = tx.Rollback()
					return "", err
				}
				if _, err := tx.Commit(); err != nil && !errors.Is(err, workspace.ErrCommitted) {
					return "", err
				}
				return fmt.Sprintf("Wrote %d bytes to %s.", len(in.Content), s.Rel(a.Principal, abs)), nil
			})},
		{Name: "move_file", Description: "Move or rename a file or folder of the user's.",
			Schema: obj(`"from":{"type":"string"},"to":{"type":"string"}`, "from", "to"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				var in struct{ From, To string }
				_ = json.Unmarshal(raw, &in)
				from, err := s.writePath(a.Principal, in.From)
				if err != nil {
					return "", err
				}
				to, err := s.writePath(a.Principal, in.To)
				if err != nil {
					return "", err
				}
				if s.Rel(a.Principal, from) == "/" || s.Rel(a.Principal, to) == "/" {
					return "", workspace.ErrInvalid
				}
				if _, err := s.ws.Move(ctx, a, from, to); err != nil && !errors.Is(err, workspace.ErrCommitted) {
					return "", err
				}
				return "Moved.", nil
			})},
		{Name: "delete_file", Description: "Delete a file or folder of the user's. It goes to the Trash, so the user can restore it.",
			Schema: obj(pathProp, "path"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				var in struct{ Path string }
				_ = json.Unmarshal(raw, &in)
				abs, err := s.writePath(a.Principal, in.Path)
				if err != nil {
					return "", err
				}
				if s.Rel(a.Principal, abs) == "/" {
					return "", workspace.ErrInvalid
				}
				if err := s.ws.Delete(ctx, a, abs); err != nil {
					return "", err
				}
				return "Moved to the Trash.", nil
			})},
		{Name: "search_files", Description: "Search the user's files by name and by the text inside them. Case does not matter.",
			Schema: obj(`"query":{"type":"string"},"path":{"type":"string","description":"Search only below this folder."}`, "query"),
			Run: call(func(ctx context.Context, raw json.RawMessage) (string, error) {
				var in struct{ Query, Path string }
				_ = json.Unmarshal(raw, &in)
				hits, err := s.Search(ctx, a.Principal, in.Query, toolPath(in.Path), 30)
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
