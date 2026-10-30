package pages

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The starter pages ship with the server. A user copies one into pages/ with one
// click and owns the copy, which the assistant can then change.
//
//go:embed all:starters
var starterFS embed.FS

// maxCopies bounds the search for a free name when the user copies a starter
// again and again.
const maxCopies = 50

// Starter is a page that ships with the server.
type Starter struct {
	ID          string
	Title       string
	Icon        string
	Description string
}

// Starters lists the starter pages, sorted by id.
func (s *Service) Starters() []Starter {
	dirs, err := fs.ReadDir(starterFS, "starters")
	if err != nil {
		return nil
	}
	out := []Starter{}
	for _, d := range dirs {
		if !d.IsDir() || !ValidName(d.Name()) {
			continue
		}
		st := Starter{ID: d.Name(), Title: d.Name()}
		if raw, err := starterFS.ReadFile(path.Join("starters", d.Name(), MetaFile)); err == nil {
			var m struct{ Title, Icon, Description string }
			if json.Unmarshal(raw, &m) == nil {
				if m.Title != "" {
					st.Title = clip(m.Title, maxTitle)
				}
				if iconRE.MatchString(m.Icon) {
					st.Icon = m.Icon
				}
				st.Description = clip(m.Description, maxDesc)
			}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CopyStarter copies a starter into pages/ under its id, or under the first free
// "<id>-N", in one change set, and returns the new page.
func (s *Service) CopyStarter(ctx context.Context, principal, id string) (Page, error) {
	if !ValidName(id) {
		return Page{}, ErrNotFound
	}
	root := path.Join("starters", id)
	if _, err := fs.Stat(starterFS, path.Join(root, IndexFile)); err != nil {
		return Page{}, ErrNotFound
	}
	a := s.actor(principal)
	for i := 1; i <= maxCopies; i++ {
		name := id
		if i > 1 {
			name = id + "-" + strconv.Itoa(i)
		}
		if len(name) > 64 {
			break
		}
		l, err := s.own(principal, name)
		if err != nil {
			return Page{}, err
		}
		folder := l.folder
		if _, err := s.f.Store().Stat(ctx, a, folder); err == nil {
			continue
		} else if !errors.Is(err, workspace.ErrNotFound) {
			return Page{}, err
		}
		if err := s.copyTree(ctx, a, root, folder, name); err != nil {
			if errors.Is(err, workspace.ErrExists) {
				continue // another tab took the name first
			}
			return Page{}, err
		}
		return s.Get(ctx, principal, name)
	}
	return Page{}, fmt.Errorf("%w: no free name for another copy of %s", workspace.ErrExists, id)
}

func (s *Service) copyTree(ctx context.Context, a workspace.Actor, root, folder, name string) error {
	tx, err := s.f.Store().Begin(ctx, a, "Add the page "+name)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	err = fs.WalkDir(starterFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := starterFS.Open(p)
		if err != nil {
			return err
		}
		defer body.Close()
		_, err = tx.Put(ctx, folder+"/"+strings.TrimPrefix(p, root+"/"), body, workspace.PutOptions{})
		return err
	})
	if err != nil {
		return err
	}
	_, err = tx.Commit()
	if errors.Is(err, workspace.ErrCommitted) {
		return nil
	}
	return err
}
