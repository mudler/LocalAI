package studio_test

import (
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const offerLine = "Offer allowed this turn"

func newHintChat(on bool) (*studio.Service, *studiotest.ToolHarness, *files.Service) {
	f := newFiles()
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	svc, h := newToolService(func(_ workspace.Actor, _ string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) {
		d.Files = f
		d.Clock = func() time.Time { return now }
		d.SuggestionsDefault = func() bool { return on }
	})
	// LocalAI met alice a day ago: the quiet start is over.
	writeHome(f, alice, "/LocalAI/user.json", `{"seen":{"since":"2026-10-29T09:00:00Z"}}`)
	return svc, h, f
}

func turnWith(svc *studio.Service, id, text string) {
	sent(svc, alice, id, text)
	idle(svc, alice, id)
}

var _ = Describe("offers in the turn note", func() {
	It("tells the model in the third turn of a chat, once, and nothing in the other turns", func() {
		svc, h, _ := newHintChat(true)
		id := create(svc, alice)
		turnWith(svc, id, "hi")
		turnWith(svc, id, "how are you")
		turnWith(svc, id, "thanks, I am a designer")
		turnWith(svc, id, "bye")
		got := h.Sent(id)
		Expect(got).To(HaveLen(4))
		Expect(got[0]).NotTo(ContainSubstring(offerLine))
		Expect(got[1]).NotTo(ContainSubstring(offerLine))
		Expect(got[2]).To(ContainSubstring(offerLine))
		Expect(got[3]).NotTo(ContainSubstring(offerLine), "one a chat")
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring(offerLine), "the prompt stays the same, so the harness does not restart")

		By("allowing none in another chat the same day")
		other := create(svc, alice)
		for _, m := range []string{"a", "b", "c"} {
			turnWith(svc, other, m)
		}
		for _, s := range h.Sent(other) {
			Expect(s).NotTo(ContainSubstring(offerLine))
		}
	})

	It("adds nothing when the server's default is off and the user did not choose", func() {
		svc, h, _ := newHintChat(false)
		id := create(svc, alice)
		for _, m := range []string{"a", "b", "c"} {
			turnWith(svc, id, m)
		}
		for _, s := range h.Sent(id) {
			Expect(strings.Contains(s, offerLine)).To(BeFalse())
		}
	})

	It("adds nothing after the assistant refused something in the chat", func() {
		f := newFiles()
		now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "do the bad thing" {
				return []studiotest.ToolStep{{Say: "I can't help with that."}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = f; d.Clock = func() time.Time { return now } })
		writeHome(f, alice, "/LocalAI/user.json", `{"seen":{"since":"2026-10-29T09:00:00Z"}}`)
		id := create(svc, alice)
		for _, m := range []string{"do the bad thing", "b", "c"} {
			turnWith(svc, id, m)
		}
		for _, s := range h.Sent(id) {
			Expect(s).NotTo(ContainSubstring(offerLine))
		}
	})
})
