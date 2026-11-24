package studio

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
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

var _ = Describe("awaitTurn across a resume", func() {
	ctx := context.Background()

	// A turn that ends interrupted and will be resumed is not the end of the
	// watch: the resumed turn is.
	It("keeps watching a turn that the server will resume", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "c", Principal: "1", Model: "m", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		s := &Service{d: Deps{Store: st, AutoResume: true}}
		Expect(st.ClaimTurn(ctx, "1", "c", "r0")).To(Succeed())
		Expect(st.MarkInterrupted(ctx, "1", "c", "r0", 0)).To(Succeed())

		ev := make(chan session.Event, 8)
		ev <- session.Event{Kind: session.KindTurnStart}
		ev <- session.Event{Kind: session.KindError, Text: "The turn was interrupted."}
		ev <- session.Event{Kind: session.KindTurnEnd}
		done := make(chan TurnEnd, 1)
		go func() { done <- s.awaitTurn("1", "c", ev, 10*time.Second, nil) }()

		Consistently(done, 300*time.Millisecond).ShouldNot(Receive(), "the interrupted turn ended the watch")

		// The sweep resumes the turn, and it finishes.
		Expect(st.ClaimResume(ctx, "1", "c", "r1")).To(Succeed())
		ev <- session.Event{Kind: session.KindTurnStart}
		ev <- session.Event{Kind: session.KindUsage, Usage: &session.UsageEvent{Used: 10, Completion: 5}}
		Expect(st.ReleaseTurn(ctx, "1", "c", "r1", store.TurnDone)).To(Succeed())
		ev <- session.Event{Kind: session.KindTurnEnd}
		var end TurnEnd
		Eventually(done, 5*time.Second).Should(Receive(&end))
		Expect(end.How).To(Equal(TurnEnded))
		Expect(end.Info.Last).To(Equal(store.TurnDone))
		Expect(end.Tokens).To(Equal(15))
	})

	It("ends the watch for an interrupted turn that nothing will resume", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "c", Principal: "1", Model: "m", Created: time.Now(), Updated: time.Now()})).To(Succeed())
		s := &Service{d: Deps{Store: st}} // AutoResume is off
		Expect(st.ClaimTurn(ctx, "1", "c", "r0")).To(Succeed())
		Expect(st.MarkInterrupted(ctx, "1", "c", "r0", 0)).To(Succeed())
		ev := make(chan session.Event, 4)
		ev <- session.Event{Kind: session.KindTurnStart}
		ev <- session.Event{Kind: session.KindTurnEnd}
		end := s.awaitTurn("1", "c", ev, 5*time.Second, nil)
		Expect(end.How).To(Equal(TurnEnded))
		Expect(end.Info.Last).To(Equal(store.TurnInterrupted))
	})
})
