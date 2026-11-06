package studio

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func delta(turn int, s string) session.Event {
	return session.Event{Kind: session.KindText, Turn: turn, Text: s}
}

func texts(evs []session.Event) string {
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(e.Text)
	}
	return b.String()
}

var _ = Describe("eventRing", func() {
	It("numbers the events of a run from 1", func() {
		r := newEventRing("run1", 10, 1<<20)
		Expect(r.Add(session.Event{Kind: session.KindTurnStart}).ID).To(Equal("run1.1"))
		Expect(r.Add(delta(1, "a")).ID).To(Equal("run1.2"))
		Expect(r.Head()).To(Equal("run1.2"))
	})

	It("parses ids whose run holds dots, and refuses bad ones", func() {
		run, n, ok := parseEventID("a.b.12")
		Expect([]any{run, n, ok}).To(Equal([]any{"a.b", 12, true}))
		for _, bad := range []string{"", "12", ".3", "run.", "run.x", "run.0", "run.-1"} {
			_, _, ok := parseEventID(bad)
			Expect(ok).To(BeFalse(), bad)
		}
	})

	Describe("merge", func() {
		It("joins consecutive deltas of one turn and kind into one entry", func() {
			r := newEventRing("r", 10, 1<<20)
			r.Add(delta(1, "Hel"))
			r.Add(delta(1, "lo "))
			r.Add(delta(1, "world"))
			Expect(r.entries).To(HaveLen(1))
			_, ok := r.After("r.0")
			Expect(ok).To(BeFalse(), "0 is not an id")
			got, ok := r.After("r.1")
			Expect(ok).To(BeTrue())
			Expect(texts(got)).To(Equal("lo world"))
			Expect(got[0].ID).To(Equal("r.3"))
		})

		It("serves only the deltas after the one the client saw", func() {
			r := newEventRing("r", 10, 1<<20)
			r.Add(session.Event{Kind: session.KindTurnStart, Turn: 1})
			for _, s := range []string{"ab", "cd", "ef"} {
				r.Add(delta(1, s))
			}
			got, ok := r.After("r.3") // seen: turn_start, ab, cd
			Expect(ok).To(BeTrue())
			Expect(got).To(HaveLen(1))
			Expect(got[0].Text).To(Equal("ef"))
			Expect(got[0].ID).To(Equal("r.4"))
			got, _ = r.After("r.4")
			Expect(got).To(BeEmpty())
		})

		It("does not join text with reasoning, another turn, or a tool event", func() {
			r := newEventRing("r", 10, 1<<20)
			r.Add(delta(1, "a"))
			r.Add(session.Event{Kind: session.KindReasoning, Turn: 1, Text: "b"})
			r.Add(delta(2, "c"))
			r.Add(session.Event{Kind: session.KindToolStart, Turn: 2, Tool: &session.ToolEvent{Name: "x"}})
			r.Add(delta(2, "d"))
			Expect(r.entries).To(HaveLen(5))
		})

		It("lets a long answer fit in a small count", func() {
			r := newEventRing("r", 4, 1<<20)
			for i := 0; i < 500; i++ {
				r.Add(delta(1, "x"))
			}
			_, ok := r.After("r.0")
			Expect(ok).To(BeFalse())
			got, ok := r.After("r.100")
			Expect(ok).To(BeTrue())
			Expect(texts(got)).To(HaveLen(400))
		})
	})

	Describe("cap and watermark", func() {
		It("evicts the oldest past the count and refuses an id behind the watermark", func() {
			r := newEventRing("r", 3, 1<<20)
			for i := 1; i <= 5; i++ {
				r.Add(session.Event{Kind: session.KindFiles})
			}
			Expect(r.entries).To(HaveLen(3))
			Expect(r.dropped).To(Equal(2))
			_, ok := r.After("r.1")
			Expect(ok).To(BeFalse(), "event 2 is gone")
			got, ok := r.After("r.2")
			Expect(ok).To(BeTrue())
			Expect(got).To(HaveLen(3))
			Expect(got[0].ID).To(Equal("r.3"))
		})

		It("evicts the oldest past the byte cap", func() {
			r := newEventRing("r", 100, 1000)
			for i := 0; i < 5; i++ {
				r.Add(session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{Name: "t", Output: strings.Repeat("o", 400)}})
			}
			Expect(len(r.entries)).To(BeNumerically("<", 5))
			Expect(r.bytes).To(BeNumerically("<=", 1000))
			Expect(r.dropped).To(BeNumerically(">", 0))
		})

		It("keeps the newest event even when it is bigger than the cap", func() {
			r := newEventRing("r", 100, 100)
			r.Add(session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{Output: strings.Repeat("o", 500)}})
			Expect(r.entries).To(HaveLen(1))
			_, ok := r.After("r.0x")
			Expect(ok).To(BeFalse())
		})
	})

	Describe("After", func() {
		It("is empty and ok at the head", func() {
			r := newEventRing("r", 10, 1<<20)
			r.Add(delta(1, "a"))
			got, ok := r.After("r.1")
			Expect(ok).To(BeTrue())
			Expect(got).To(BeEmpty())
		})
		It("refuses another run, an id from the future and a bad id", func() {
			r := newEventRing("r", 10, 1<<20)
			r.Add(delta(1, "a"))
			for _, id := range []string{"other.1", "r.2", "r.99", "junk", ""} {
				_, ok := r.After(id)
				Expect(ok).To(BeFalse(), fmt.Sprint(id))
			}
		})
	})
})
