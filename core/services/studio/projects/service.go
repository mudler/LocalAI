package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ErrForbidden means the user may not use projects.
var ErrForbidden = errors.New("projects: not allowed")

const (
	// manifestMax is the most bytes of a manifest the service reads or writes.
	manifestMax = 1 << 20
	// errGone is the message of a project whose folder is not in Projects/.
	errGone = "the project folder is missing"
	// errOutside is the message of a project whose folder was moved out of
	// Projects/. The folder is not followed there.
	errOutside = "the project folder is outside Projects/"
	// maxCreateTries bounds the retries when a concurrent request takes the
	// slug between the check and the create.
	maxCreateTries = 5
	memoryIndex    = "# Memory\n"
)

// FileHost is what the service needs from the files service. Paths named rel
// are inside the home of the principal ("/" is the home).
type FileHost interface {
	EnsureHome(ctx context.Context, principal string) error
	Abs(principal, rel string) (string, error)
	Rel(principal, abs string) string
	ReadHome(ctx context.Context, principal, rel string, max int64) (text string, ok bool, err error)
	WriteHome(ctx context.Context, principal, rel, content, title string, max int64) (int64, error)
	DeleteHome(ctx context.Context, principal, rel string) error
	Actor(principal string) workspace.Actor
	Store() workspace.Store
	Written(ctx context.Context, n workspace.Node, principal string)
	// OthersWrite returns the folders of the home of principal that another
	// person may write.
	OthersWrite(ctx context.Context, principal string) (workspace.Writable, error)
}

// Options configure a Service.
type Options struct {
	Store Store
	Files FileHost
	// Permitted says whether a user may use projects. Nil permits everyone. It
	// is asked on every call.
	Permitted func(ctx context.Context, user string) bool
	// OwnerName returns the display name of a user, for the projects that the
	// user shared with someone else. It must return a display name, never an
	// email address or an account ID: the name is shown to other people. Nil, or an empty answer, or the account
	// ID itself gives a neutral label: an account ID is never shown to a
	// viewer.
	OwnerName func(ctx context.Context, user string) string
}

// CreateInput is the content of a new project.
type CreateInput struct {
	Name, Description string
	Defaults          Defaults
	Attached          Defaults
}

// UpdateInput changes a project. A nil field stays as it is.
type UpdateInput struct {
	Name, Description *string
	Defaults          *Defaults
	Attached          *Defaults
}

// Resolved is a project with its manifest and where its folders are now.
type Resolved struct {
	Project
	Manifest Manifest
	// FilesPath and MemoryPath are paths inside the owner's home.
	FilesPath, MemoryPath string
	// AttachedBlocked is true when the manifest holds an attached block that
	// the host ignores because another person may write the project folder: a
	// file that someone else can edit must not give tools to the owner's chats.
	// Manifest.Attached is empty then.
	AttachedBlocked bool
}

// Service creates, finds and changes projects. It keeps nothing in memory: the
// index Store and the workspace are read on each call, so every frontend of a
// cluster gives the same answer.
type Service struct {
	o Options
	// locks has one mutex per project, so two rewrites in this process queue
	// instead of racing. Another process is met by the content hash check.
	locks sync.Map
	// gen counts the changes this process made to a project (create, save,
	// delete, reindex), so a reader that keeps a project for a short time can
	// tell that its copy is older than a write of this process. A write by
	// another frontend is met by the reader's own time limit.
	gen atomic.Uint64
}

// Generation returns a number that grows on every project change made through
// this service.
func (s *Service) Generation() uint64 { return s.gen.Load() }

// NewService returns a Service.
func NewService(o Options) *Service {
	if o.Permitted == nil {
		o.Permitted = func(context.Context, string) bool { return true }
	}
	return &Service{o: o}
}

func (s *Service) permit(ctx context.Context, owner string) error {
	if !s.o.Permitted(ctx, owner) {
		return fmt.Errorf("%w: projects are not available to this account", ErrForbidden)
	}
	return nil
}

func (s *Service) rootRel() string { return "/" + RootDir }

// Create makes the folder of a new project with its manifest, in one change
// set that one undo removes.
func (s *Service) Create(ctx context.Context, owner string, in CreateInput) (Project, error) {
	defer s.gen.Add(1)
	if err := s.permit(ctx, owner); err != nil {
		return Project{}, err
	}
	m := Manifest{Version: ManifestVersion, ID: NewID(), Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description), Defaults: in.Defaults, Attached: in.Attached}
	if _, err := ParseManifest(m.Marshal()); err != nil {
		return Project{}, err
	}
	if err := CheckAttached(m.Defaults, m.Attached); err != nil {
		return Project{}, err
	}
	if err := s.o.Files.EnsureHome(ctx, owner); err != nil {
		return Project{}, err
	}
	st := s.o.Files.Store()
	actor := s.o.Files.Actor(owner)
	for try := 0; try < maxCreateTries; try++ {
		rows, err := s.o.Store.List(ctx, owner)
		if err != nil {
			return Project{}, err
		}
		var statErr error
		slug := Slugify(m.Name, func(c string) bool {
			for _, r := range rows {
				if strings.EqualFold(r.Slug, c) {
					return true
				}
			}
			abs, err := s.o.Files.Abs(owner, s.rootRel()+"/"+c)
			if err != nil {
				statErr = err
				return true
			}
			if _, err := st.Stat(ctx, actor, abs); err != nil {
				if !errors.Is(err, workspace.ErrNotFound) {
					statErr = err
					return true
				}
				return false
			}
			return true
		})
		if statErr != nil {
			return Project{}, statErr
		}
		p, rev, err := s.createFolder(ctx, owner, slug, m)
		if errors.Is(err, workspace.ErrExists) {
			continue // another request took the slug; choose again
		}
		if err != nil {
			return Project{}, err
		}
		if err := s.o.Store.Put(ctx, p); err != nil {
			// The folder exists but the index refused it: take the folder
			// back so the two agree.
			_, _, _ = st.Undo(ctx, actor, rev, workspace.UndoOptions{SkipConflicts: true})
			if errors.Is(err, ErrNameTaken) {
				continue // a concurrent request took the slug in the index
			}
			return Project{}, err
		}
		return p, nil
	}
	return Project{}, fmt.Errorf("%w: could not find a free folder name", ErrNameTaken)
}

func (s *Service) createFolder(ctx context.Context, owner, slug string, m Manifest) (Project, int64, error) {
	title := "Create the project " + m.Name
	root, err := s.o.Files.Abs(owner, s.rootRel()+"/"+slug)
	if err != nil {
		return Project{}, 0, err
	}
	tx, err := s.o.Files.Store().Begin(ctx, s.o.Files.Actor(owner), title)
	if err != nil {
		return Project{}, 0, err
	}
	var written []workspace.Node
	put := func(p, content string) error {
		n, err := tx.Put(ctx, p, strings.NewReader(content), workspace.PutOptions{MaxSize: manifestMax, Title: title})
		written = append(written, n)
		return err
	}
	n, err := tx.Mkdir(ctx, root)
	if err == nil {
		_, err = tx.Mkdir(ctx, root+"/files")
	}
	if err == nil {
		_, err = tx.Mkdir(ctx, root+"/memory")
	}
	if err == nil {
		err = put(root+"/memory/index.md", memoryIndex)
	}
	if err == nil {
		err = put(root+"/"+ManifestFile, string(m.Marshal()))
	}
	if err != nil {
		_ = tx.Rollback()
		return Project{}, 0, err
	}
	cs, err := tx.Commit()
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return Project{}, 0, err
	}
	for _, w := range written {
		w.Rev = cs.Rev
		s.o.Files.Written(ctx, w, owner)
	}
	return Project{ID: m.ID, Owner: owner, Slug: slug, Name: m.Name, Node: n.ID, Status: StatusActive, Updated: time.Now()}, cs.Rev, nil
}

// Get returns a project with its manifest and folders. A project whose folder
// or manifest is broken comes back with ErrInvalid, and the Project in the
// result says why.
func (s *Service) Get(ctx context.Context, owner, id string) (Resolved, error) {
	if err := s.permit(ctx, owner); err != nil {
		return Resolved{}, err
	}
	r, ok, err := s.resolve(ctx, owner, id)
	if err != nil {
		return Resolved{}, err
	}
	if !ok {
		// The folder moved, was edited or was restored: look again.
		if err := s.reindexOnce(ctx, owner); err != nil {
			return Resolved{}, err
		}
		if r, ok, err = s.resolve(ctx, owner, id); err != nil {
			return Resolved{}, err
		}
	}
	if !ok {
		p, perr := s.o.Store.Get(ctx, owner, id)
		if perr != nil {
			return Resolved{}, perr
		}
		return Resolved{Project: p}, fmt.Errorf("%w: %s", ErrInvalid, p.Error)
	}
	return r, nil
}

// resolve reads the row and checks it against the workspace. ok is false when
// the row and the folder disagree or the project is broken.
func (s *Service) resolve(ctx context.Context, owner, id string) (Resolved, bool, error) {
	p, err := s.o.Store.Get(ctx, owner, id)
	if err != nil {
		return Resolved{}, false, err
	}
	if p.Status == StatusBroken {
		return Resolved{}, false, nil
	}
	n, err := s.o.Files.Store().NodeByID(ctx, s.o.Files.Actor(owner), p.Node)
	if errors.Is(err, workspace.ErrNotFound) {
		return Resolved{}, false, nil
	}
	if err != nil {
		return Resolved{}, false, err
	}
	rel := s.o.Files.Rel(owner, n.Path)
	if n.Kind != workspace.KindDir || path.Dir(rel) != s.rootRel() || !strings.EqualFold(n.Name, p.Slug) {
		return Resolved{}, false, nil
	}
	text, found, err := s.o.Files.ReadHome(ctx, owner, rel+"/"+ManifestFile, manifestMax)
	if err != nil {
		return Resolved{}, false, err
	}
	if !found {
		return Resolved{}, false, nil
	}
	m, perr := ParseManifest([]byte(text))
	if perr != nil || m.ID != p.ID {
		return Resolved{}, false, nil
	}
	r := Resolved{Project: p, Manifest: m, FilesPath: rel + "/files", MemoryPath: rel + "/memory"}
	if !m.Attached.Empty() {
		r.Manifest.Attached, r.AttachedBlocked = s.readableAttached(ctx, owner, rel, m)
	}
	return r, true, nil
}

// readableAttached is the attached block of m as the host may apply it. The
// manifest is a file of the project folder: when another person may write the
// folder (OthersWrite) the block is theirs as much as the owner's, so it is
// dropped and blocked is true. A grant list that cannot be read is the same
// answer. The entries outside the limit or of a bad shape are dropped too.
func (s *Service) readableAttached(ctx context.Context, owner, rel string, m Manifest) (att Defaults, blocked bool) {
	abs, err := s.o.Files.Abs(owner, rel+"/"+ManifestFile)
	if err != nil {
		return Defaults{}, true
	}
	others, err := s.o.Files.OthersWrite(ctx, owner)
	if err != nil || others.Covers(abs) {
		return Defaults{}, true
	}
	return EffectiveAttached(m.Defaults, m.Attached), false
}

// List returns the projects of an owner, sorted by name, after a reindex.
func (s *Service) List(ctx context.Context, owner string) ([]Project, error) {
	if err := s.permit(ctx, owner); err != nil {
		return nil, err
	}
	// The manifests are the truth and a person can edit them, so every list
	// reads them. Rows that did not change are not written.
	if err := s.reindexOnce(ctx, owner); err != nil {
		return nil, err
	}
	return s.o.Store.List(ctx, owner)
}

// projectDirs lists the folders directly under Projects/.
func (s *Service) projectDirs(ctx context.Context, owner string) ([]workspace.Node, error) {
	if err := s.o.Files.EnsureHome(ctx, owner); err != nil {
		return nil, err
	}
	abs, err := s.o.Files.Abs(owner, s.rootRel())
	if err != nil {
		return nil, err
	}
	kids, err := s.o.Files.Store().List(ctx, s.o.Files.Actor(owner), abs)
	if errors.Is(err, workspace.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []workspace.Node
	for _, k := range kids {
		if k.Kind == workspace.KindDir {
			dirs = append(dirs, k)
		}
	}
	return dirs, nil
}

// Update changes the manifest of a project and its row. The folder keeps its
// name.
func (s *Service) Update(ctx context.Context, owner, id string, in UpdateInput) (Project, error) {
	return s.rewrite(ctx, owner, id, func(m *Manifest) error {
		if in.Name != nil {
			m.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			m.Description = strings.TrimSpace(*in.Description)
		}
		if in.Defaults != nil {
			m.Defaults = *in.Defaults
		}
		if in.Attached != nil {
			m.Attached = *in.Attached
		}
		if in.Defaults != nil || in.Attached != nil {
			// A narrower limit is refused too when an attached tool would fall outside it.
			return CheckAttached(m.Defaults, m.Attached)
		}
		return nil
	})
}

// Archive hides a project from the active list. Nothing is moved.
func (s *Service) Archive(ctx context.Context, owner, id string) (Project, error) {
	return s.rewrite(ctx, owner, id, func(m *Manifest) error { m.Archived = true; return nil })
}

// Restore makes an archived project active again.
func (s *Service) Restore(ctx context.Context, owner, id string) (Project, error) {
	return s.rewrite(ctx, owner, id, func(m *Manifest) error { m.Archived = false; return nil })
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// writeIfUnchanged replaces the file at rel with content only when it still
// has the hash want. The workspace Put has no refusing revision check (a stale
// BaseVersion keeps both versions instead), so the check is a re-read just
// before the write, under the project lock for writers of this process. It
// returns ErrConflict when the file changed or is gone.
func (s *Service) writeIfUnchanged(ctx context.Context, owner, rel, want, content, title string) error {
	cur, ok, err := s.o.Files.ReadHome(ctx, owner, rel, manifestMax)
	if err != nil {
		return err
	}
	if !ok || contentHash(cur) != want {
		return ErrConflict
	}
	_, err = s.o.Files.WriteHome(ctx, owner, rel, content, title, manifestMax)
	return err
}

func (s *Service) lock(owner, id string) func() {
	v, _ := s.locks.LoadOrStore(owner+"\x00"+id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// rewrite changes the manifest of a project with a compare-and-set on the hash
// of the file it started from. On a change it starts again from the new file,
// once; a second change is ErrConflict.
func (s *Service) rewrite(ctx context.Context, owner, id string, change func(*Manifest) error) (Project, error) {
	defer s.lock(owner, id)()
	for attempt := 0; ; attempt++ {
		r, err := s.Get(ctx, owner, id)
		if err != nil {
			return Project{}, err
		}
		rel := path.Dir(r.FilesPath) + "/" + ManifestFile
		text, ok, err := s.o.Files.ReadHome(ctx, owner, rel, manifestMax)
		if err != nil {
			return Project{}, err
		}
		if !ok {
			return Project{}, fmt.Errorf("%w: %s", ErrInvalid, ManifestFile+" is missing")
		}
		m, err := ParseManifest([]byte(text))
		if err != nil {
			return Project{}, err
		}
		if m.ID != r.ID {
			// The folder was given to another project since Get.
			return Project{}, fmt.Errorf("%w: the folder now belongs to another project", ErrConflict)
		}
		if err := change(&m); err != nil {
			return Project{}, err
		}
		p, err := s.save(ctx, owner, r, rel, contentHash(text), m)
		if errors.Is(err, ErrConflict) && attempt == 0 {
			continue
		}
		if errors.Is(err, ErrConflict) {
			return Project{}, fmt.Errorf("projects: %s: %w", r.Slug, err)
		}
		return p, err
	}
}

// save writes the manifest if it still has the hash want, then the row. The
// manifest is the truth: a failure between the two leaves a row that Reindex
// corrects.
func (s *Service) save(ctx context.Context, owner string, r Resolved, rel, want string, m Manifest) (Project, error) {
	defer s.gen.Add(1)
	out := m.Marshal()
	if _, err := ParseManifest(out); err != nil {
		return Project{}, err
	}
	if err := s.writeIfUnchanged(ctx, owner, rel, want, string(out), "Update the project "+m.Name); err != nil {
		return Project{}, err
	}
	p := r.Project
	p.Name, p.Status, p.Error, p.Updated = m.Name, statusOf(m), "", time.Now()
	if err := s.o.Store.Put(ctx, p); err != nil {
		return Project{}, err
	}
	return p, nil
}

func statusOf(m Manifest) Status {
	if m.Archived {
		return StatusArchived
	}
	return StatusActive
}

// Delete moves the folder of a project to the Trash and removes its row. Chats
// that used the project are not touched. When the folder holds a manifest of a
// different project, nothing is trashed: the stale row is removed and the call
// returns ErrNotFound.
func (s *Service) Delete(ctx context.Context, owner, id string) error {
	defer s.gen.Add(1)
	if err := s.permit(ctx, owner); err != nil {
		return err
	}
	p, err := s.o.Store.Get(ctx, owner, id)
	if err != nil {
		return err
	}
	n, err := s.o.Files.Store().NodeByID(ctx, s.o.Files.Actor(owner), p.Node)
	switch {
	case err == nil:
		// Only delete what the row points at when it still is a project
		// folder; a node reused for something else stays.
		if rel := s.o.Files.Rel(owner, n.Path); rel != "" && path.Dir(rel) == s.rootRel() && n.Kind == workspace.KindDir {
			// The folder may have been given to another project since the row
			// was written. Then it is not ours to trash: drop the row only.
			if text, ok, err := s.o.Files.ReadHome(ctx, owner, rel+"/"+ManifestFile, manifestMax); err == nil && ok {
				if m, perr := ParseManifest([]byte(text)); perr == nil && m.ID != "" && m.ID != p.ID {
					if err := s.o.Store.Delete(ctx, owner, id); err != nil {
						return err
					}
					return fmt.Errorf("%w: the folder now belongs to another project", ErrNotFound)
				}
			}
			if err := s.o.Files.DeleteHome(ctx, owner, rel); err != nil {
				return err
			}
		}
	case !errors.Is(err, workspace.ErrNotFound):
		return err
	}
	return s.o.Store.Delete(ctx, owner, id)
}

// FenceNode is one folder of a project as a fenced agent keeps it: the
// workspace node and where the folder is now, inside the owner's home.
type FenceNode struct {
	Node int64
	Path string
}

// FenceRoots returns the two folders a project agent may use: the project's
// files folder and its memory folder, as nodes of the workspace. It is Get
// followed by FenceRootsOf: a project that is missing, broken or moved out of
// Projects/ is the error of Get.
func (s *Service) FenceRoots(ctx context.Context, owner, id string) (files, memory FenceNode, err error) {
	r, err := s.Get(ctx, owner, id)
	if err != nil {
		return FenceNode{}, FenceNode{}, err
	}
	return s.FenceRootsOf(ctx, owner, r)
}

// FenceRootsOf is FenceRoots for a project that was already resolved. Both
// folders must exist now and be folders, not links or files: a project whose
// files or memory folder was removed or replaced by a file has no fence, and
// the error wraps ErrInvalid. The nodes are read now and kept nowhere.
func (s *Service) FenceRootsOf(ctx context.Context, owner string, r Resolved) (files, memory FenceNode, err error) {
	node := func(rel, what string) (FenceNode, error) {
		abs, err := s.o.Files.Abs(owner, rel)
		if err != nil {
			return FenceNode{}, fmt.Errorf("%w: the %s folder is not a path of the project", ErrInvalid, what)
		}
		n, err := s.o.Files.Store().Stat(ctx, s.o.Files.Actor(owner), abs)
		if errors.Is(err, workspace.ErrNotFound) {
			return FenceNode{}, fmt.Errorf("%w: the %s folder of the project is missing", ErrInvalid, what)
		}
		if err != nil {
			return FenceNode{}, err
		}
		if n.Kind != workspace.KindDir || s.o.Files.Rel(owner, n.Path) != rel {
			return FenceNode{}, fmt.Errorf("%w: the %s folder of the project is not a folder", ErrInvalid, what)
		}
		return FenceNode{Node: n.ID, Path: rel}, nil
	}
	if files, err = node(r.FilesPath, "files"); err != nil {
		return FenceNode{}, FenceNode{}, err
	}
	if memory, err = node(r.MemoryPath, "memory"); err != nil {
		return FenceNode{}, FenceNode{}, err
	}
	return files, memory, nil
}
