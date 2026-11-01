package session

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Access is what one chat turn may reach in the workspace: its actor (fenced
// when the chat's agent has a folder) and the view of its folders. It has no
// exported fields and one constructor, so a tool cannot be built from a
// principal string.
type Access struct {
	actor workspace.Actor
	view  workspace.View
}

// NewAccess returns the access of an actor and the view built for it. The
// actor must name a chat.
func NewAccess(a workspace.Actor, v workspace.View) (Access, error) {
	if !validPrincipal(a.Principal) {
		return Access{}, fmt.Errorf("%w: no access for principal %q", ErrInvalid, a.Principal)
	}
	if a.ChatID == "" {
		return Access{}, fmt.Errorf("%w: the access of a chat needs its chat ID", ErrInvalid)
	}
	v.Actor = copyActor(a)
	v.Mounts = append([]workspace.Mount(nil), v.Mounts...)
	return Access{actor: copyActor(a), view: v}, nil
}

// Actor is the actor of the turn.
func (x Access) Actor() workspace.Actor { return copyActor(x.actor) }

// View is the view of the turn's folders.
func (x Access) View() workspace.View {
	v := x.view
	v.Actor = copyActor(v.Actor)
	v.Mounts = append([]workspace.Mount(nil), v.Mounts...)
	return v
}

// Owner is the user's own actor, without the fence and the chat ID, for the
// host's bookkeeping (chat folder rename, upload of a person's attachment).
func (x Access) Owner() workspace.Actor {
	return workspace.Actor{Principal: x.actor.Principal, Groups: append([]string(nil), x.actor.Groups...)}
}

// Scoped reports whether the actor of the access is fenced: the chat's agent
// has a folder.
func (x Access) Scoped() bool {
	_, ok := x.actor.Fence()
	return ok
}
