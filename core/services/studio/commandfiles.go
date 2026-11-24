package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The files a command changed. A command (the Sandbox's bash or the built-in
// bash) saves what it wrote in the user's files as change sets of the chat
// (ChangeSet.ChatID), like the file tools do. The chat reads those change sets
// back from the shared store after the call and puts the files on the call's
// card (session.SetToolFiles), so the interface lists them under the step and
// the chat's file index, its chips and its preview pane see them too. Nothing
// is kept in memory: any frontend reads the same store.

// maxCommandSets bounds the change sets one call reads back.
const maxCommandSets = 50

// commandMark returns the newest revision of the chat's change sets in the
// files it can reach, or 0 when it has none (or the store cannot say: then
// every change set of the chat counts, which only lists files twice).
func (s *Service) commandMark(ctx context.Context, a workspace.Actor) int64 {
	if !s.d.Files.Available() || a.ChatID == "" {
		return 0
	}
	sets, err := s.chatSets(ctx, a, 1)
	if err != nil || len(sets) == 0 {
		return 0
	}
	return sets[0].Rev
}

// chatSets returns the newest change sets of the chat of a, newest first, as
// a can read them. A chat on the whole home reads the history of the home; a
// scoped chat cannot read the home, so it reads the history of each folder
// it reaches and the store keeps every change set inside its fence.
func (s *Service) chatSets(ctx context.Context, a workspace.Actor, limit int) ([]workspace.ChangeSet, error) {
	ws := s.d.Files.Store()
	if _, fenced := a.Fence(); !fenced {
		return ws.History(ctx, a, workspace.HistoryQuery{Path: session.HomePath(a.Principal), ChatID: a.ChatID, Limit: limit})
	}
	roots, err := ws.ReadablePrefixes(ctx, a)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	var out []workspace.ChangeSet
	for _, root := range roots {
		sets, err := ws.History(ctx, a, workspace.HistoryQuery{Path: root, ChatID: a.ChatID, Limit: limit})
		if err != nil {
			return nil, err
		}
		for _, cs := range sets {
			if !seen[cs.Rev] {
				seen[cs.Rev] = true
				out = append(out, cs)
			}
		}
	}
	slices.SortFunc(out, func(x, y workspace.ChangeSet) int { return cmp.Compare(y.Rev, x.Rev) })
	return out[:min(limit, len(out))], nil
}

// commandFiles lists the files that the chat's change sets
// after mark created, changed or deleted, oldest change first. A file that
// several change sets touched is listed once, with the last of them.
func (s *Service) commandFiles(ctx context.Context, a workspace.Actor, mark int64) []session.ToolFile {
	if !s.d.Files.Available() || a.ChatID == "" {
		return nil
	}
	sets, err := s.chatSets(ctx, a, maxCommandSets)
	if err != nil {
		s.log.Warn("studio: the files a command changed are not on its card", "chat", a.ChatID, "error", err)
		return nil
	}
	slices.Reverse(sets) // oldest first
	type node struct {
		path    string
		rev     int64
		created bool
		gone    bool
	}
	byID := map[int64]*node{}
	var list []*node // in the order of the first change
	for _, cs := range sets {
		if cs.Rev <= mark {
			continue
		}
		for _, op := range cs.Ops {
			n := byID[op.NodeID]
			if n == nil {
				n = &node{created: op.Kind == workspace.OpCreate}
				byID[op.NodeID] = n
				list = append(list, n)
			}
			n.path, n.rev, n.gone = op.NewPath, cs.Rev, op.Kind == workspace.OpDelete
			if n.gone && op.NewPath == "" {
				n.path = op.OldPath
			}
		}
	}
	var out []session.ToolFile
	for _, n := range list {
		rel := s.d.Files.Rel(a.Principal, n.path)
		if rel == "" || rel == "/" {
			continue
		}
		// A folder is no file of the card: what is inside it is.
		if !n.gone {
			if st, err := s.d.Files.Store().Stat(ctx, a, n.path); err != nil || st.Kind != workspace.KindFile {
				continue
			}
		}
		op := session.ToolFileChanged
		switch {
		case n.gone && n.created:
			continue // made and removed by the same command: the user never had it
		case n.gone:
			op = session.ToolFileDeleted
		case n.created:
			op = session.ToolFileCreated
		}
		out = append(out, session.ToolFile{Path: rel, Rev: n.rev, Op: op})
	}
	return out
}

// withCommandFiles wraps the run of a command tool. Before the call it makes
// the chat's work folder; after a successful call it puts the files the call
// changed on its card and, when there are any, tells the chat's clients that
// the chat's files changed, and nudges the model when it left a helper file
// outside the work folder.
func (s *Service) withCommandFiles(a workspace.Actor, run func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		s.ensureWorkFolder(ctx, a)
		mark := s.commandMark(ctx, a)
		out, err := run(ctx, args)
		if err != nil {
			return out, err
		}
		if changed := s.commandFiles(ctx, a, mark); len(changed) > 0 {
			session.SetToolFiles(ctx, changed)
			s.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindFiles})
			var created []string
			for _, f := range changed {
				if f.Op == session.ToolFileCreated {
					created = append(created, f.Path)
				}
			}
			// a is the actor of the chat's access: it is fenced exactly
			// when the access is scoped.
			out = withNudge(out, s.nudgeLine(ctx, a, workspace.HasFence(a), created))
		}
		return out, nil
	}
}
