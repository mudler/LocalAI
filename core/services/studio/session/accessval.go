package session

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Access is what one chat turn may reach in the workspace: its actor (fenced
// when the chat's agent has a folder), the view of its folders and the place
// that maps the model's paths. It has no exported fields and one
// constructor, so a tool cannot be built from a principal string.
type Access struct {
	actor workspace.Actor
	view  workspace.View
	place Place
}

// NewAccess returns the access of an actor and the view built for it.
func NewAccess(a workspace.Actor, v workspace.View) (Access, error) {
	if !validPrincipal(a.Principal) {
		return Access{}, fmt.Errorf("session: no access for principal %q", a.Principal)
	}
	v.Actor = copyActor(a)
	return Access{actor: copyActor(a), view: v, place: NewPlace(v)}, nil
}

// Actor is the actor of the turn.
func (x Access) Actor() workspace.Actor { return copyActor(x.actor) }

// View is the view of the turn's folders.
func (x Access) View() workspace.View {
	v := x.view
	v.Mounts = append([]workspace.Mount(nil), v.Mounts...)
	return v
}

// Place maps the model's paths to workspace paths.
func (x Access) Place() Place { return x.place }

// Owner is the user's own actor, without the fence and the chat ID, for the
// host's bookkeeping (chat folder rename, upload of a person's attachment).
func (x Access) Owner() workspace.Actor {
	return workspace.Actor{Principal: x.actor.Principal, Groups: append([]string(nil), x.actor.Groups...)}
}
