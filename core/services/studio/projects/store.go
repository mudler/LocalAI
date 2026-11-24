package projects

import (
	"context"
	"fmt"
)

// errIDInUse is returned when an ID belongs to another owner. It does not say
// whose project it is.
var errIDInUse = fmt.Errorf("%w: project id in use", ErrInvalid)

// Store keeps the index of projects: one record per project, found by owner
// and ID. The folder and manifest on disk stay the source of truth for the
// content; the index is what lists and resolves projects quickly.
//
// A slug is unique per owner without regard to case. Put stores the slug in
// lower case.
type Store interface {
	// Put creates or replaces a project. Another project of the same owner
	// with an equal-fold slug gives ErrNameTaken. An empty slug, or an ID that
	// another owner holds, gives ErrInvalid and changes nothing.
	Put(ctx context.Context, p Project) error
	// Get returns a project. A project of another owner is ErrNotFound.
	Get(ctx context.Context, owner, id string) (Project, error)
	// List returns the projects of one owner, sorted by name.
	List(ctx context.Context, owner string) ([]Project, error)
	// Delete removes a project. A missing project is not an error.
	Delete(ctx context.Context, owner, id string) error
}
