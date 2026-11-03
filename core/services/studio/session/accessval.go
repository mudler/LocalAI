package session

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Access is what one chat turn may reach in the workspace: its actor (fenced
// when the chat's agent has a folder), the view of its folders and the place
// that maps the model's paths. It has no exported fields and one constructor,
// so a tool cannot be built from a principal string.
type Access struct {
	actor workspace.Actor
	view  workspace.View
	place Place
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
	return Access{actor: copyActor(a), view: v, place: NewPlace(v)}, nil
}

// ForChat is the same access for another chat of the same user: the same
// fence, view and place, with chatID on the actor. A sub-agent's file tools
// act for its parent chat with it, so what they change is a change of the
// parent, while they reach only what the sub-agent's access reaches.
func (x Access) ForChat(chatID string) (Access, error) {
	a := copyActor(x.actor)
	a.ChatID = chatID
	return NewAccess(a, x.view)
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

// Place maps the model's paths to workspace paths.
func (x Access) Place() Place { return x.place }

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

// SandboxView is the view a command runner of the chat is made with: the
// mounts of the turn's view (the home, or the agent's folder and the chat
// folder) and the skill layers the actor can read, read only, at the names
// the file tools use, so a skill's scripts run in place. It is built with the
// access's actor, so the store caps every mount at the actor's fence.
func (x Access) SandboxView(ctx context.Context, st workspace.Store) (workspace.View, error) {
	v := x.View()
	if st == nil {
		return v, ErrNoWorkspace
	}
	layers, err := SkillLayerMounts(ctx, st, x.actor)
	if err != nil {
		return workspace.View{}, err
	}
	taken := map[string]bool{}
	for _, m := range v.Mounts {
		taken[m.At] = true
	}
	var add []workspace.MountRequest
	for _, l := range layers {
		if !taken[l.At] {
			add = append(add, l)
		}
	}
	if len(add) == 0 {
		return v, nil
	}
	lv, err := st.View(ctx, x.actor, add)
	if err != nil {
		return workspace.View{}, err
	}
	v.Mounts = append(v.Mounts, lv.Mounts...)
	return v, nil
}
