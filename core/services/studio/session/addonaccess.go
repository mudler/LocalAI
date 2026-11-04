package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// PersonAccess is the access of a person's own call that no chat made: an
// add-on page, a call of the web interface or of the OpenAI-compatible route.
// It works on the whole home, as the Files page does, because the person acts
// with their own rights there. A call that a chat made always takes the chat's
// access instead. The actor must not be fenced and must name no chat.
func (m *Manager) PersonAccess(ctx context.Context, a workspace.Actor) (Access, error) {
	switch {
	case !validPrincipal(a.Principal):
		return Access{}, fmt.Errorf("%w: no access for principal %q", ErrInvalid, a.Principal)
	case a.ChatID != "":
		return Access{}, fmt.Errorf("%w: the call of a chat takes the chat's access", ErrInvalid)
	case workspace.HasFence(a):
		return Access{}, fmt.Errorf("%w: a fenced actor has no view of the home", ErrInvalid)
	}
	v, err := m.buildView(ctx, a, HomePath(a.Principal), nil, "")
	if err != nil {
		return Access{}, err
	}
	v.Actor = copyActor(a)
	v.Mounts = append([]workspace.Mount(nil), v.Mounts...)
	return Access{actor: copyActor(a), view: v, place: NewPlace(v)}, nil
}

// SubView builds a view of some folders inside the access, for code that
// mounts folders of its own choice (an add-on that materializes a tree). Each
// request must name a folder inside a mount of the access's view, and gets at
// most that mount's mode. The store builds the view for the access's actor, so
// the fence of a scoped chat applies again. A folder outside every mount is
// workspace.ErrNotFound: the access does not reach it.
func (x Access) SubView(ctx context.Context, st workspace.Store, reqs []workspace.MountRequest) (workspace.View, error) {
	if st == nil || len(reqs) == 0 {
		return workspace.View{}, fmt.Errorf("%w: a view needs a store and a folder", ErrInvalid)
	}
	out := make([]workspace.MountRequest, 0, len(reqs))
	for _, r := range reqs {
		segs, err := workspace.SplitPath(r.Path)
		if err != nil || !strings.HasPrefix(r.Path, "/") || workspace.JoinPath(segs) != r.Path {
			return workspace.View{}, workspace.ErrBadName
		}
		within, ok := x.mountOf(r.Path)
		if !ok {
			return workspace.View{}, fmt.Errorf("%w: the folder is outside this access", workspace.ErrNotFound)
		}
		r.Mode = min(r.Mode, within.Mode)
		out = append(out, r)
	}
	return st.View(ctx, x.Actor(), out)
}

// mountOf returns the mount of the view that holds p most closely.
func (x Access) mountOf(p string) (workspace.Mount, bool) {
	var best workspace.Mount
	found := false
	for _, m := range x.view.Mounts {
		root := strings.TrimSuffix(m.Path, "/")
		if root == "" || (p != root && !strings.HasPrefix(p, root+"/")) {
			continue
		}
		if !found || len(root) > len(strings.TrimSuffix(best.Path, "/")) {
			best, found = m, true
		}
	}
	return best, found
}
