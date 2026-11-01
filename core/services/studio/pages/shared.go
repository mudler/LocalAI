package pages

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A shared folder is a folder outside the viewer's home that an administrator
// granted the viewer, read-only or read-write. Its pages are the folders
// pages/<name>/ with an index.html, as in a home. The viewer reaches them only
// through a grant of their own: the store checks every call with the viewer's
// actor, so a page in a shared tree can read, write and list nothing that the
// viewer could not, and a viewer without a grant gets ErrNotFound.

// SharedFolder is one shared folder of a viewer.
type SharedFolder struct {
	ID       int64  // the folder's node ID: the stable name the API uses
	Path     string // workspace path
	Writable bool   // the viewer has a write grant
}

// skipShared tells whether a readable folder is not a shared folder of pages:
// the viewer's own home (or a folder that holds it), the folders of skills that
// every user reads, and the root of all homes.
func skipShared(p, home string) bool {
	switch {
	case p == "/" || p == session.UsersRoot:
		return true
	case p == home || strings.HasPrefix(p, home+"/") || strings.HasPrefix(home, p+"/"):
		return true
	case p == files.SharedSkillsPath || strings.HasPrefix(p, files.SharedSkillsPath+"/"):
		return true
	case p == files.BuiltinSkillsPath || strings.HasPrefix(p, files.BuiltinSkillsPath+"/"):
		return true
	case p == files.AddonSkillsPath || strings.HasPrefix(p, files.AddonSkillsPath+"/"):
		return true
	}
	return false
}

// SharedFolders returns the shared folders of the viewer, by path.
func (s *Service) SharedFolders(ctx context.Context, principal string) ([]SharedFolder, error) {
	if !validPrincipal(principal) {
		return nil, ErrNotFound
	}
	a := s.actor(principal)
	prefixes, err := s.f.Store().ReadablePrefixes(ctx, a)
	if err != nil {
		return nil, err
	}
	home := session.HomePath(principal)
	out := []SharedFolder{}
	for _, p := range prefixes {
		if skipShared(p, home) {
			continue
		}
		n, err := s.f.Store().Stat(ctx, a, p)
		if err != nil || n.Kind != workspace.KindDir {
			continue
		}
		out = append(out, SharedFolder{ID: n.ID, Path: n.Path, Writable: s.writable(ctx, a, n.Path)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// writable asks the store which mode a view of the folder would get: it
// clamps the mode to the viewer's grant.
func (s *Service) writable(ctx context.Context, a workspace.Actor, p string) bool {
	v, err := s.f.Store().View(ctx, a, []workspace.MountRequest{{Path: p, At: "x", Mode: workspace.ModeWrite}})
	return err == nil && len(v.Mounts) == 1 && v.Mounts[0].Mode == workspace.ModeWrite
}

func validPrincipal(p string) bool {
	return p != "" && p != workspace.StudioAdmin && p != files.EveryoneGroup && !strings.ContainsAny(p, "/\\\x00") && p != "." && p != ".."
}

// sharedLoc finds the shared folder folderID of the viewer and the page name
// in it. A folder the viewer has no grant on is ErrNotFound, like a missing
// one.
func (s *Service) sharedLoc(ctx context.Context, principal string, folderID int64, name string) (loc, SharedFolder, error) {
	if !ValidName(name) {
		return loc{}, SharedFolder{}, ErrBadName
	}
	folders, err := s.SharedFolders(ctx, principal)
	if err != nil {
		return loc{}, SharedFolder{}, err
	}
	for _, f := range folders {
		if f.ID == folderID {
			return loc{a: s.actor(principal), folder: f.Path + "/" + Dir + "/" + name, name: name,
				key: principal + "/shared/" + f.Path + "/" + name, shared: true}, f, nil
		}
	}
	return loc{}, SharedFolder{}, ErrNotFound
}

func sharedPage(p *Page, f SharedFolder) {
	p.Source, p.Folder, p.FolderID, p.Writable = SourceShared, f.Path, f.ID, f.Writable
	p.Path = f.Path + "/" + Dir + "/" + p.Name
}

// ListShared returns the pages of every shared folder of the viewer, sorted
// by folder and name.
func (s *Service) ListShared(ctx context.Context, principal string) ([]Page, error) {
	folders, err := s.SharedFolders(ctx, principal)
	if err != nil {
		return nil, err
	}
	a := s.actor(principal)
	out := []Page{}
	for _, f := range folders {
		list, err := s.listIn(ctx, a, f.Path+"/"+Dir, func(name string) (loc, error) {
			return loc{a: a, folder: f.Path + "/" + Dir + "/" + name, name: name, shared: true}, nil
		}, func(p *Page) { sharedPage(p, f) })
		if err != nil && !errors.Is(err, workspace.ErrNotFound) {
			return nil, err
		}
		out = append(out, list...)
		if len(out) >= MaxPages {
			return out[:MaxPages], nil
		}
	}
	return out, nil
}

// GetShared returns one shared page.
func (s *Service) GetShared(ctx context.Context, principal string, folderID int64, name string) (Page, error) {
	l, f, err := s.sharedLoc(ctx, principal, folderID, name)
	if err != nil {
		return Page{}, err
	}
	p, err := s.getAt(ctx, l)
	if err != nil {
		return Page{}, err
	}
	sharedPage(&p, f)
	return p, nil
}

// SharedSource returns a shared page with the text of its index.html.
func (s *Service) SharedSource(ctx context.Context, principal string, folderID int64, name string) (Page, string, error) {
	p, err := s.GetShared(ctx, principal, folderID, name)
	if err != nil {
		return Page{}, "", err
	}
	c, err := s.ReadShared(ctx, principal, folderID, name, IndexFile)
	if err != nil {
		return Page{}, "", err
	}
	return p, c.Content, nil
}

// ReadShared reads a text file of a shared page's folder.
func (s *Service) ReadShared(ctx context.Context, principal string, folderID int64, name, rel string) (Content, error) {
	l, _, err := s.sharedLoc(ctx, principal, folderID, name)
	if err != nil {
		return Content{}, err
	}
	return s.readAt(ctx, l, rel)
}

// ReadSharedImage returns an image file of a shared page's folder, with the
// viewer's rights, as ReadImage does for an own page.
func (s *Service) ReadSharedImage(ctx context.Context, principal string, folderID int64, name, rel string) (Image, error) {
	l, _, err := s.sharedLoc(ctx, principal, folderID, name)
	if err != nil {
		return Image{}, err
	}
	return s.readImageAt(ctx, l, rel)
}

// WriteShared writes a file of a shared page's folder, with the viewer's
// rights: a viewer with a read grant gets ErrFolderReadOnly.
func (s *Service) WriteShared(ctx context.Context, principal string, folderID int64, name, rel string, body []byte, base int64) (File, error) {
	l, _, err := s.sharedLoc(ctx, principal, folderID, name)
	if err != nil {
		return File{}, err
	}
	return s.writeAt(ctx, l, rel, body, base)
}

// ListSharedFiles lists a folder of a shared page.
func (s *Service) ListSharedFiles(ctx context.Context, principal string, folderID int64, name, dir string) ([]File, error) {
	l, _, err := s.sharedLoc(ctx, principal, folderID, name)
	if err != nil {
		return nil, err
	}
	return s.listAt(ctx, l, dir)
}

// AllowSharedSend says whether a shared page may send another message now.
func (s *Service) AllowSharedSend(principal string, folderID int64, name string) bool {
	return s.sends.allow(principal + "/shared/" + strconv.FormatInt(folderID, 10) + "/" + name)
}
