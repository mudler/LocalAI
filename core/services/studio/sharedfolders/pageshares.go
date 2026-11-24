// SPDX-License-Identifier: MIT
package sharedfolders

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A person can share one page of their own home: the folder pages/<name>. The
// grant is a grant on that folder, like the grant on any other folder of the
// home, made through the same code (setOwnMember) with two differences:
//
//   - it carries the page mark (workspace.PageGranter), which is what makes
//     the pages service treat the folder as a shared page, and
//   - it may have the mode data, which the folder routes never give.
//
// Only an existing page of the person's home can be shared: one level below
// pages/, with an index.html. pages/ itself, a file, a folder inside a page
// and the page of another person are ErrNotShareable, with the same answer as
// a page that does not exist. The folder routes keep refusing every folder
// under pages/: the page routes are the only way to share one.
//
// Everyone may get view and use but not edit: an edit grant lets the grantee
// change code that runs in other people's browsers, so it goes to named people.

// Errors of page sharing.
var (
	// ErrPageSharesOff means an administrator turned off the pages that
	// people share. A person can still take a share back.
	ErrPageSharesOff = errors.New("shared folders: pages cannot be shared on this server")
	// ErrEveryoneEdit means a grant to everyone has the role edit.
	ErrEveryoneEdit = errors.New("shared folders: a page can be shared with everyone to view or use, not to edit")
)

// RoleMode returns the grant mode that a page role stands for, and false for a
// word that is not a role: view is read, use is data and edit is write.
func RoleMode(role string) (workspace.Mode, bool) {
	switch role {
	case pages.RoleView:
		return workspace.ModeRead, true
	case pages.RoleUse:
		return workspace.ModeData, true
	case pages.RoleEdit:
		return workspace.ModeWrite, true
	}
	return workspace.ModeNone, false
}

// PageSharesOn says whether people may share pages now.
func (s *Service) PageSharesOn() bool { return s.o.PageShares == nil || s.o.PageShares() }

// pageDir returns the folder of the page name in the home of the person a,
// whether or not it still has an index.html: the folder whose grants the
// person sees and revokes.
func (s *Service) pageDir(ctx context.Context, st workspace.Store, a workspace.Actor, name string) (workspace.Node, error) {
	if !isPerson(a) || !pages.ValidName(name) {
		return workspace.Node{}, ErrNotShareable
	}
	abs, err := s.o.Files.Abs(a.Principal, "/"+pages.Dir+"/"+name)
	if err != nil {
		return workspace.Node{}, ErrNotShareable
	}
	n, err := st.Stat(ctx, a, abs)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
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

// pageShareable returns the folder of the page name when the person may share
// it: it is a page (it has an index.html) and the person may write it.
func (s *Service) pageShareable(ctx context.Context, st workspace.Store, a workspace.Actor, name string) (workspace.Node, error) {
	n, err := s.pageDir(ctx, st, a, name)
	if err != nil {
		return workspace.Node{}, err
	}
	ix, err := st.Stat(ctx, a, n.Path+"/"+pages.IndexFile)
	if err != nil || ix.Kind != workspace.KindFile {
		return workspace.Node{}, ErrNotShareable
	}
	v, err := st.View(ctx, a, []workspace.MountRequest{{Path: n.Path, At: "x", Mode: workspace.ModeWrite}})
	if err != nil || len(v.Mounts) != 1 || v.Mounts[0].Mode != workspace.ModeWrite {
		return workspace.Node{}, ErrNotShareable
	}
	return n, nil
}

// PageGrants returns the grants on the page name of the person's home.
func (s *Service) PageGrants(ctx context.Context, a workspace.Actor, name string) (Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.pageDir(ctx, st, a, name)
	if err != nil {
		return Folder{}, err
	}
	return s.describeOwned(ctx, st, a.Principal, n)
}

// PageSetMember gives principal (an account ID or Everyone) the mode read,
// data or write on the page name of the person's home, or changes the mode.
// Lowering a grant drops the consent of the grantee and reports the lost
// access before it returns. It is refused with ErrPageSharesOff while an
// administrator keeps page shares off.
func (s *Service) PageSetMember(ctx context.Context, a workspace.Actor, name, principal string, mode workspace.Mode) (Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.pageShareable(ctx, st, a, name)
	if err != nil {
		return Folder{}, err
	}
	if !s.PageSharesOn() {
		return Folder{}, ErrPageSharesOff
	}
	switch mode {
	case workspace.ModeRead, workspace.ModeData, workspace.ModeWrite:
	default:
		return Folder{}, ErrBadMode
	}
	if mode == workspace.ModeWrite && strings.TrimSpace(principal) == Everyone {
		return Folder{}, ErrEveryoneEdit
	}
	return s.setOwnMember(ctx, st, a, n, principal, mode, true)
}

// PageRemoveMember takes away the grant of principal on the page name of the
// person's home and drops the grantee's consent. A member who has no grant is
// not an error. It works while page shares are off.
func (s *Service) PageRemoveMember(ctx context.Context, a workspace.Actor, name, principal string) (Folder, error) {
	st, err := s.ownerStore()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.pageDir(ctx, st, a, name)
	if err != nil {
		return Folder{}, err
	}
	return s.removeOwnMember(ctx, st, a, n, principal)
}

// PageSearchAccounts is the user picker of the page routes: the picker of the
// folder routes, for any person who may share pages. It is refused with
// ErrPageSharesOff while an administrator keeps page shares off.
func (s *Service) PageSearchAccounts(ctx context.Context, a workspace.Actor, query string, limit int) ([]User, error) {
	if _, err := s.ownerStore(); err != nil {
		return nil, err
	}
	if !isPerson(a) {
		return nil, ErrNotShareable
	}
	if !s.PageSharesOn() {
		return nil, ErrPageSharesOff
	}
	return s.searchAccounts(ctx, a, query, limit)
}

// PageShareCounts returns how many grants each shared page of the person's
// home has, by page name. A page without a grant is not in it.
func (s *Service) PageShareCounts(ctx context.Context, a workspace.Actor) (map[string]int, error) {
	owned, err := s.Owned(ctx, a)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, f := range owned {
		dir, name := path.Split(f.Path)
		if dir == "/"+pages.Dir+"/" && pages.ValidName(name) && len(f.Members) > 0 {
			out[name] = len(f.Members)
		}
	}
	return out, nil
}

// lostPageAccess drops the consent of principal (everyone's, for Everyone) to
// the page in folder n, when n is a page of a home, so that a later grant
// asks again. The grant is already gone or lower: a failure here leaves a
// consent that no grant can use, and is logged.
func (s *Service) lostPageAccess(ctx context.Context, n workspace.Node, principal string) {
	if s.o.Consents == nil {
		return
	}
	owner := homeOwner(n.Path)
	rel, ok := homeRel(owner, n.Path)
	if owner == "" || !ok || path.Dir(rel) != "/"+pages.Dir || !pages.ValidName(path.Base(rel)) {
		return
	}
	now := s.o.Now()
	var err error
	if principal == Everyone {
		_, err = s.o.Consents.RevokePages(ctx, []int64{n.ID}, "revoke_share", now)
	} else {
		err = s.o.Consents.Revoke(ctx, principal, n.ID, pages.ConsentEvent{Principal: principal, PageID: n.ID, Folder: path.Dir(rel),
			Name: path.Base(rel), Action: "revoke_share", At: now})
	}
	if err != nil {
		xlog.Error("shared folders: could not drop the consent to a page that is no longer shared", "page", n.Path, "member", principal, "error", err)
	}
}
