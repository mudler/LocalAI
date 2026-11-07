package studio

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ownerWith is a Service with only the ring of the chat k, as the process that
// runs its turn holds it.
func ownerWith(k string, r *eventRing) *Service {
	return &Service{rings: map[string]*eventRing{k: r}}
}

var _ = Describe("the answer to a replay ask", func() {
	It("is a resync with no head when the process has no ring for the chat", func() {
		s := ownerWith("alice/other", newEventRing("run1", 10, 1<<20))
		Expect(s.answerReplay(ReplayRequest{Key: "alice/chat", After: "run1.1"})).To(Equal(ReplayReply{Resync: true}))
	})

	It("is a resync with the head when the id is older than the ring keeps", func() {
		r := newEventRing("run1", 2, 1<<20)
		for range 5 {
			r.Add(session.Event{Kind: session.KindToolStart})
		}
		rep := ownerWith("k", r).answerReplay(ReplayRequest{Key: "k", After: "run1.1"})
		Expect(rep.Resync).To(BeTrue())
		Expect(rep.Head).To(Equal("run1.5"))
	})

	It("comes in pages that fit a bus message, and a page says when more follow", func() {
		r := newEventRing("run1", ringMaxEvents, ringMaxBytes)
		big := strings.Repeat("x", 100<<10)
		for range 12 { // about 1.2 MB in all, so it takes three pages
			r.Add(session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{Name: "read", Output: big}})
		}
		s := ownerWith("k", r)
		after, pages, got := "run1.1", 0, 0
		for {
			rep := s.answerReplay(ReplayRequest{Key: "k", After: after})
			Expect(rep.Resync).To(BeFalse())
			Expect(rep.Events).ToNot(BeEmpty())
			pages++
			got += len(rep.Events)
			after = rep.Events[len(rep.Events)-1].ID
			if !rep.More {
				break
			}
		}
		Expect(pages).To(BeNumerically(">=", 3))
		Expect(got).To(Equal(11), "all but the one the client had")
		Expect(after).To(Equal("run1.12"))
	})

	It("is a resync when one event alone is larger than a reply", func() {
		r := newEventRing("run1", ringMaxEvents, ringMaxBytes)
		r.Add(session.Event{Kind: session.KindTurnStart})
		r.Add(session.Event{Kind: session.KindText, Turn: 1, Text: strings.Repeat("y", replayPageBytes+1)})
		rep := ownerWith("k", r).answerReplay(ReplayRequest{Key: "k", After: "run1.1"})
		Expect(rep.Resync).To(BeTrue())
		Expect(rep.Head).To(Equal("run1.2"))
	})
})
