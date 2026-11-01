package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// turnAccess is the access of a chat turn that the file and media tools, the
// rules nudge, the work folder lines and the prompt's file list take their
// paths and their actor from.
//
// It is the seam with the chat access resolver (chatAccess): until that
// resolves the folder of the chat's agent, it is the unscoped access of a,
// which is today's actor and view. The resolver replaces this body; the
// callers stay. A failure gives an access with no folders, so a tool finds
// nothing rather than reaching more.
func (s *Service) turnAccess(ctx context.Context, a workspace.Actor) session.Access {
	x, err := s.d.Files.UnscopedAccess(ctx, a)
	if err == nil {
		return x
	}
	if s.d.Files.Available() {
		s.log.Warn("studio: no view for the chat's file tools this turn", "chat", a.ChatID, "error", err)
	}
	x, _ = session.NewAccess(a, workspace.View{Actor: a})
	return x
}
