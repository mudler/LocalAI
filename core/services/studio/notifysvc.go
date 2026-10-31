package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/notify"
)

// Notifier returns the inbox service, or nil when there is none.
func (s *Service) Notifier() *notify.Service { return s.d.Notify }

// Notifications lists the user's notifications, newest first.
func (s *Service) Notifications(ctx context.Context, user string, unreadOnly bool, limit int) ([]notify.Notification, error) {
	if s.d.Notify == nil {
		return nil, ErrNotificationsOff
	}
	return s.d.Notify.List(ctx, principal(user), unreadOnly, limit)
}

// UnreadNotifications returns the number of unread notifications of the user.
func (s *Service) UnreadNotifications(ctx context.Context, user string) (int, error) {
	if s.d.Notify == nil {
		return 0, ErrNotificationsOff
	}
	return s.d.Notify.Count(ctx, principal(user))
}

// MarkNotificationsRead marks the named notifications read; with all set it
// marks every one.
func (s *Service) MarkNotificationsRead(ctx context.Context, user string, ids []int64, all bool) error {
	if s.d.Notify == nil {
		return ErrNotificationsOff
	}
	if all {
		return s.d.Notify.MarkAllRead(ctx, principal(user))
	}
	return s.d.Notify.MarkRead(ctx, principal(user), ids)
}

// DeleteNotifications removes the named notifications.
func (s *Service) DeleteNotifications(ctx context.Context, user string, ids []int64) error {
	if s.d.Notify == nil {
		return ErrNotificationsOff
	}
	return s.d.Notify.Delete(ctx, principal(user), ids)
}

// ClearNotifications removes every notification of the user, or with readOnly
// the read ones, and returns how many it removed.
func (s *Service) ClearNotifications(ctx context.Context, user string, readOnly bool) (int, error) {
	if s.d.Notify == nil {
		return 0, ErrNotificationsOff
	}
	return s.d.Notify.DeleteAll(ctx, principal(user), readOnly)
}

// TotalNotifications returns how many notifications the user keeps, read or not.
func (s *Service) TotalNotifications(ctx context.Context, user string) (int, error) {
	if s.d.Notify == nil {
		return 0, ErrNotificationsOff
	}
	all, err := s.d.Notify.List(ctx, principal(user), false, 0)
	return len(all), err
}

// SubscribeNotifications returns the user's new notifications as they arrive.
func (s *Service) SubscribeNotifications(user string) (<-chan notify.Notification, func(), error) {
	if s.d.Notify == nil {
		return nil, nil, ErrNotificationsOff
	}
	ch, cancel := s.d.Notify.Subscribe(principal(user))
	return ch, cancel, nil
}

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

// Pusher returns the browser push service, or nil when push is off.
func (s *Service) Pusher() *notify.Pusher { return s.d.Push }
