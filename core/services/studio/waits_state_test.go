package studio

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mudler/nib/watch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

var _ = Describe("the state a wait keeps for the person", func() {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	newWatch := func(mode string) *loops.Watch {
		return &loops.Watch{Tool: "list_downloads", Mode: mode, Pattern: "ready", Deadline: now.Add(time.Hour), MaxErrors: 3}
	}

	It("records an unchanged check", func() {
		w := newWatch(loops.UntilMatch)
		v, _ := step(w, watch.Result{Output: "still busy"}, now)
		Expect(v.Done).To(BeFalse())
		Expect(w.Outcome).To(Equal(loops.OutcomeUnchanged))
		Expect(w.LastCheck).To(Equal(now))
		Expect(w.End).To(BeEmpty())
	})

	It("records a failed check, and the end when the source keeps failing", func() {
		w := newWatch(loops.UntilMatch)
		for i := 0; i < 2; i++ {
			step(w, watch.Result{Err: errors.New("boom")}, now)
			Expect(w.Outcome).To(Equal(loops.OutcomeError))
			Expect(w.End).To(BeEmpty())
		}
		v, _ := step(w, watch.Result{Err: errors.New("boom")}, now)
		Expect(v.Done).To(BeTrue())
		Expect(w.Errors).To(Equal(3))
		Expect(w.End).To(Equal(loops.EndSourceFailing))
	})

	It("ends as met when the condition holds", func() {
		w := newWatch(loops.UntilMatch)
		v, _ := step(w, watch.Result{Output: "all ready"}, now)
		Expect(v.Done).To(BeTrue())
		Expect(w.Outcome).To(Equal(loops.OutcomeChanged))
		Expect(w.End).To(Equal(loops.EndMet))
	})

	Describe("a json_equals wait on a field that may not exist yet", func() {
		newJSON := func() *loops.Watch {
			w := newWatch(loops.UntilJSONEquals)
			w.Field, w.Equals = "downloads.0.status", "completed"
			return w
		}

		It("keeps polling while the list is empty, then is met when the item appears", func() {
			w := newJSON()
			for range 5 {
				v, _ := step(w, watch.Result{Output: `{"downloads":[]}`}, now)
				Expect(v.Done).To(BeFalse())
				Expect(w.Errors).To(Equal(0))
				Expect(w.Outcome).To(Equal(loops.OutcomeUnchanged))
			}
			v, _ := step(w, watch.Result{Output: `{"downloads":[{"status":"completed"}]}`}, now)
			Expect(v.Done).To(BeTrue())
			Expect(w.End).To(Equal(loops.EndMet))
		})

		It("does not match an empty target against an absent field", func() {
			w := newJSON()
			w.Equals = ""
			v, _ := step(w, watch.Result{Output: `{"downloads":[]}`}, now)
			Expect(v.Done).To(BeFalse())
		})

		It("times out at the deadline when the field never appears, and says so", func() {
			w := newJSON()
			for range 5 {
				step(w, watch.Result{Output: `{"downloads":[]}`}, now)
			}
			v, _ := step(w, watch.Result{Output: `{"downloads":[]}`}, now.Add(2*time.Hour))
			Expect(v.Done).To(BeTrue())
			Expect(w.End).To(Equal(loops.EndTimedOut))
			Expect(v.Detail).To(ContainSubstring("the field downloads.0.status was never present"))
			Expect(v.Detail).To(ContainSubstring("last output"))
		})

		It("is source failing when a field that was seen is absent for max_errors checks", func() {
			w := newJSON()
			step(w, watch.Result{Output: `{"downloads":[{"status":"running"}]}`}, now)
			Expect(w.FieldSeen).To(BeTrue())
			for range 2 {
				v, _ := step(w, watch.Result{Output: `{"downloads":[]}`}, now)
				Expect(v.Done).To(BeFalse())
				Expect(w.Outcome).To(Equal(loops.OutcomeError))
			}
			v, _ := step(w, watch.Result{Output: `{"downloads":[]}`}, now)
			Expect(v.Done).To(BeTrue())
			Expect(w.End).To(Equal(loops.EndSourceFailing))
		})

		It("is source failing on output that is not JSON, even if the field was never seen", func() {
			w := newJSON()
			for range 2 {
				v, _ := step(w, watch.Result{Output: "<html>"}, now)
				Expect(v.Done).To(BeFalse())
			}
			v, _ := step(w, watch.Result{Output: "<html>"}, now)
			Expect(v.Done).To(BeTrue())
			Expect(w.FieldSeen).To(BeFalse())
			Expect(w.End).To(Equal(loops.EndSourceFailing))
		})

		It("loads a stored wait that has no field_seen as not seen", func() {
			old := `{"tool":"list_downloads","mode":"json_equals","field":"a.0","equals":"x","deadline":"2026-11-06T11:00:00Z","max_errors":3,"checks":2,"errors":0}`
			var w loops.Watch
			Expect(json.Unmarshal([]byte(old), &w)).To(Succeed())
			Expect(w.FieldSeen).To(BeFalse())
			v, _ := step(&w, watch.Result{Output: `{"a":[]}`}, now)
			Expect(v.Done).To(BeFalse())
			raw, err := json.Marshal(&w)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring("field_seen"))
			w.FieldSeen = true
			raw, _ = json.Marshal(&w)
			Expect(string(raw)).To(ContainSubstring(`"field_seen":true`))
		})
	})

	It("ends as timed out at the deadline, without calling it a change", func() {
		w := newWatch(loops.UntilMatch)
		step(w, watch.Result{Output: "busy"}, now.Add(2*time.Hour))
		Expect(w.End).To(Equal(loops.EndTimedOut))
		Expect(w.Outcome).To(Equal(loops.OutcomeUnchanged))
	})

	It("shows the loop that keeps a sub-agent running", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		ls, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := NewLoops(svc, LoopOptions{Store: ls})
		Expect(err).NotTo(HaveOccurred())
		svc.SetLoops(host)
		DeferCleanup(host.Close)
		ctx := context.Background()
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "mom", Principal: "alice"})).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "kid", Principal: "alice", Kind: session.KindSubAgent, ParentID: "mom"})).To(Succeed())
		Expect(st.ClaimTurn(ctx, "alice", "kid", "run-1")).To(Succeed())
		Expect(st.ReleaseTurn(ctx, "alice", "kid", "run-1", store.TurnDone)).To(Succeed())
		Expect(ls.Add(ctx, loops.Loop{ID: "l1", Owner: "alice", ChatID: "kid", Prompt: "check", State: loops.StateActive,
			Next: now.Add(time.Hour), Scheduled: true, Created: now, Updated: now}, loops.Limits{})).To(Succeed())

		runs, err := svc.SubAgents(ctx, "alice", "mom")
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))
		Expect(runs[0].Turn.Running).To(BeTrue())
		Expect(runs[0].Loop).NotTo(BeNil())
		Expect(runs[0].Loop.ID).To(Equal("l1"))
	})
})
