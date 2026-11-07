// SPDX-License-Identifier: MIT
package sharedfolders

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

// A person can share a folder of their own home: read or write, with another
// account or with Everyone. The grant is the same workspace grant as for an
// administrator's shared folder, in the same table, written by this service's
// administrator actor after it checked the person. The store keeps checking
// every file call against it, so a revoke or a lowering holds from the next
// call of the grantee on every frontend.
//
// Only the person who owns the home shares, and only from outside any chat:
// a chat, an agent or a tool acts with a chat ID or a fence and is refused.
// The folder must be inside the home, not the home itself and not a folder
// LocalAI manages there or reads with the owner's authority (LocalAI/, Chats/,
// Memory/, pages/ or any .skills folder: workfiles.Managed). A folder that is
// not one is ErrNotShareable, whatever the reason, so a caller cannot learn
// what exists.
//
// The owner sees and may revoke every grant on a folder of the home, whoever
// made it and wherever it is, a managed folder included: a grant made before
// these rules, or by an administrator, is never out of the owner's reach.

// Errors of owner sharing.
var (
	ErrNotShareable = errors.New("shared folders: you cannot share this folder")
	ErrSharingOff   = errors.New("shared folders: folders cannot be shared on this server")
	ErrBadQuery     = errors.New("shared folders: the search text is 3 to 64 characters")
	// ErrSearchLimited means the person searched for people too often.
	ErrSearchLimited = errors.New("shared folders: too many searches for people, wait a moment")
)

// maxSearch is the most people one search of the user picker returns.
const maxSearch = 20

// Directory is the account directory of the user picker. Without one (no
// accounts, local-owner mode) folders cannot be shared by their owners.
type Directory interface {
	// Accounts is the user picker of the sharing routes, the one of every
	// kind: active named accounts whose display name starts with query, or
	// the account without a display name whose whole address is query, at
	// most limit of them, never caller, as an ID and a display name only. It
	// answers ErrBadQuery for a query it does not take and ErrSearchLimited
	// when caller has searched too often.
	Accounts(ctx context.Context, caller, query string, limit int) ([]User, error)
	// Active returns the active named account id, and ErrUnknownUser for an
	// account that does not exist or is not active.
	Active(ctx context.Context, id string) (User, error)
}

// SharingAvailable says whether people may share their own folders here.
func (s *Service) SharingAvailable() bool {
	_, err := s.store()
	return err == nil && s.o.Directory != nil
}

func (s *Service) ownerStore() (workspace.Store, error) {
	st, err := s.store()
	if err != nil {
		return nil, err
	}
	if s.o.Directory == nil {
		return nil, ErrSharingOff
	}
	return st, nil
}

// isPerson says whether a is a person acting outside any chat.
func isPerson(a workspace.Actor) bool {
	return a.ChatID == "" && !workspace.HasFence(a) && validMember(a.Principal)
}

// homeRel returns the path of p inside the home of principal, and false for a
// path that is not strictly inside it.
func homeRel(principal, p string) (string, bool) {
	home := session.HomePath(principal)
	rel, ok := strings.CutPrefix(p, home+"/")
	if !ok || rel == "" {
		return "", false
	}
	return "/" + rel, true
}

// shareable returns the folder id when the person a may share it.
func (s *Service) shareable(ctx context.Context, st workspace.Store, a workspace.Actor, id int64) (workspace.Node, error) {
	n, err := s.ownFolder(ctx, st, a, id)
	if err != nil {
		return workspace.Node{}, err
	}
	if rel, _ := homeRel(a.Principal, n.Path); workfiles.Managed(rel) {
		return workspace.Node{}, ErrNotShareable
	}
	// The owner gives at most what the owner has: write, which the home
	// grant gives. A folder the owner holds read only is not shared.
	v, err := st.View(ctx, a, []workspace.MountRequest{{Path: n.Path, At: "x", Mode: workspace.ModeWrite}})
	if err != nil || len(v.Mounts) != 1 || v.Mounts[0].Mode != workspace.ModeWrite {
		return workspace.Node{}, ErrNotShareable
	}
	return n, nil
}

// ownFolder returns the folder id when it is strictly inside the home of the
// person a, managed or not: the folders whose grants the owner sees and
// revokes.
func (s *Service) ownFolder(ctx context.Context, st workspace.Store, a workspace.Actor, id int64) (workspace.Node, error) {
	if !isPerson(a) || id <= 0 {
		return workspace.Node{}, ErrNotShareable
	}
	n, err := st.NodeByID(ctx, a, id)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) || errors.Is(err, workspace.ErrReadOnly) {
			return workspace.Node{}, ErrNotShareable
		}
		return workspace.Node{}, err
	}
	if n.Kind != workspace.KindDir {
		return workspace.Node{}, ErrNotShareable
	}
	if _, ok := homeRel(a.Principal, n.Path); !ok {
		return workspace.Node{}, ErrNotShareable
	}
	return n, nil
}

// memberName is the display name of a member for the owner.
func (s *Service) memberName(ctx context.Context, principal string) string {
	if principal == Everyone {
		return Everyone
	}
	if s.o.Directory != nil {
		if u, err := s.o.Directory.Active(ctx, principal); err == nil && u.Name != "" {
			return u.Name
		}
	}
	return s.userName(ctx, principal)
}

func sortMembers(ms []Member) {
	slices.SortStableFunc(ms, func(a, b Member) int {
		if (a.Principal == Everyone) != (b.Principal == Everyone) {
			if a.Principal == Everyone {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// describeOwned returns the owner's view of a folder: its path in the home and
// its members.
func (s *Service) describeOwned(ctx context.Context, st workspace.Store, owner string, n workspace.Node) (Folder, error) {
	gl, ok := st.(workspace.GrantLister)
	if !ok {
		return Folder{}, errors.New("shared folders: the workspace store cannot list grants")
	}
	grants, err := gl.Grants(ctx, s.admin, n.Path)
	if err != nil {
		return Folder{}, err
	}
	rel, _ := homeRel(owner, n.Path)
	f := Folder{ID: n.ID, Name: n.Name, Path: rel, Members: []Member{}}
	for _, g := range grants {
		if g.Principal == owner {
			continue
		}
		f.Members = append(f.Members, Member{Principal: g.Principal, Name: s.memberName(ctx, g.Principal), Mode: g.Mode})
	}
	sortMembers(f.Members)
	return f, nil
}

// Owned returns the folders of the person's home that are shared, with their
// members, by path: every grant on a folder strictly inside the home, whoever
// made it, managed folders included.
func (s *Service) Owned(ctx context.Context, a workspace.Actor) ([]Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return nil, err
	}
	if !isPerson(a) {
		return nil, ErrNotShareable
	}
	gt, ok := st.(workspace.GrantTree)
	if !ok {
		return nil, errors.New("shared folders: the workspace store cannot list the grants of a home")
	}
	grants, err := gt.GrantsBelow(ctx, s.admin, session.HomePath(a.Principal))
	if errors.Is(err, workspace.ErrNotFound) {
		return []Folder{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Folder{}
	for _, g := range grants {
		if g.Principal == a.Principal {
			continue
		}
		rel, ok := homeRel(a.Principal, g.Path)
		if !ok {
			continue
		}
		if len(out) == 0 || out[len(out)-1].ID != g.NodeID {
			out = append(out, Folder{ID: g.NodeID, Name: path.Base(g.Path), Path: rel, Members: []Member{}})
		}
		last := &out[len(out)-1]
		last.Members = append(last.Members, Member{Principal: g.Principal, Name: s.memberName(ctx, g.Principal), Mode: g.Mode})
	}
	for i := range out {
		sortMembers(out[i].Members)
	}
	return out, nil
}

// OwnGrants returns one folder of the person's home with its members: a
// folder the person may share, or another folder of the home that still has
// members, so that the person can revoke them.
func (s *Service) OwnGrants(ctx context.Context, a workspace.Actor, id int64) (Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.revocable(ctx, st, a, id)
	if err != nil {
		return Folder{}, err
	}
	return s.describeOwned(ctx, st, a.Principal, n)
}

// revocable returns the folder id when the person a may share it, or when it
// is another folder of their home that still has members (a managed folder
// that got a grant before these rules, or a grant an administrator made), so
// that the person sees and revokes them. Any other folder is ErrNotShareable.
func (s *Service) revocable(ctx context.Context, st workspace.Store, a workspace.Actor, id int64) (workspace.Node, error) {
	n, err := s.shareable(ctx, st, a, id)
	if !errors.Is(err, ErrNotShareable) {
		return n, err
	}
	n, err = s.ownFolder(ctx, st, a, id)
	if err != nil {
		return workspace.Node{}, err
	}
	f, err := s.describeOwned(ctx, st, a.Principal, n)
	if err != nil {
		return workspace.Node{}, err
	}
	if len(f.Members) == 0 {
		return workspace.Node{}, ErrNotShareable
	}
	return n, nil
}

// grantee checks the principal an owner shares with: Everyone, or an active
// named account other than the owner, named by its ID.
func (s *Service) grantee(ctx context.Context, owner, ref string) (string, error) {
	p := strings.TrimSpace(ref)
	if p == Everyone {
		return Everyone, nil
	}
	if !validMember(p) || p == owner {
		return "", ErrUnknownUser
	}
	u, err := s.o.Directory.Active(ctx, p)
	if err != nil {
		if errors.Is(err, ErrUnknownUser) {
			return "", ErrUnknownUser
		}
		return "", err
	}
	if u.ID != p {
		return "", ErrUnknownUser
	}
	return p, nil
}

// OwnSetMember gives principal (an account ID or Everyone) the mode read or
// write on a folder of the person's home, or changes the mode. A lowering
// reports the lost access before it returns.
func (s *Service) OwnSetMember(ctx context.Context, a workspace.Actor, id int64, principal string, mode workspace.Mode) (Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.shareable(ctx, st, a, id)
	if err != nil {
		return Folder{}, err
	}
	if mode != workspace.ModeRead && mode != workspace.ModeWrite {
		return Folder{}, ErrBadMode
	}
	p, err := s.grantee(ctx, a.Principal, principal)
	if err != nil {
		return Folder{}, err
	}
	f, err := s.describeOwned(ctx, st, a.Principal, n)
	if err != nil {
		return Folder{}, err
	}
	old := workspace.ModeNone
	if i := slices.IndexFunc(f.Members, func(m Member) bool { return m.Principal == p }); i >= 0 {
		old = f.Members[i].Mode
	} else {
		if len(f.Members) >= MaxMembers {
			return Folder{}, fmt.Errorf("%w: a shared folder has at most %d members", ErrLimit, MaxMembers)
		}
		if len(f.Members) == 0 {
			owned, err := s.Owned(ctx, a)
			if err != nil {
				return Folder{}, err
			}
			if len(owned) >= MaxFolders {
				return Folder{}, fmt.Errorf("%w: a person shares at most %d folders", ErrLimit, MaxFolders)
			}
		}
	}
	if err := st.Grant(ctx, s.admin, p, n.Path, mode); err != nil {
		return Folder{}, err
	}
	s.audit(ctx, Event{Actor: a.Principal, Action: ActionGrant, FolderID: n.ID, Folder: n.Path, Principal: p, Mode: mode})
	if old > mode {
		s.accessLost(ctx, p)
	}
	return s.describeOwned(ctx, st, a.Principal, n)
}

// OwnRemoveMember takes away the grant of principal on a folder of the
// person's home: one the person may share, or another one that still has
// members (see revocable). A member who has no grant is not an error. The lost
// access is reported before it returns.
func (s *Service) OwnRemoveMember(ctx context.Context, a workspace.Actor, id int64, principal string) (Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.revocable(ctx, st, a, id)
	if err != nil {
		return Folder{}, err
	}
	p := strings.TrimSpace(principal)
	if p == "" || p == a.Principal {
		return Folder{}, ErrUnknownUser
	}
	f, err := s.describeOwned(ctx, st, a.Principal, n)
	if err != nil {
		return Folder{}, err
	}
	had := slices.ContainsFunc(f.Members, func(m Member) bool { return m.Principal == p })
	if err := st.Revoke(ctx, s.admin, p, n.Path); err != nil {
		return Folder{}, err
	}
	s.audit(ctx, Event{Actor: a.Principal, Action: ActionRevoke, FolderID: n.ID, Folder: n.Path, Principal: p})
	if had {
		s.accessLost(ctx, p)
	}
	return s.describeOwned(ctx, st, a.Principal, n)
}

// SearchAccounts is the user picker of a folder of the person's home. Only
// the owner of a folder that can be shared searches, with the picker of the
// directory (see Directory.Accounts), which never returns the owner.
func (s *Service) SearchAccounts(ctx context.Context, a workspace.Actor, id int64, query string, limit int) ([]User, error) {
	st, err := s.ownerStore()
	if err != nil {
		return nil, err
	}
	if _, err := s.shareable(ctx, st, a, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxSearch {
		limit = maxSearch
	}
	found, err := s.o.Directory.Accounts(ctx, a.Principal, query, limit)
	if err != nil {
		return nil, err
	}
	out := []User{}
	for _, u := range found {
		if u.ID == a.Principal || !validMember(u.ID) {
			continue
		}
		out = append(out, User{ID: u.ID, Name: u.Name})
	}
	return out, nil
}

// accessLost tells the host that principal may have lost access, so it stops
// the work that still holds it.
func (s *Service) accessLost(ctx context.Context, principal string) {
	if s.o.AccessLost != nil {
		s.o.AccessLost(ctx, principal)
	}
}

// homeOwner returns the principal whose home holds p, or "".
func homeOwner(p string) string {
	rest, ok := strings.CutPrefix(p, session.UsersRoot+"/")
	if !ok {
		return ""
	}
	owner, _, _ := strings.Cut(rest, "/")
	return owner
}

// OwnFolderAt returns the folder at rel, a path of the person's home ("/" is
// the home), with its members, when the person may share it.
func (s *Service) OwnFolderAt(ctx context.Context, a workspace.Actor, rel string) (Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return Folder{}, err
	}
	if !isPerson(a) {
		return Folder{}, ErrNotShareable
	}
	abs, err := s.o.Files.Abs(a.Principal, rel)
	if err != nil {
		return Folder{}, ErrNotShareable
	}
	n, err := st.Stat(ctx, a, abs)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
			return Folder{}, ErrNotShareable
		}
		return Folder{}, err
	}
	return s.OwnGrants(ctx, a, n.ID)
}
