// Package sharedfolders lets an administrator create the folders that users
// share, and choose who may read or edit each one.
//
// A shared folder is a workspace folder /shared/<name>. Access to it is a
// workspace grant on the folder, to a user or to files.EveryoneGroup, with the
// mode read or write; the store checks every call against those grants, so a
// user without a grant gets ErrNotFound and cannot learn that the folder
// exists. A chat of a member mounts the folder at /workspace/shared-<name>, and
// the folder's pages/<name>/ folders are shared pages.
//
// Removing a folder sets the store's tombstone, as for any file. Restore undoes
// that change set and brings back the folder with its grants. Removing it also
// drops every viewer's consent to its pages, so a restored page asks again.
// Every change is a line in the audit.
package sharedfolders

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Root is the workspace folder that holds the shared folders.
const Root = "/shared"

// Everyone is the member that stands for every user.
const Everyone = files.EveryoneGroup

// ActorAssistant is the audit actor of a change that the LocalAI Assistant
// made: its tools run with no user of their own.
const ActorAssistant = "@assistant"

// Limits.
const (
	// MaxFolders is the most shared folders a server keeps.
	MaxFolders = 100
	// MaxMembers is the most members of one folder.
	MaxMembers = 200
	// MaxNameRunes is the longest folder name, in characters.
	MaxNameRunes = 64
)

// Errors. Test them with errors.Is.
var (
	ErrNotFound    = errors.New("shared folders: not found")
	ErrExists      = errors.New("shared folders: a shared folder with this name exists")
	ErrBadName     = errors.New("shared folders: a folder name is 1 to 64 letters, digits, spaces, dots, dashes or underscores, and starts with a letter or a digit")
	ErrLimit       = errors.New("shared folders: limit reached")
	ErrUnknownUser = errors.New("shared folders: no such user")
	ErrBadMode     = errors.New("shared folders: the mode is read or write")
	ErrBadRestore  = errors.New("shared folders: that change is not the removal of a shared folder")
	ErrConflict    = errors.New("shared folders: the folder cannot be restored; a folder with its name exists")
	ErrUnavailable = errors.New("shared folders: files are off on this server")
)

var nameRE = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]*$`)

// CleanName trims a folder name and checks the name rules.
func CleanName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) > MaxNameRunes || !nameRE.MatchString(n) || strings.HasSuffix(n, ".") || strings.Contains(n, "  ") {
		return "", ErrBadName
	}
	return n, nil
}

// Member is one grant on a shared folder.
type Member struct {
	Principal string // a user ID or Everyone
	Name      string // the user's name, or the principal when it is not known
	Mode      workspace.Mode
}

// Folder is one shared folder.
type Folder struct {
	ID      int64 // the folder's node ID; stable across a rename
	Name    string
	Path    string
	Pages   int
	Members []Member       // the administrator's view only
	Mode    workspace.Mode // the viewer's view only: read or write
}

// Event is one line of the audit.
type Event struct {
	Actor     string // who made the change: a user ID or ActorAssistant
	Action    string // one of the Action* constants
	FolderID  int64
	Folder    string // the folder's path at the time
	Principal string // the member, for a grant or a revoke
	Mode      workspace.Mode
	Rev       int64 // the change set of a removal or a restore
	At        time.Time
}

// Audit actions.
const (
	ActionCreate  = "create"
	ActionRename  = "rename"
	ActionDelete  = "delete"
	ActionRestore = "restore"
	ActionGrant   = "grant"
	ActionRevoke  = "revoke"
)

// consentAction is the action of the consent audit lines that a removal writes.
const consentAction = "revoke_folder_removed"

// User is a user that a member names.
type User struct {
	ID   string
	Name string
}

// Users finds users. Nil: there is no user database, and any principal that
// can name a home is a member.
type Users interface {
	// Find returns the user that ref names: an ID, an email or a name. A ref
	// that names no user is ErrUnknownUser.
	Find(ctx context.Context, ref string) (User, error)
}

// Options builds a Service.
type Options struct {
	Files    *files.Service
	Consents pages.ConsentStore // nil: no consent is kept, so none is dropped
	Audit    AuditStore         // nil: an audit in memory
	Users    Users
	Now      func() time.Time
}

// Service manages the shared folders.
type Service struct {
	o     Options
	pages *pages.Service
	admin workspace.Actor
}

// New returns a Service.
func New(o Options) *Service {
	if o.Audit == nil {
		o.Audit, _ = NewJSONAudit("")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, pages: pages.New(o.Files), admin: workspace.Actor{Principal: files.AdminPrincipal}}
}

func (s *Service) store() (workspace.Store, error) {
	if s == nil || s.o.Files == nil || !s.o.Files.Available() {
		return nil, ErrUnavailable
	}
	return s.o.Files.Store(), nil
}

// List returns every shared folder with its members, by name.
func (s *Service) List(ctx context.Context) ([]Folder, error) {
	st, err := s.store()
	if err != nil {
		return nil, err
	}
	nodes, err := st.List(ctx, s.admin, Root)
	if errors.Is(err, workspace.ErrNotFound) {
		return []Folder{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Folder, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != workspace.KindDir {
			continue
		}
		f, err := s.describe(ctx, st, n)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// Get returns one shared folder with its members.
func (s *Service) Get(ctx context.Context, id int64) (Folder, error) {
	st, err := s.store()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.find(ctx, st, id)
	if err != nil {
		return Folder{}, err
	}
	return s.describe(ctx, st, n)
}

// find returns the live folder id under Root.
func (s *Service) find(ctx context.Context, st workspace.Store, id int64) (workspace.Node, error) {
	if id <= 0 {
		return workspace.Node{}, ErrNotFound
	}
	nodes, err := st.List(ctx, s.admin, Root)
	if errors.Is(err, workspace.ErrNotFound) {
		return workspace.Node{}, ErrNotFound
	}
	if err != nil {
		return workspace.Node{}, err
	}
	for _, n := range nodes {
		if n.ID == id && n.Kind == workspace.KindDir {
			return n, nil
		}
	}
	return workspace.Node{}, ErrNotFound
}

func (s *Service) describe(ctx context.Context, st workspace.Store, n workspace.Node) (Folder, error) {
	f := Folder{ID: n.ID, Name: n.Name, Path: n.Path, Members: []Member{}}
	gl, ok := st.(workspace.GrantLister)
	if !ok {
		return Folder{}, errors.New("shared folders: the workspace store cannot list grants")
	}
	grants, err := gl.Grants(ctx, s.admin, n.Path)
	if err != nil {
		return Folder{}, err
	}
	for _, g := range grants {
		f.Members = append(f.Members, Member{Principal: g.Principal, Name: s.userName(ctx, g.Principal), Mode: g.Mode})
	}
	// Everyone first, then by name.
	slices.SortStableFunc(f.Members, func(a, b Member) int {
		if (a.Principal == Everyone) != (b.Principal == Everyone) {
			if a.Principal == Everyone {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	f.Pages, err = s.countPages(ctx, st, n.Path)
	return f, err
}

func (s *Service) userName(ctx context.Context, principal string) string {
	if principal == Everyone || s.o.Users == nil {
		return principal
	}
	if u, err := s.o.Users.Find(ctx, principal); err == nil && u.ID == principal && u.Name != "" {
		return u.Name
	}
	return principal
}

// countPages counts the folders pages/<name>/ with an index.html.
func (s *Service) countPages(ctx context.Context, st workspace.Store, folder string) (int, error) {
	ids, err := s.pageIDs(ctx, st, folder)
	return len(ids), err
}

// pageIDs returns the node IDs of the pages of a folder.
func (s *Service) pageIDs(ctx context.Context, st workspace.Store, folder string) ([]int64, error) {
	dir := folder + "/" + pages.Dir
	nodes, err := st.List(ctx, s.admin, dir)
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, n := range nodes {
		if n.Kind != workspace.KindDir || !pages.ValidName(n.Name) {
			continue
		}
		if ix, err := st.Stat(ctx, s.admin, dir+"/"+n.Name+"/"+pages.IndexFile); err == nil && ix.Kind == workspace.KindFile {
			ids = append(ids, n.ID)
		}
	}
	return ids, nil
}

// Create makes the shared folder name. by is the user who asks: they become
// a member who can edit, so they can fill the folder. An empty by (the
// assistant) adds no member.
func (s *Service) Create(ctx context.Context, by, name string) (Folder, error) {
	st, err := s.store()
	if err != nil {
		return Folder{}, err
	}
	name, err = CleanName(name)
	if err != nil {
		return Folder{}, err
	}
	list, err := st.List(ctx, s.admin, Root)
	if err != nil && !errors.Is(err, workspace.ErrNotFound) {
		return Folder{}, err
	}
	live := 0
	for _, n := range list {
		if n.Kind != workspace.KindDir {
			continue
		}
		live++
		if workspace.FoldName(n.Name) == workspace.FoldName(name) {
			return Folder{}, ErrExists
		}
	}
	if live >= MaxFolders {
		return Folder{}, fmt.Errorf("%w: a server keeps at most %d shared folders", ErrLimit, MaxFolders)
	}
	p := Root + "/" + name
	tx, err := st.Begin(ctx, s.admin, "Create the shared folder "+name)
	if err != nil {
		return Folder{}, err
	}
	n, err := tx.Mkdir(ctx, p)
	if err == nil {
		_, err = tx.Mkdir(ctx, p+"/"+pages.Dir)
	}
	if err != nil {
		_ = tx.Rollback()
		if errors.Is(err, workspace.ErrExists) {
			return Folder{}, ErrExists
		}
		return Folder{}, err
	}
	if _, err := tx.Commit(); err != nil {
		return Folder{}, err
	}
	s.audit(ctx, Event{Actor: actorOf(by), Action: ActionCreate, FolderID: n.ID, Folder: p})
	if by != "" && validMember(by) {
		if err := st.Grant(ctx, s.admin, by, p, workspace.ModeWrite); err != nil {
			return Folder{}, err
		}
		s.audit(ctx, Event{Actor: actorOf(by), Action: ActionGrant, FolderID: n.ID, Folder: p, Principal: by, Mode: workspace.ModeWrite})
	}
	return s.describe(ctx, st, n)
}

// Rename gives the folder id a new name. Its grants, pages and the consents to
// them stay: they follow the folder, not its path.
func (s *Service) Rename(ctx context.Context, by string, id int64, name string) (Folder, error) {
	st, err := s.store()
	if err != nil {
		return Folder{}, err
	}
	name, err = CleanName(name)
	if err != nil {
		return Folder{}, err
	}
	n, err := s.find(ctx, st, id)
	if err != nil {
		return Folder{}, err
	}
	if n.Name == name {
		return s.describe(ctx, st, n)
	}
	moved, err := st.Move(ctx, s.admin, n.Path, Root+"/"+name)
	if err != nil {
		if errors.Is(err, workspace.ErrExists) {
			return Folder{}, ErrExists
		}
		return Folder{}, err
	}
	s.audit(ctx, Event{Actor: actorOf(by), Action: ActionRename, FolderID: n.ID, Folder: moved.Path})
	return s.describe(ctx, st, moved)
}

// Delete removes the folder id: it sets the tombstone on the folder and
// everything in it, as one change set whose revision it returns for Restore.
// It drops every viewer's consent to the folder's pages.
func (s *Service) Delete(ctx context.Context, by string, id int64) (Folder, int64, error) {
	st, err := s.store()
	if err != nil {
		return Folder{}, 0, err
	}
	n, err := s.find(ctx, st, id)
	if err != nil {
		return Folder{}, 0, err
	}
	f, err := s.describe(ctx, st, n)
	if err != nil {
		return Folder{}, 0, err
	}
	ids, err := s.pageIDs(ctx, st, n.Path)
	if err != nil {
		return Folder{}, 0, err
	}
	tx, err := st.Begin(ctx, s.admin, "Remove the shared folder "+n.Name)
	if err != nil {
		return Folder{}, 0, err
	}
	if err := tx.Delete(ctx, n.Path); err != nil {
		_ = tx.Rollback()
		return Folder{}, 0, err
	}
	cs, err := tx.Commit()
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return Folder{}, 0, err
	}
	if s.o.Consents != nil {
		// The folder is gone either way; a consent left behind would let a
		// restored page run without asking again, so say so loudly.
		if dropped, err := s.o.Consents.RevokePages(ctx, ids, consentAction, s.o.Now()); err != nil {
			xlog.Error("shared folders: could not drop the consents to the pages of a removed folder", "folder", n.Path, "error", err)
		} else if dropped > 0 {
			xlog.Info("shared folders: dropped the consents to the pages of a removed folder", "folder", n.Path, "consents", dropped)
		}
	}
	s.audit(ctx, Event{Actor: actorOf(by), Action: ActionDelete, FolderID: n.ID, Folder: n.Path, Rev: cs.Rev})
	return f, cs.Rev, nil
}

// Restore undoes the removal of a shared folder, change set rev, and returns
// the folder with its members.
func (s *Service) Restore(ctx context.Context, by string, rev int64) (Folder, error) {
	st, err := s.store()
	if err != nil {
		return Folder{}, err
	}
	if rev <= 0 {
		return Folder{}, ErrBadRestore
	}
	sets, err := st.History(ctx, s.admin, workspace.HistoryQuery{Path: Root, BeforeRev: rev + 1, Limit: 1})
	if err != nil {
		return Folder{}, err
	}
	if len(sets) != 1 || sets[0].Rev != rev {
		return Folder{}, ErrBadRestore
	}
	top := ""
	for _, op := range sets[0].Ops {
		p := op.OldPath
		if op.Kind != workspace.OpDelete || !strings.HasPrefix(p, Root+"/") {
			return Folder{}, ErrBadRestore
		}
		if path.Dir(p) == Root {
			top = p
		}
	}
	if top == "" {
		return Folder{}, ErrBadRestore
	}
	if _, _, err := st.Undo(ctx, s.admin, rev, workspace.UndoOptions{}); err != nil {
		if errors.Is(err, workspace.ErrConflict) {
			return Folder{}, ErrConflict
		}
		return Folder{}, err
	}
	n, err := st.Stat(ctx, s.admin, top)
	if err != nil {
		return Folder{}, err
	}
	s.audit(ctx, Event{Actor: actorOf(by), Action: ActionRestore, FolderID: n.ID, Folder: n.Path, Rev: rev})
	return s.describe(ctx, st, n)
}

// SetMember gives a user (an ID, an email or a name) or Everyone the mode
// read or write on the folder id, or changes the mode they have.
func (s *Service) SetMember(ctx context.Context, by string, id int64, member string, mode workspace.Mode) (Folder, error) {
	st, err := s.store()
	if err != nil {
		return Folder{}, err
	}
	if mode != workspace.ModeRead && mode != workspace.ModeWrite {
		return Folder{}, ErrBadMode
	}
	n, err := s.find(ctx, st, id)
	if err != nil {
		return Folder{}, err
	}
	principal, err := s.resolve(ctx, member)
	if err != nil {
		return Folder{}, err
	}
	f, err := s.describe(ctx, st, n)
	if err != nil {
		return Folder{}, err
	}
	if !slices.ContainsFunc(f.Members, func(m Member) bool { return m.Principal == principal }) && len(f.Members) >= MaxMembers {
		return Folder{}, fmt.Errorf("%w: a shared folder has at most %d members", ErrLimit, MaxMembers)
	}
	if err := st.Grant(ctx, s.admin, principal, n.Path, mode); err != nil {
		return Folder{}, err
	}
	s.audit(ctx, Event{Actor: actorOf(by), Action: ActionGrant, FolderID: n.ID, Folder: n.Path, Principal: principal, Mode: mode})
	return s.describe(ctx, st, n)
}

// RemoveMember takes away the grant of a member on the folder id. A member
// who has no grant is not an error. The chats of the member stop seeing the
// folder the next time they open.
func (s *Service) RemoveMember(ctx context.Context, by string, id int64, member string) (Folder, error) {
	st, err := s.store()
	if err != nil {
		return Folder{}, err
	}
	n, err := s.find(ctx, st, id)
	if err != nil {
		return Folder{}, err
	}
	principal := strings.TrimSpace(member)
	if principal != Everyone {
		// A member that is listed is removed by its principal even when the
		// user is gone from the user database.
		f, err := s.describe(ctx, st, n)
		if err != nil {
			return Folder{}, err
		}
		if !slices.ContainsFunc(f.Members, func(m Member) bool { return m.Principal == principal }) {
			if principal, err = s.resolve(ctx, member); err != nil {
				return Folder{}, err
			}
		}
	}
	if err := st.Revoke(ctx, s.admin, principal, n.Path); err != nil {
		return Folder{}, err
	}
	s.audit(ctx, Event{Actor: actorOf(by), Action: ActionRevoke, FolderID: n.ID, Folder: n.Path, Principal: principal})
	return s.describe(ctx, st, n)
}

// resolve turns a member reference into a principal.
func (s *Service) resolve(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == Everyone {
		return Everyone, nil
	}
	if !validMember(ref) {
		return "", ErrUnknownUser
	}
	if s.o.Users == nil {
		return ref, nil
	}
	u, err := s.o.Users.Find(ctx, ref)
	if err != nil {
		return "", err
	}
	if !validMember(u.ID) {
		return "", ErrUnknownUser
	}
	return u.ID, nil
}

// validMember reports whether p can be the principal of a user.
func validMember(p string) bool {
	return p != "" && p != files.AdminPrincipal && p != Everyone && !strings.HasPrefix(p, "@") && p != "." && p != ".." &&
		len(p) <= 200 && !strings.ContainsAny(p, "/\\\x00")
}

// ForUser returns the shared folders of a user with the mode the user has, by
// path. A folder appears only with a grant to the user or to Everyone.
func (s *Service) ForUser(ctx context.Context, principal string) ([]Folder, error) {
	if _, err := s.store(); err != nil {
		return nil, err
	}
	shared, err := s.pages.SharedFolders(ctx, principal)
	if errors.Is(err, pages.ErrNotFound) {
		return []Folder{}, nil
	}
	if err != nil {
		return nil, err
	}
	list, err := s.pages.ListShared(ctx, principal)
	if err != nil {
		return nil, err
	}
	count := map[int64]int{}
	for _, p := range list {
		count[p.FolderID]++
	}
	out := make([]Folder, 0, len(shared))
	for _, f := range shared {
		mode := workspace.ModeRead
		if f.Writable {
			mode = workspace.ModeWrite
		}
		out = append(out, Folder{ID: f.ID, Name: path.Base(f.Path), Path: f.Path, Pages: count[f.ID], Mode: mode})
	}
	return out, nil
}

// GetForUser returns one shared folder of a user. A folder that the user has
// no grant for is ErrNotFound, like one that does not exist.
func (s *Service) GetForUser(ctx context.Context, principal string, id int64) (Folder, error) {
	list, err := s.ForUser(ctx, principal)
	if err != nil {
		return Folder{}, err
	}
	for _, f := range list {
		if f.ID == id {
			return f, nil
		}
	}
	return Folder{}, ErrNotFound
}

// Roots returns the shared folders of a chat's actor for its view: each one
// read-write when the actor may write it, else read-only.
func (s *Service) Roots(ctx context.Context, a workspace.Actor) ([]session.SharedRoot, error) {
	if _, err := s.store(); err != nil {
		return nil, nil
	}
	shared, err := s.pages.SharedFolders(ctx, a.Principal)
	if errors.Is(err, pages.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]session.SharedRoot, 0, len(shared))
	for _, f := range shared {
		mode := workspace.ModeRead
		if f.Writable {
			mode = workspace.ModeWrite
		}
		out = append(out, session.SharedRoot{Path: f.Path, Mode: mode})
	}
	return out, nil
}

// Events returns the newest lines of the audit, newest first.
func (s *Service) Events(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > maxEventsPage {
		limit = maxEventsPage
	}
	return s.o.Audit.Events(ctx, limit)
}

func (s *Service) audit(ctx context.Context, ev Event) {
	ev.At = s.o.Now().UTC()
	if err := s.o.Audit.Add(ctx, ev); err != nil {
		xlog.Error("shared folders: could not write the audit", "action", ev.Action, "folder", ev.Folder, "error", err)
	}
	xlog.Info("shared folders: changed", "by", ev.Actor, "action", ev.Action, "folder", ev.Folder, "member", ev.Principal, "mode", int(ev.Mode))
}

func actorOf(by string) string {
	if by == "" {
		return ActorAssistant
	}
	return by
}
