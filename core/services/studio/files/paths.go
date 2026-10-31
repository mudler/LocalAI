package files

import (
	"context"
	"errors"
	"slices"
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

// What a managed path holds (see Managed).
const (
	ManagedSettings = "localai" // LocalAI/ and the settings files in it
	ManagedChats    = "chats"   // Chats/, one folder per chat
)

// Managed says whether LocalAI manages a path inside the home: ManagedSettings
// for LocalAI/ and what is in it, ManagedChats for the Chats folder itself,
// and "" for the user's own files. The Files page marks these paths and asks
// before they are deleted or moved.
func Managed(rel string) string {
	switch {
	case rel == "/"+session.SettingsDir || strings.HasPrefix(rel, "/"+session.SettingsDir+"/"):
		return ManagedSettings
	case rel == "/"+session.ChatsDir:
		return ManagedChats
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
	// Files, Dirs and Bytes say what deleting the entry for good would remove:
	// the files and the folders (a folder with its content counts once for
	// itself and once for each item in it) and the size of the files.
	Files int
	Dirs  int
	Bytes int64
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
	s.sizeTrash(ctx, principal, out)
	return out, nil
}

// sizeTrash fills in what each entry would remove. A store that cannot count
// leaves the sizes at zero.
func (s *Service) sizeTrash(ctx context.Context, principal string, entries []TrashEntry) {
	pg, ok := s.ws.(workspace.Purger)
	if !ok || len(entries) == 0 {
		return
	}
	revs := make([]int64, len(entries))
	for i, e := range entries {
		revs[i] = e.Rev
	}
	res, err := pg.Purge(ctx, s.Actor(principal), session.HomePath(principal), revs, true)
	if err != nil {
		return
	}
	for i := range entries {
		c := res.ByRev[entries[i].Rev]
		entries[i].Files, entries[i].Dirs, entries[i].Bytes = c.Files, c.Dirs, c.Bytes
	}
}

// ErrNoPurge means the workspace store cannot delete for good.
var ErrNoPurge = errors.New("the workspace cannot empty the Trash")

// purger returns the store as a Purger.
func (s *Service) purger() (workspace.Purger, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	pg, ok := s.ws.(workspace.Purger)
	if !ok {
		return nil, ErrNoPurge
	}
	return pg, nil
}

// EmptyTrash removes everything in the user's Trash for good. With dryRun it
// only counts. It is every deleted item in the home, including
// the ones that the list leaves out because it is limited. It is a user's
// action: the agent tools do not reach it.
func (s *Service) EmptyTrash(ctx context.Context, principal string, dryRun bool) (workspace.PurgeCount, error) {
	pg, err := s.purger()
	if err != nil {
		return workspace.PurgeCount{}, err
	}
	res, err := pg.PurgeAll(ctx, s.Actor(principal), session.HomePath(principal), dryRun)
	return res.PurgeCount, err
}

// PurgeTrashEntry deletes one entry of the user's Trash for good. A revision
// that is not in the user's Trash is workspace.ErrNotFound. It is a user's
// action: the agent tools do not reach it.
func (s *Service) PurgeTrashEntry(ctx context.Context, principal string, rev int64) (workspace.PurgeCount, error) {
	pg, err := s.purger()
	if err != nil {
		return workspace.PurgeCount{}, err
	}
	entries, err := s.Trash(ctx, principal, 0)
	if err != nil {
		return workspace.PurgeCount{}, err
	}
	if !slices.ContainsFunc(entries, func(e TrashEntry) bool { return e.Rev == rev }) {
		return workspace.PurgeCount{}, workspace.ErrNotFound
	}
	res, err := pg.Purge(ctx, s.Actor(principal), session.HomePath(principal), []int64{rev}, false)
	return res.PurgeCount, err
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
