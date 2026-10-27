package notify

import (
	"context"
	"time"
)

// Options builds a Service.
type Options struct {
	Store Store
	// Fanout delivers live. Nil: a fanout for this process only.
	Fanout Fanout
	// Clock returns the time of a new notification. Nil: time.Now.
	Clock func() time.Time
}

// Service stores notifications and delivers them live.
type Service struct {
	st    Store
	fan   Fanout
	clock func() time.Time
	hooks []func(Notification)
}

// NewService returns a Service. The store is required.
func NewService(o Options) *Service {
	s := &Service{st: o.Store, fan: o.Fanout, clock: o.Clock}
	if s.fan == nil {
		s.fan = NewLocalFanout()
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	return s
}

// OnAdd registers f to be called, on the frontend that added it, with every
// notification after it is stored. Browser push uses it. Call it before the
// service is shared.
func (s *Service) OnAdd(f func(Notification)) { s.hooks = append(s.hooks, f) }

// Add stores a notification for its Principal and delivers it live. The text is
// cut to its limits; Created and Read are set here. It returns the stored
// notification.
func (s *Service) Add(ctx context.Context, n Notification) (Notification, error) {
	n = Clean(n)
	n.Created, n.Read = s.clock().UTC(), false
	id, err := s.st.Add(ctx, n)
	if err != nil {
		return Notification{}, err
	}
	n.ID = id
	s.fan.Publish(n.Principal, n)
	for _, h := range s.hooks {
		h(n)
	}
	return n, nil
}

// Subscribe returns the live notifications of a principal.
func (s *Service) Subscribe(principal string) (<-chan Notification, func()) {
	return s.fan.Subscribe(principal)
}

// List returns the principal's notifications, newest first.
func (s *Service) List(ctx context.Context, principal string, unreadOnly bool, limit int) ([]Notification, error) {
	return s.st.List(ctx, principal, unreadOnly, limit)
}

// Count returns the number of unread notifications.
func (s *Service) Count(ctx context.Context, principal string) (int, error) {
	return s.st.Count(ctx, principal)
}

// MarkRead marks notifications read.
func (s *Service) MarkRead(ctx context.Context, principal string, ids []int64) error {
	return s.st.MarkRead(ctx, principal, ids)
}

// MarkChatRead marks the notifications about one chat read.
func (s *Service) MarkChatRead(ctx context.Context, principal, chatID string) error {
	return s.st.MarkChatRead(ctx, principal, ChatLink(chatID))
}

// MarkAllRead marks every notification read.
func (s *Service) MarkAllRead(ctx context.Context, principal string) error {
	return s.st.MarkAllRead(ctx, principal)
}

// Delete removes notifications.
func (s *Service) Delete(ctx context.Context, principal string, ids []int64) error {
	return s.st.Delete(ctx, principal, ids)
}

// ChatLink is the link of a notification about a chat.
func ChatLink(chatID string) string { return "/app/chats/" + chatID }
