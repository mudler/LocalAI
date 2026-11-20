package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/notify"
)

// Notifier returns the inbox service, or nil when there is none.
func (s *Service) Notifier() *notify.Service { return s.d.Notify }

// MarkChatNotificationsRead marks the notifications about a chat read. It
// does nothing without an inbox.
func (s *Service) MarkChatNotificationsRead(ctx context.Context, user, conv string) {
	if s.d.Notify == nil {
		return
	}
	if err := s.d.Notify.MarkChatRead(ctx, principal(user), conv); err != nil {
		s.log.Warn("studio: could not mark a chat's notifications read", "chat", conv, "error", err)
	}
}
