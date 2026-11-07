package studio

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// HoldSenderForTest gives the running turn of a connector chat the grant that
// Authorize computes for a sender on a binding, as the connector manager does
// when a message comes in. senderID is paired, listed or neither, as the
// binding says.
func (s *Service) HoldSenderForTest(chat string, b connectors.Binding, senderID string, listed bool, now time.Time) {
	subject := AccessSubject{SenderID: senderID, Allowlisted: true, Listed: listed}
	g := turnGrant{Binding: b.ID, Kind: b.Kind, Senders: []string{senderID}}
	for _, name := range contract.ConnectorCapabilityNames() {
		if d := Authorize(b, nil, subject, name, now, Block{}); d.Allowed {
			g.Caps = append(g.Caps, name)
		} else {
			if g.Denied == nil {
				g.Denied = map[string]string{}
			}
			g.Denied[name] = d.Say()
		}
	}
	s.access.set(chat, g)
}

// ReleaseSenderForTest ends the grant of a chat, as the manager does when the
// reply to the sender ends. A loop run has no grant.
func (s *Service) ReleaseSenderForTest(chat string) { s.access.clear(chat) }

// BeginByModelForTest is the start path of start_loop (wait false) or wait_for
// (wait true), called with no check of the tool list.
func (h *Loops) BeginByModelForTest(ctx context.Context, user, conv string, wait bool) (loops.Loop, error) {
	by := &modelStart{wait: time.Minute, reason: "test"}
	if wait {
		by.watch = &loops.Watch{Tool: "list_things", Mode: loops.UntilChange}
	}
	return h.begin(ctx, user, conv, LoopInput{Prompt: "Check it."}, by)
}

// MaxRunsForTest is the run limit of the loops in a chat.
func (h *Loops) MaxRunsForTest(ctx context.Context, user, conv string, lim LoopLimits) int {
	return h.maxRunsFor(ctx, principal(user), conv, lim)
}
