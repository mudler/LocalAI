package projects

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// folder is one directory of Projects/ with what its manifest says.
type folder struct {
	node  workspace.Node
	rel   string
	m     Manifest
	valid bool
	err   string // why the manifest is unusable
	fresh bool   // the manifest needs a new ID written back
	hash  string // hash of the manifest text that was read
	oldID string // the ID the manifest had when it was read
}

// placement is a row to write, and the manifest to rewrite after it.
type placement struct {
	p Project
	f *folder
}

// Reindex makes the index match the folders under Projects/: it adds folders
// that have no row, follows moved folders by the ID in their manifest, takes
// Archived from the manifest, and marks a project broken when its manifest is
// missing or invalid or its folder is gone. It never deletes a folder or a row.
//
// A manifest without an ID, or with an ID that two folders share or that
// another owner holds, belongs to a copy: the copy gets a new ID, written back
// into its manifest.
func (s *Service) Reindex(ctx context.Context, owner string) error {
	defer s.gen.Add(1)
	if err := s.permit(ctx, owner); err != nil {
		return err
	}
	dirs, err := s.projectDirs(ctx, owner)
	if err != nil {
		return err
	}
	rows, err := s.o.Store.List(ctx, owner)
	if err != nil {
		return err
	}
	byID, byNode := map[string]Project{}, map[int64]Project{}
	for _, r := range rows {
		byID[r.ID], byNode[r.Node] = r, r
	}
	folders := make([]*folder, 0, len(dirs))
	for _, d := range dirs {
		f := &folder{node: d, rel: s.rootRel() + "/" + d.Name}
		text, ok, err := s.o.Files.ReadHome(ctx, owner, f.rel+"/"+ManifestFile, manifestMax)
		if err != nil {
			return err
		}
		switch {
		case !ok:
			f.err = ManifestFile + " is missing"
		default:
			m, perr := ParseManifest([]byte(text))
			if perr != nil {
				f.err = "invalid " + ManifestFile + ": " + strings.TrimPrefix(perr.Error(), ErrInvalid.Error()+": ")
			} else {
				f.m, f.valid, f.hash, f.oldID = m, true, contentHash(text), m.ID
			}
		}
		folders = append(folders, f)
	}

	// Two folders with one ID are a copy: the one the index knows keeps it.
	group := map[string][]*folder{}
	for _, f := range folders {
		if f.valid && f.m.ID != "" {
			group[f.m.ID] = append(group[f.m.ID], f)
		}
	}
	for id, g := range group {
		keep := g[0]
		for _, f := range g {
			if f.node.ID == byID[id].Node {
				keep = f
			}
		}
		for _, f := range g {
			if f != keep {
				f.m.ID, f.fresh = "", true
			}
		}
	}

	var todo []*placement
	used := map[string]bool{}
	for _, f := range folders {
		if f.valid && f.m.ID != "" {
			used[f.m.ID] = true
		}
	}
	for _, f := range folders {
		if !f.valid {
			continue
		}
		if f.m.ID == "" {
			// A manifest that lost its ID keeps the row of its folder.
			if id := byNode[f.node.ID].ID; id != "" && !used[id] {
				f.m.ID = id
			} else {
				f.m.ID = s.freshID(used)
			}
			f.fresh = true
		}
		used[f.m.ID] = true
		old := byID[f.m.ID]
		p := Project{ID: f.m.ID, Owner: owner, Slug: f.node.Name, Name: f.m.Name, Node: f.node.ID, Status: statusOf(f.m), Updated: updated(f.node, old)}
		todo = append(todo, &placement{p: p, f: f})
	}
	for _, f := range folders {
		if f.valid {
			continue
		}
		// A broken folder keeps the ID of the row that points at it.
		id := byNode[f.node.ID].ID
		if id == "" || used[id] {
			id = s.freshID(used)
		}
		used[id] = true
		p := Project{ID: id, Owner: owner, Slug: f.node.Name, Name: nameOf(byID[id], f.node.Name), Node: f.node.ID, Status: StatusBroken, Error: f.err, Updated: updated(f.node, byID[id])}
		todo = append(todo, &placement{p: p, f: f})
	}
	seen := map[string]bool{}
	for _, pl := range todo {
		seen[pl.p.ID] = true
	}
	// Rows with no folder stay, marked broken. They give up their slug so a
	// new folder with that name can be indexed.
	for _, r := range rows {
		if seen[r.ID] {
			continue
		}
		why := errGone
		if n, err := s.o.Files.Store().NodeByID(ctx, s.o.Files.Actor(owner), r.Node); err == nil && s.o.Files.Rel(owner, n.Path) != "" && path.Dir(s.o.Files.Rel(owner, n.Path)) != s.rootRel() {
			why = errOutside
		}
		if r.Status == StatusBroken && r.Error == why && strings.HasSuffix(r.Slug, goneMark+r.ID) {
			continue
		}
		r.Status, r.Error, r.Updated = StatusBroken, why, time.Now()
		r.Slug = placeholderSlug(r)
		todo = append(todo, &placement{p: r})
	}
	return s.place(ctx, owner, byID, todo)
}

func nameOf(old Project, fallback string) string {
	if old.Name != "" {
		return old.Name
	}
	return fallback
}

func updated(n workspace.Node, old Project) time.Time {
	if !n.Modified.IsZero() {
		return n.Modified
	}
	if !old.Updated.IsZero() {
		return old.Updated
	}
	return time.Now()
}

func (s *Service) freshID(used map[string]bool) string {
	for {
		if id := NewID(); !used[id] {
			return id
		}
	}
}

// place writes the rows. A slug can be taken only for a moment, as when two
// folders swap names, so a refused row is tried again while others succeed.
func (s *Service) place(ctx context.Context, owner string, byID map[string]Project, todo []*placement) error {
	for len(todo) > 0 {
		var again []*placement
		var firstErr error
		for _, pl := range todo {
			err := s.putOne(ctx, owner, byID, pl)
			if errors.Is(err, ErrNameTaken) && s.freeGoneSlug(ctx, owner, pl.p) {
				err = s.putOne(ctx, owner, byID, pl)
			}
			if errors.Is(err, ErrNameTaken) {
				again = append(again, pl)
				continue
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if len(again) == len(todo) {
			var errs []error
			for _, pl := range again {
				errs = append(errs, fmt.Errorf("projects: folder %q: %w", pl.p.Slug, ErrNameTaken))
			}
			return errors.Join(errs...)
		}
		if firstErr != nil {
			return firstErr
		}
		todo = again
	}
	return nil
}

func (s *Service) putOne(ctx context.Context, owner string, byID map[string]Project, pl *placement) error {
	for try := 0; ; try++ {
		old, had := byID[pl.p.ID]
		if !(had && sameRow(old, pl.p)) {
			err := s.o.Store.Put(ctx, pl.p)
			if isIDInUse(err) && try < maxCreateTries {
				// Another owner holds this ID: this folder is a copy.
				pl.p.ID = NewID()
				if pl.f != nil && pl.f.valid {
					pl.f.m.ID, pl.f.fresh = pl.p.ID, true
				}
				continue
			}
			if err != nil {
				return err
			}
		}
		break
	}
	if pl.f != nil && pl.f.valid && pl.f.fresh {
		err := s.writeBackID(ctx, owner, pl.f)
		if errors.Is(err, errSkipWriteback) {
			// The manifest is not ours to change any more. Drop the row this
			// pass made under the new ID; the next pass places the folder.
			if _, had := byID[pl.p.ID]; !had {
				return s.o.Store.Delete(ctx, owner, pl.p.ID)
			}
			return nil
		}
		return err
	}
	return nil
}

// errSkipWriteback says the manifest changed in a way that is not a conflict:
// a person gave it an ID, or it is gone or unreadable. Reindex leaves it for
// the next pass.
var errSkipWriteback = errors.New("projects: manifest changed during the reindex")

// reindexOnce runs Reindex and, on a conflict with a concurrent writer, once
// more: the second pass reads the files as they are now.
func (s *Service) reindexOnce(ctx context.Context, owner string) error {
	err := s.Reindex(ctx, owner)
	if errors.Is(err, ErrConflict) {
		err = s.Reindex(ctx, owner)
	}
	return err
}

// writeBackID writes the new ID of a folder into its manifest if the file is
// unchanged since the read. When it changed, the new content is read once and
// gets the ID. If its ID is not the one this pass saw, a person gave the
// project an ID of their own, or it is gone or unreadable: errSkipWriteback.
// A second change in a row is ErrConflict.
func (s *Service) writeBackID(ctx context.Context, owner string, f *folder) error {
	rel := f.rel + "/" + ManifestFile
	m, hash := f.m, f.hash
	for attempt := 0; ; attempt++ {
		title := "Give the project " + m.Name + " its own ID"
		err := s.writeIfUnchanged(ctx, owner, rel, hash, string(m.Marshal()), title)
		if !errors.Is(err, ErrConflict) {
			return err
		}
		if attempt > 0 {
			return fmt.Errorf("projects: folder %q: %w", f.node.Name, err)
		}
		text, ok, rerr := s.o.Files.ReadHome(ctx, owner, rel, manifestMax)
		if rerr != nil {
			return rerr
		}
		if !ok {
			return errSkipWriteback
		}
		fresh, perr := ParseManifest([]byte(text))
		if perr != nil || fresh.ID != f.oldID {
			return errSkipWriteback
		}
		fresh.ID = f.m.ID
		m, hash = fresh, contentHash(text)
	}
}

// isIDInUse says whether Put refused the row because another owner holds its
// ID, as opposed to a bad row.
func isIDInUse(err error) bool {
	return err != nil && errors.Is(err, ErrInvalid) && strings.Contains(err.Error(), "id in use")
}

func sameRow(a, b Project) bool {
	return a.Owner == b.Owner && strings.EqualFold(a.Slug, b.Slug) && a.Name == b.Name && a.Node == b.Node && a.Status == b.Status && a.Error == b.Error
}

// goneMark separates the old slug from the ID in the placeholder slug of a
// project whose folder is not in Projects/. No folder name can be taken by it
// in practice, and it keeps the row findable by ID.
const goneMark = "~gone-"

func placeholderSlug(r Project) string {
	base := r.Slug
	if i := strings.Index(base, goneMark); i >= 0 {
		base = base[:i]
	}
	// The slug column holds 255 bytes. Keep the whole placeholder well inside
	// it, and never cut a multi-byte character in two.
	room := maxPlaceholderBytes - len(goneMark) - len(r.ID)
	if room < 0 {
		room = 0
	}
	if len(base) > room {
		base = base[:room]
		for len(base) > 0 && !utf8.ValidString(base) {
			base = base[:len(base)-1]
		}
	}
	return base + goneMark + r.ID
}

// maxPlaceholderBytes bounds a placeholder slug, below the 255 bytes of the
// slug column.
const maxPlaceholderBytes = 200

func isGone(r Project) bool {
	return r.Status == StatusBroken && (r.Error == errGone || r.Error == errOutside)
}

// freeGoneSlug moves the row that holds p's slug to a placeholder slug when
// its folder is gone, and says whether it did. Only the index row changes.
func (s *Service) freeGoneSlug(ctx context.Context, owner string, p Project) bool {
	rows, err := s.o.Store.List(ctx, owner)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.ID == p.ID || !strings.EqualFold(r.Slug, p.Slug) || !isGone(r) {
			continue
		}
		r.Slug = placeholderSlug(r)
		return s.o.Store.Put(ctx, r) == nil
	}
	return false
}
