package studio

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// AddonAccess is what an add-on's workspace operation may reach, for the host
// operations of add-ons (core/services/addons/hostops). conv is the chat the
// add-on call was made for, from the host's binding of the call. A chat's call
// gets the chat's access: fenced to the agent's folder and the chat folder
// when the chat's agent has a folder, so an add-on reaches no more than the
// chat's own file tools. An error (no such chat, a chat of another user, a
// missing folder) refuses the call.
//
// A call that no chat made (conv "": an add-on page, the web interface, the
// OpenAI-compatible route) gets the user's whole home: the person acts with
// their own rights there, as on the Files page.
func (s *Service) AddonAccess(ctx context.Context, user, conv string) (session.Access, error) {
	if conv != "" {
		return s.ChatAccess(ctx, user, conv)
	}
	p := principal(user)
	if err := s.d.Files.EnsureHome(ctx, p); err != nil {
		return session.Access{}, fmt.Errorf("studio: the home of an add-on call: %w", err)
	}
	return s.mgr.PersonAccess(ctx, userChatActor(p, ""))
}
