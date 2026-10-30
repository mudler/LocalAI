package files

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Actor returns the actor that acts for a user. Every user is in the
// EveryoneGroup, so a grant to that group reaches them all.
func (s *Service) Actor(principal string) workspace.Actor {
	return UserActor(principal)
}

// UserActor is the actor of a user outside a chat: the principal in the
// EveryoneGroup.
func UserActor(principal string) workspace.Actor {
	return workspace.Actor{Principal: principal, Groups: []string{EveryoneGroup}}
}

// Abs turns a path inside the user's home ("/" is the home) into the
// workspace path. A path that names a parent, a bad name or a control
// character is workspace.ErrBadName, so a user can never name a path outside
// their own home.
func (s *Service) Abs(principal, rel string) (string, error) {
	if !validPrincipal(principal) {
		return "", workspace.ErrBadName
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	segs, err := workspace.SplitPath(rel)
	if err != nil {
		return "", err
	}
	return session.HomePath(principal) + strings.TrimSuffix(workspace.JoinPath(segs), "/"), nil
}

// Rel turns a workspace path into a path inside the user's home. A path
// outside the home gives "".
func (s *Service) Rel(principal, abs string) string {
	home := session.HomePath(principal)
	switch {
	case abs == home:
		return "/"
	case strings.HasPrefix(abs, home+"/"):
		return strings.TrimPrefix(abs, home)
	}
	return ""
}

// TrashEntry is a deletion that can be undone.
type TrashEntry struct {
	Rev   int64
	Title string
	At    time.Time
	// Paths are what the change set deleted, inside the home. A folder is one
	// path; its files are not listed.
	Paths []string
}

// Trash lists the deletions in the user's home that nobody has undone, newest
// first. Restoring one is Undo of its revision.
func (s *Service) Trash(ctx context.Context, principal string, limit int) ([]TrashEntry, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	home := session.HomePath(principal)
	hist, err := s.ws.History(ctx, s.Actor(principal), workspace.HistoryQuery{Path: home, Limit: 1000})
	if err != nil {
		return nil, err
	}
	undone := map[int64]bool{}
	for _, cs := range hist {
		if cs.Undoes != 0 {
			undone[cs.Undoes] = true
		}
	}
	var out []TrashEntry
	for _, cs := range hist {
		if cs.Kind == workspace.ChangeUndo || undone[cs.Rev] {
			continue
		}
		var gone []string
		for _, op := range cs.Ops {
			if op.Kind == workspace.OpDelete {
				if rel := s.Rel(principal, op.OldPath); rel != "" {
					gone = append(gone, rel)
				}
			}
		}
		gone = topLevel(gone)
		if len(gone) == 0 {
			continue
		}
		out = append(out, TrashEntry{Rev: cs.Rev, Title: cs.Title, At: cs.CreatedAt, Paths: gone})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// topLevel drops every path that lies under another path of the list.
func topLevel(paths []string) []string {
	sort.Strings(paths)
	var out []string
	for _, p := range paths {
		if n := len(out); n > 0 && (p == out[n-1] || strings.HasPrefix(p, out[n-1]+"/")) {
			continue
		}
		out = append(out, p)
	}
	return out
}
