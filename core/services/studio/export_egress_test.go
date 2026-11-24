package studio

import (
	"context"
	"time"
)

// SettleEgressCardsForTest runs what the end of a turn does to the chat's
// network cards.
func (s *Service) SettleEgressCardsForTest(ctx context.Context, user, conv string, since time.Time) {
	s.settleEgressCards(ctx, principal(user), conv, since)
}
