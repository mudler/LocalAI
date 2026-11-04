package studio

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The command runners (the built-in bash on this machine, and any sandbox
// runner of the server) take the actor and the view of a call from the chat's
// resolved access, through these two hooks. They resolve the access again at
// every call, like a turn does at its start, so an edit of the chat's agent
// applies from the next call on every frontend (stateless per request).

// ErrCommandAccessChanged means the view of a command was asked for an actor
// that is no longer the chat's: the chat's agent changed its folder or mode
// between the two hooks. The call fails; the next one resolves both again.
var ErrCommandAccessChanged = errors.New("studio: the access of the chat changed; run the command again")

// CommandActor returns the actor a command of the chat reads and commits as:
// the actor of the chat's access, fenced when the chat's agent has a folder.
// A chat whose folder is missing is ErrAgentFolderMissing, and the chat of
// another user session.ErrNotFound.
func (s *Service) CommandActor(ctx context.Context, user, conv string) (workspace.Actor, error) {
	x, err := s.ChatAccess(ctx, user, conv)
	if err != nil {
		return workspace.Actor{}, err
	}
	return x.Actor(), nil
}

// CommandView returns the view a command runner of the chat of a is made
// with: the mounts of the chat's access (the home, or the agent's folder at
// home and the chat folder at chat) and the skill layers, read only. a must
// be the chat's actor of this call (CommandActor): a view is never handed to
// an actor wider than the one the access names.
func (s *Service) CommandView(ctx context.Context, a workspace.Actor) (workspace.View, error) {
	x, err := s.ChatAccess(ctx, a.Principal, a.ChatID)
	if err != nil {
		return workspace.View{}, err
	}
	if got := x.Actor(); got.Principal != a.Principal || got.ChatID != a.ChatID || !got.SameFence(a) {
		return workspace.View{}, ErrCommandAccessChanged
	}
	return x.SandboxView(ctx, s.d.Files.Store())
}
