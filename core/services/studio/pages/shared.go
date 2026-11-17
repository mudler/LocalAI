package pages

import (
	"context"
	"errors"
	"path"
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
//
// The other kind of shared page is one page of a person's home that the person
// shared through the page routes. The grant is on the page's own folder
// (/users/<owner>/pages/<name>) and carries the page mark, so the folder is
// itself the page and not a folder of pages. The viewer's mode says what they
// may do: read (view), data (use: the page saves data files) or write (edit).

// SharedFolder is one shared folder of a viewer.
type SharedFolder struct {
	ID       int64  // the folder's node ID: the stable name the API uses
	Path     string // workspace path
	Writable bool   // the viewer has a write grant
	// Mode is the most the viewer may do in the folder: read, data or write.
	Mode workspace.Mode
	// Page is set when the folder is one page that its owner shared through
	// the page routes. Owner is then the principal of the owner.
	Page  bool
	Owner string
	// Off is set on a page that a person shared while the administrator's
	// switch for such pages is off: it is listed and does not open.
	Off bool
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

// pageFolder says whether a shared folder holds shared pages. The pages of a
// folder that a person shared from their own home are not shared pages: a page
// runs in the viewer's browser with the viewer's rights, and only a folder an
// administrator made may put one there. The one exception is a page folder
// that its owner shared through the page routes (the grant carries the page
// mark), which is itself the page.
func pageFolder(f SharedFolder) bool {
	return !workspace.Within(f.Path, session.UsersRoot) || f.Page
}

// userPage returns the principal of the owner when p is the folder of one
// page of a home: /users/<owner>/pages/<name>.
func userPage(p string) (owner string, ok bool) {
	rest, found := strings.CutPrefix(p, session.UsersRoot+"/")
	if !found {
		return "", false
	}
	segs := strings.Split(rest, "/")
	if len(segs) != 3 || segs[0] == "" || segs[1] != Dir || !ValidName(segs[2]) {
		return "", false
	}
	return segs[0], true
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
		f := SharedFolder{ID: n.ID, Path: n.Path, Mode: s.mode(ctx, a, n.Path)}
		f.Writable = f.Mode == workspace.ModeWrite
		if owner, ok := userPage(n.Path); ok {
			pg, ok := s.f.Store().(workspace.PageGranter)
			if !ok {
				continue
			}
			marked, err := pg.PageShared(ctx, a, n.Path)
			if err != nil {
				return nil, err
			}
			f.Page, f.Owner, f.Off = marked, owner, marked && !s.userSharesOn()
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// mode asks the store which mode a view of the folder would get: it clamps
// the mode to the viewer's grant. A folder that cannot be viewed is ModeNone.
func (s *Service) mode(ctx context.Context, a workspace.Actor, p string) workspace.Mode {
	v, err := s.f.Store().View(ctx, a, []workspace.MountRequest{{Path: p, At: "x", Mode: workspace.ModeWrite}})
	if err != nil || len(v.Mounts) != 1 {
		return workspace.ModeNone
	}
	return v.Mounts[0].Mode
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
		if f.ID != folderID || !pageFolder(f) {
			continue
		}
		if f.Page {
			// The folder is the page: its name is the page's name.
			if name != path.Base(f.Path) {
				return loc{}, SharedFolder{}, ErrNotFound
			}
			if f.Off {
				return loc{}, SharedFolder{}, ErrUserSharesOff
			}
			return loc{a: s.actor(principal), folder: f.Path, name: name, key: principal + "/shared/" + f.Path, shared: true}, f, nil
		}
		return loc{a: s.actor(principal), folder: f.Path + "/" + Dir + "/" + name, name: name,
			key: principal + "/shared/" + f.Path + "/" + name, shared: true}, f, nil
	}
	return loc{}, SharedFolder{}, ErrNotFound
}

// The roles a person gives with the page routes, as the page JSON says them.
const (
	RoleView = "view" // read the page and its data
	RoleUse  = "use"  // also save data files through the page
	RoleEdit = "edit" // also change the code
)

// RoleOf returns the role that a mode stands for in a shared page.
func RoleOf(m workspace.Mode) string {
	switch {
	case m >= workspace.ModeWrite:
		return RoleEdit
	case m == workspace.ModeData:
		return RoleUse
	}
	return RoleView
}

func sharedPage(p *Page, f SharedFolder) {
	p.Source, p.Folder, p.FolderID, p.Writable = SourceShared, f.Path, f.ID, f.Writable
	p.Path = f.Path + "/" + Dir + "/" + p.Name
	if f.Page {
		// The folder is the page, and its path would name the owner's
		// account: nothing of it goes out as a path.
		p.UserShared, p.Role, p.OwnerID, p.TurnedOff = true, RoleOf(f.Mode), f.Owner, f.Off
		p.Path = ""
	}
}

// OwnShareState returns the page name of the owner's home as a shared page
// would be judged: its node ID and the hash of its code, which a consent is
// kept against. It reads the page with the owner's rights and does not depend
// on who else may write it.
func (s *Service) OwnShareState(ctx context.Context, owner, name string) (Page, error) {
	if !ValidName(name) {
		return Page{}, ErrBadName
	}
	folder, err := s.f.Abs(owner, "/"+Dir+"/"+name)
	if err != nil {
		return Page{}, err
	}
	return s.getAt(ctx, loc{a: s.actor(owner), folder: folder, name: name, shared: true})
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
		if !pageFolder(f) {
			continue
		}
		if f.Page {
			l := loc{a: a, folder: f.Path, name: path.Base(f.Path), key: principal + "/shared/" + f.Path, shared: true}
			if f.Off {
				// Listed, not opened: its code is not read.
				p := Page{Name: l.name, Title: l.name, Source: SourceShared}
				sharedPage(&p, f)
				out = append(out, p)
				continue
			}
			p, err := s.getAt(ctx, l)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			sharedPage(&p, f)
			out = append(out, p)
			continue
		}
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
// rights: a viewer with a read grant gets ErrFolderReadOnly. The page writes
// data files only, whatever the viewer's grant: a file that the page's code
// names (a script, a style sheet, a module) is code too and is refused, so a
// page cannot change what the viewers allowed to run.
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
