package studio

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// maxRulesNamed is how many AGENTS.md files one line names.
const maxRulesNamed = 2

// rulesNudge points the model at the AGENTS.md files that apply to what it
// works on, for one turn: a line in the turn note for the chat's folder and
// the home, and a line in the result of the first file tool call that touches
// another folder with rules. The model gets the path, never the text, so a
// file costs about 25 tokens and only when it exists. A new rulesNudge is made
// for each turn, so "once per folder" means once per turn.
type rulesNudge struct {
	s    *Service
	a    workspace.Actor
	home string

	mu    sync.Mutex
	has   map[string]bool // folder in the home -> it holds an AGENTS.md
	named map[string]bool // folders the model was pointed at, or got the rules of
}

// newRulesNudge returns the nudge of one turn, or nil without files. A
// trigger run got the rules of its folder and the folders above it in its
// first message: those count as named.
func (s *Service) newRulesNudge(ctx context.Context, a workspace.Actor, rec session.ChatRecord) *rulesNudge {
	if s.d.Files == nil || !s.d.Files.Available() {
		return nil
	}
	n := &rulesNudge{s: s, a: a, home: session.HomePath(a.Principal), has: map[string]bool{}, named: map[string]bool{}}
	if rec.Origin == session.OriginTrigger && s.triggers != nil {
		if dir := s.triggers.rulesFolder(ctx, a.Principal, rec.OriginID); dir != "" {
			for _, d := range upFrom(dir) {
				n.named[d] = true
			}
		}
	}
	return n
}

// upFrom lists dir and the folders above it, nearest first, to "/".
func upFrom(dir string) []string {
	dir = path.Clean("/" + dir)
	out := []string{dir}
	for dir != "/" {
		dir = path.Dir(dir)
		out = append(out, dir)
	}
	return out
}

// exists says whether dir (in the home) holds an AGENTS.md the chat can read.
// Each folder is checked once a turn.
func (n *rulesNudge) exists(ctx context.Context, dir string) bool {
	if v, ok := n.has[dir]; ok {
		return v
	}
	p := n.home + strings.TrimSuffix(dir, "/") + "/" + triggers.RulesFile
	node, err := n.s.d.Files.Store().Stat(ctx, n.a, p)
	v := err == nil && node.Kind == workspace.KindFile
	n.has[dir] = v
	return v
}

// pick names the nearest folders from dir up to the home that hold rules the
// model was not pointed at yet, at most maxRulesNamed, and marks them named.
func (n *rulesNudge) pick(ctx context.Context, dir string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var paths []string
	for _, d := range upFrom(dir) {
		if len(paths) == maxRulesNamed {
			break
		}
		if n.named[d] || !n.exists(ctx, d) {
			continue
		}
		n.named[d] = true
		paths = append(paths, guestRulesPath(d))
	}
	return rulesLine(paths)
}

func guestRulesPath(dir string) string {
	return session.GuestRoot + "/" + session.GuestHome + strings.TrimSuffix(dir, "/") + "/" + triggers.RulesFile
}

// rulesLine is the line for the model, or "" for no path.
func rulesLine(paths []string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return "Rules for this folder are in " + paths[0] + ". Read that file before you start."
	}
	return "Rules for these folders are in " + strings.Join(paths, " and ") + ". Read those files before you start."
}

// turnLine is the line of the turn note: the chat's own folder, the folders
// above it and the home.
func (n *rulesNudge) turnLine(ctx context.Context, rec session.ChatRecord) string {
	if n == nil {
		return ""
	}
	dir := "/"
	if rec.Folder != "" {
		dir = "/" + session.ChatsDir + "/" + rec.Folder
	}
	return n.pick(ctx, dir)
}

// rulesTools are the file tools whose calls can point at rules, and how to
// find the folders a call touches.
var rulesTools = map[string]func(args map[string]any) []string{
	"list_files": func(m map[string]any) []string { return []string{asDir(m["path"], true)} },
	"read_file":  func(m map[string]any) []string { return []string{asDir(m["path"], false)} },
	"write_file": func(m map[string]any) []string { return []string{asDir(m["path"], false)} },
	"move_file":  func(m map[string]any) []string { return []string{asDir(m["to"], false), asDir(m["from"], false)} },
}

// homePath turns a path a file tool got into a clean path in the home, or ""
// for a path outside it (a shared skill, for example).
func homePath(v any) string {
	p, _ := v.(string)
	p = strings.TrimSpace(p)
	guest := session.GuestRoot + "/" + session.GuestHome
	switch {
	case p == guest:
		return "/"
	case strings.HasPrefix(p, guest+"/"):
		p = strings.TrimPrefix(p, guest)
	case strings.HasPrefix(p, session.GuestRoot+"/"):
		return ""
	}
	if p == "" {
		return "/"
	}
	return path.Clean("/" + p)
}

// asDir is the folder a call works in: the path itself for a folder, else its parent.
func asDir(v any, isDir bool) string {
	p := homePath(v)
	if p == "" || isDir {
		return p
	}
	return path.Dir(p)
}

// wrap adds the line to the result of a file tool call that touches a folder
// with rules the model was not pointed at this turn. Reading an AGENTS.md
// counts as being pointed at it.
func (n *rulesNudge) wrap(tools []session.Tool) []session.Tool {
	if n == nil {
		return tools
	}
	for i, t := range tools {
		dirs := rulesTools[t.Name]
		if dirs == nil {
			continue
		}
		run, name := t.Run, t.Name
		tools[i].Run = func(ctx context.Context, raw json.RawMessage) (string, error) {
			out, err := run(ctx, raw)
			if err != nil {
				return out, err
			}
			var m map[string]any
			if json.Unmarshal(raw, &m) != nil {
				return out, nil
			}
			if name == "read_file" {
				if p := homePath(m["path"]); path.Base(p) == triggers.RulesFile {
					n.mu.Lock()
					n.named[path.Dir(p)] = true
					n.mu.Unlock()
				}
			}
			var lines []string
			for _, d := range dirs(m) {
				if d == "" {
					continue
				}
				if l := n.pick(ctx, d); l != "" {
					lines = append(lines, l)
				}
			}
			if len(lines) == 0 {
				return out, nil
			}
			return strings.TrimRight(out, "\n") + "\n\n" + strings.Join(lines, " "), nil
		}
	}
	return tools
}
