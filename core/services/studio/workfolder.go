package studio

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

// The work folder of a chat (Chats/<chat>/work): the prompt of every turn
// names it, and the scripts, logs and drafts a chat makes go there instead of
// next to the user's files (see workfiles). The setting chats_work_folder
// turns the steering off; the interface still shows working files apart.
//
// The steering is soft. When a call leaves a helper file outside the work
// folder, its result gets one line that suggests moving it, at most once a
// turn of a chat. That count is kept per process: a turn runs on one process,
// so no other process needs it, and a lost count only repeats the line.

// maxNudged bounds the turns remembered for the nudge.
const maxNudged = 4096

// nudges remembers, per conversation, the last turn whose result got the line.
type nudges struct {
	mu   sync.Mutex
	turn map[string]int
}

// first reports whether the turn of the conversation has not had the line yet,
// and marks it.
func (n *nudges) first(k string, turn int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.turn == nil || len(n.turn) >= maxNudged {
		n.turn = map[string]int{}
	}
	if t, ok := n.turn[k]; ok && t == turn {
		return false
	}
	n.turn[k] = turn
	return true
}

// workFolderOn is the setting chats_work_folder.
func (s *Service) workFolderOn() bool {
	return s.d.WorkFolderOn == nil || s.d.WorkFolderOn()
}

// workFolderOf returns the chat folder name of the chat the actor works for
// and its turn count, when the steering is on and the chat has a folder name.
func (s *Service) workFolderOf(ctx context.Context, a workspace.Actor) (folder string, turns int) {
	if !s.workFolderOn() || !s.d.Files.Available() || a.ChatID == "" {
		return "", 0
	}
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return "", 0
	}
	return rec.Folder, rec.Turns
}

// subWorkBlock names the parent's work folder for a sub-agent, which saves in
// the parent's files. It is "" when the parent has no folder name yet.
func (s *Service) subWorkBlock(ctx context.Context, rec session.ChatRecord) string {
	if !s.workFolderOn() || !s.d.Files.Available() || rec.ParentID == "" {
		return ""
	}
	parent, err := s.d.Store.GetChat(ctx, rec.Principal, rec.ParentID)
	if err != nil || parent.Folder == "" {
		return ""
	}
	return workfiles.Prompt(workfiles.GuestFolder(parent.Folder))
}

// ensureWorkFolder makes the chat's work folder before a command runs, so a
// script can save into it at once. A failure is logged: the command runs.
func (s *Service) ensureWorkFolder(ctx context.Context, a workspace.Actor) {
	folder, _ := s.workFolderOf(ctx, a)
	if folder == "" {
		return
	}
	if err := session.EnsureWorkFolder(ctx, s.d.Files.Store(), userOf(a), a.Principal, folder); err != nil {
		s.log.Warn("studio: could not make the chat's work folder", "chat", a.ChatID, "error", err)
	}
}

// nudgeLine is the line for the first helper file among paths (paths of the
// home, or guest paths) that a call created outside the work folder, or ""
// when there is none or the turn had its line already.
func (s *Service) nudgeLine(ctx context.Context, a workspace.Actor, paths []string) string {
	stray := ""
	for _, p := range paths {
		if workfiles.Stray(p) {
			stray = p
			break
		}
	}
	if stray == "" {
		return ""
	}
	folder, turns := s.workFolderOf(ctx, a)
	if folder == "" || !s.nudged.first(key(a.Principal, a.ChatID), turns) {
		return ""
	}
	if !strings.HasPrefix(stray, session.GuestRoot+"/") {
		stray = session.GuestRoot + "/" + session.GuestHome + stray
	}
	return workfiles.Nudge(workfiles.GuestFolder(folder), stray)
}

// withNudge appends line to a tool result, on a line of its own.
func withNudge(out, line string) string {
	if line == "" {
		return out
	}
	return strings.TrimRight(out, "\n") + "\n" + line
}

// fileNudgeTools wraps write_file and move_file: a call that saved a helper
// file outside the work folder gets the nudge.
func (s *Service) fileNudgeTools(a workspace.Actor, tools []session.Tool) []session.Tool {
	for i, tool := range tools {
		field := map[string]string{"write_file": "path", "move_file": "to"}[tool.Name]
		if field == "" {
			continue
		}
		run := tool.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			out, err := run(ctx, args)
			if err != nil {
				return out, err
			}
			var in map[string]any
			_ = json.Unmarshal(args, &in)
			p, _ := in[field].(string)
			if p == "" {
				return out, nil
			}
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			return withNudge(out, s.nudgeLine(ctx, a, []string{p})), nil
		}
	}
	return tools
}
