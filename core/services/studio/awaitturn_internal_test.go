package studio

import (
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("awaitTurn tokens", func() {
	It("adds up the prompt and completion tokens of the turn's requests", func() {
		ev := make(chan session.Event, 8)
		// A usage report before the turn starts belongs to another turn.
		ev <- session.Event{Kind: session.KindUsage, Usage: &session.UsageEvent{Used: 999, Completion: 1}}
		ev <- session.Event{Kind: session.KindTurnStart}
		ev <- session.Event{Kind: session.KindUsage, Usage: &session.UsageEvent{Used: 1000, Completion: 200}}
		ev <- session.Event{Kind: session.KindUsage, Usage: &session.UsageEvent{Used: 1300, Completion: 50}}
		ev <- session.Event{Kind: session.KindUsage}
		end := (&Service{}).awaitTurn("1", "c", ev, 50*time.Millisecond, nil)
		Expect(end.How).To(Equal(TurnTimedOut))
		Expect(end.Tokens).To(Equal(2550))
	})
})
