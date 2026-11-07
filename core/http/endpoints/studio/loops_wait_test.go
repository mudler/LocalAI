package studio

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/loops"
)

var _ = Describe("a wait in the loops list", func() {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	base := func() loops.Loop {
		return loops.Loop{ID: "w1", Owner: "alice", ChatID: "c1", Prompt: "Wait for it", State: loops.StateActive,
			Interval: time.Minute, Next: now.Add(time.Minute), Created: now, Updated: now,
			Watch: &loops.Watch{Tool: "list_downloads", Args: json.RawMessage(`{"token":"s3cret"}`), Mode: loops.UntilChange,
				Deadline: now.Add(time.Hour), MaxErrors: 3, Note: "Waiting for the download to finish.",
				Checks: 4, LastCheck: now, Outcome: loops.OutcomeUnchanged}}
	}

	It("names the tool and never shows its arguments", func() {
		out := loopJSON(base())
		Expect(out.Kind).To(Equal("wait"))
		Expect(out.Watch).NotTo(BeNil())
		Expect(out.Watch.SourceKind).To(Equal("tool"))
		Expect(out.Watch.Source).To(Equal("list_downloads"))
		raw, err := json.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("s3cret"))
		Expect(out.Watch.Phase).To(Equal("waiting"))
		Expect(out.Watch.NextCheck).NotTo(BeNil())
		Expect(out.Watch.Condition).To(Equal("the output changes"))
		Expect(out.Watch.Reason).To(Equal("Waiting for the download to finish."))
		Expect(out.Watch.Checks).To(Equal(4))
		Expect(out.Watch.Outcome).To(Equal("unchanged"))
	})

	It("shows a URL as host and path, without scheme, user, query or fragment", func() {
		l := base()
		l.Watch.Tool = ""
		l.Watch.URL = "https://bob:pw@status.example.com/api/v1/job/?api_key=abc123#frag"
		out := loopJSON(l)
		Expect(out.Watch.SourceKind).To(Equal("url"))
		Expect(out.Watch.Source).To(Equal("status.example.com/api/v1/job"))
		raw, _ := json.Marshal(out)
		for _, leak := range []string{"abc123", "api_key", "pw", "bob", "frag"} {
			Expect(string(raw)).NotTo(ContainSubstring(leak))
		}
	})

	It("cuts URLs in the last error to host and path", func() {
		l := base()
		l.Watch.Errors, l.Watch.LastErr, l.Watch.Outcome = 2, `Get "https://status.example.com/api?token=abc123": connection refused`, loops.OutcomeError
		out := loopJSON(l)
		Expect(out.Watch.LastError).To(Equal(`Get "status.example.com/api": connection refused`))
		Expect(out.Watch.Errors).To(Equal(2))
		Expect(out.Watch.MaxErrors).To(Equal(3))
		Expect(out.Watch.Outcome).To(Equal("error"))
	})

	It("gives the phase and the way it ended", func() {
		l := base()
		l.Watch.Wake, l.Watch.End = "wake up", loops.EndTimedOut
		Expect(loopJSON(l).Watch.Phase).To(Equal("waking"))
		Expect(loopJSON(l).Watch.End).To(Equal("timed_out"))
		Expect(loopJSON(l).Watch.NextCheck).To(BeNil())

		l = base()
		l.State = loops.StatePaused
		Expect(loopJSON(l).Watch.Phase).To(Equal("paused"))

		for end, want := range map[string]string{loops.EndMet: "condition_met", loops.EndSourceFailing: "source_failing", loops.EndTimedOut: "timed_out", "": "stopped"} {
			l = base()
			l.State, l.Next, l.Watch.End = loops.StateEnded, time.Time{}, end
			got := loopJSON(l).Watch
			Expect(got.Phase).To(Equal("ended"))
			Expect(got.End).To(Equal(want))
			Expect(got.NextCheck).To(BeNil())
		}
	})

	It("words the conditions", func() {
		l := base()
		l.Watch.Mode, l.Watch.Pattern = loops.UntilMatch, "done"
		Expect(loopJSON(l).Watch.Condition).To(Equal(`the output matches "done"`))
		l.Watch.Mode, l.Watch.Field, l.Watch.Equals = loops.UntilJSONEquals, "status", "ok"
		Expect(loopJSON(l).Watch.Condition).To(Equal(`the field status equals "ok"`))
	})

	It("leaves a plain loop without a watch", func() {
		l := base()
		l.Watch = nil
		Expect(loopJSON(l).Watch).To(BeNil())
		Expect(loopJSON(l).Kind).To(BeEmpty())
	})
})
