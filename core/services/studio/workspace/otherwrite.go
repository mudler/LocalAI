package workspace

import (
	"context"
	"errors"
	"path"
)

// ErrNoGrantList means the store cannot list its grants, so OthersWrite cannot
// tell which folders are safe to read with an owner's authority.
var ErrNoGrantList = errors.New("workspace: the store cannot list its grants")

// Writable is the set of folders of one tree on which another principal than
// its owner holds write access, from OthersWrite.
type Writable struct{ folders []string }

// Covers reports whether p is one of the folders or below one: someone other
// than the owner may write p, through a grant on p or on a folder above it.
func (w Writable) Covers(p string) bool {
	for _, f := range w.folders {
		if Within(p, f) {
			return true
		}
	}
	return false
}

// OthersWrite returns the folders of the tree root, root and the folders
// above it included, on which a principal other than owner holds a write
// grant, including a group such as everyone. A read grant does not count. It
// is the one check of the host for content it reads with the owner's
// authority (the owner's skills, notes, profiles and pages): a file that
// another person may write is that person's text, not the owner's, so the
// host leaves it out. The store must list grants (GrantLister and GrantTree);
// admin is its administrator.
func OthersWrite(ctx context.Context, st Store, admin Actor, owner, root string) (Writable, error) {
	gl, okList := st.(GrantLister)
	gt, okTree := st.(GrantTree)
	if !okList || !okTree {
		return Writable{}, ErrNoGrantList
	}
	var out Writable
	add := func(p string, gs []Grant) {
		for _, g := range gs {
			if g.Principal != owner && g.Mode >= ModeWrite {
				out.folders = append(out.folders, p)
				return
			}
		}
	}
	for p := root; ; p = path.Dir(p) {
		gs, err := gl.Grants(ctx, admin, p)
		switch {
		case errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid):
		case err != nil:
			return Writable{}, err
		default:
			add(p, gs)
		}
		if p == "/" {
			break
		}
	}
	below, err := gt.GrantsBelow(ctx, admin, root)
	if errors.Is(err, ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return Writable{}, err
	}
	for _, g := range below {
		add(g.Path, []Grant{g.Grant})
	}
	return out, nil
}
