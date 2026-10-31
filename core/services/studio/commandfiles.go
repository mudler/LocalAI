package studio

import (
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
// home, or 0 when it has none (or the store cannot say: then every change set
// of the chat counts, which only lists files twice).
func (s *Service) commandMark(ctx context.Context, a workspace.Actor) int64 {
	if !s.d.Files.Available() || a.ChatID == "" {
		return 0
	}
	sets, err := s.d.Files.Store().History(ctx, userOf(a), workspace.HistoryQuery{Path: session.HomePath(a.Principal), ChatID: a.ChatID, Limit: 1})
	if err != nil || len(sets) == 0 {
		return 0
	}
	return sets[0].Rev
}

// userOf is the user's own actor, which reads the history of the whole home.
func userOf(a workspace.Actor) workspace.Actor {
	return workspace.Actor{Principal: a.Principal, Groups: a.Groups}
}

// commandFiles lists the files of the user's home that the chat's change sets
// after mark created, changed or deleted, oldest change first. A file that
// several change sets touched is listed once, with the last of them.
func (s *Service) commandFiles(ctx context.Context, a workspace.Actor, mark int64) []session.ToolFile {
	if !s.d.Files.Available() || a.ChatID == "" {
		return nil
	}
	home := session.HomePath(a.Principal)
	sets, err := s.d.Files.Store().History(ctx, userOf(a), workspace.HistoryQuery{Path: home, ChatID: a.ChatID, Limit: maxCommandSets})
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
		order   int
	}
	byID := map[int64]*node{}
	for _, cs := range sets {
		if cs.Rev <= mark {
			continue
		}
		for _, op := range cs.Ops {
			n := byID[op.NodeID]
			if n == nil {
				n = &node{created: op.Kind == workspace.OpCreate, order: len(byID)}
				byID[op.NodeID] = n
			}
			n.path, n.rev, n.gone = op.NewPath, cs.Rev, op.Kind == workspace.OpDelete
			if n.gone && op.NewPath == "" {
				n.path = op.OldPath
			}
		}
	}
	list := make([]*node, 0, len(byID))
	for _, n := range byID {
		list = append(list, n)
	}
	slices.SortFunc(list, func(x, y *node) int { return x.order - y.order })
	var out []session.ToolFile
	for _, n := range list {
		rel := s.d.Files.Rel(a.Principal, n.path)
		if rel == "" || rel == "/" {
			continue
		}
		// A folder is no file of the card: what is inside it is.
		if !n.gone {
			if st, err := s.d.Files.Store().Stat(ctx, userOf(a), n.path); err != nil || st.Kind != workspace.KindFile {
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
			out = withNudge(out, s.nudgeLine(ctx, a, created))
		}
		return out, nil
	}
}
