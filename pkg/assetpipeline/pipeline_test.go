// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPipeline(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Asset pipeline")
}

var _ = Describe("Recorded pipeline", func() {
	var w Workspace
	var branch string
	var now time.Time
	BeforeEach(func() {
		now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
		g := DefaultGraph()
		g.Nodes[0].Input.Value.Text = "A stone tower"
		for i := 1; i < len(g.Nodes); i++ {
			g.Nodes[i].Model = "test-model"
		}

		w = New("Tower", g, now)
		branch = w.Branches[0].ID
	})
	completeSource := func() {
		a, err := w.Begin(branch, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Complete(branch, a.ID, []Value{a.Arguments.Input}, "", nil, nil, now)).To(Succeed())
	}
	It("runs only the primary output's ancestors and refuses cycles", func() {
		g := w.Branches[0].Current().Graph
		g.Primary = "image"
		g.Nodes[2].Input = Binding{Value: &Value{Type: "text", Text: "Direct description"}}
		order, err := g.Order()
		Expect(err).NotTo(HaveOccurred())
		Expect(order).To(Equal([]string{"image"}))

		g.Nodes[1].Input = Binding{From: "review"}
		Expect(g.Validate()).NotTo(Succeed())
	})

	It("records resolved inputs and pauses until approval without regenerating", func() {
		completeSource()
		a, err := w.Begin(branch, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Arguments.Input.Text).To(Equal("A stone tower"))

		checkpoint := w.Branches[0].Events()[len(w.Branches[0].Events())-1].ID
		Expect(w.Progress(branch, a.ID, "job-1", "Reviewing composition", now)).To(Succeed())
		Expect(w.Complete(branch, a.ID, []Value{{Type: "text", Text: "A centered tower on black"}}, "Remove the scenery", nil, nil, now)).To(Succeed())

		id, err := w.Branches[0].Current().Next()
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(BeEmpty())

		edit := "A centered stone tower on black"
		Expect(w.Select(branch, "review", 0, &edit, now)).To(Succeed())

		next, err := w.Begin(branch, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Arguments.Input.Text).To(Equal(edit))

		historical, err := w.Branches[0].Replay(checkpoint)
		Expect(err).NotTo(HaveOccurred())
		Expect(historical.Nodes["review"].Attempt.Arguments.Input.Text).To(Equal("A stone tower"))
	})

	It("forks retries while preserving the original outputs and configuration", func() {
		completeSource()
		before := w.Branches[0].Current()
		fork, err := w.Retry(branch, "source", now)
		Expect(err).NotTo(HaveOccurred())
		Expect(fork).NotTo(Equal(branch))
		Expect(w.Branches[0].Current()).To(Equal(before))
		Expect(w.Branches[1].Current().Nodes["source"].Status()).To(Equal(Stale))
		Expect(w.Branches[1].Parent).To(BeIdenticalTo(w.Branches[0]))
	})

	It("invalidates dependencies and retains unrelated successful outputs", func() {
		completeSource()
		g := w.Branches[0].Current().Graph
		g.Nodes[2].Input = Binding{Value: &Value{Type: "text", Text: "Independent image"}}
		Expect(w.Configure(branch, g, now)).To(Succeed())
		Expect(w.Branches[0].Current().Nodes["source"].Status()).To(Equal(Succeeded))

		g.Nodes[0].Input.Value.Text = "Another tower"
		Expect(w.Configure(branch, g, now)).To(Succeed())
		Expect(w.Branches[0].Current().Nodes["source"].Status()).To(Equal(Stale))
		Expect(g.Descendants("source")).NotTo(HaveKey("image"))
	})

	It("warns about changed advice without invalidating an accepted prompt", func() {
		completeSource()
		a, err := w.Begin(branch, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Complete(branch, a.ID, []Value{{Type: "text", Text: "Tower"}}, "", nil, nil, now)).To(Succeed())
		Expect(w.Select(branch, "review", 0, nil, now)).To(Succeed())

		g := w.Branches[0].Current().Graph
		g.Target.Advice = "Use a white background"
		g.Nodes[4].Model = "another-engine"
		Expect(w.Configure(branch, g, now)).To(Succeed())
		Expect(w.Branches[0].Current().Nodes["review"].Status()).To(Equal(Succeeded))

		events := w.Branches[0].Events()
		Expect(events[len(events)-1].Kind).To(Equal(WarningKind))

		g.Target.Inputs = []string{"text"}
		Expect(w.Configure(branch, g, now)).To(Succeed())

		_, err = w.Begin(branch, now)
		Expect(err).To(MatchError(ContainSubstring("incompatible")))
	})

	It("records failure diagnostics and rejects late completions", func() {
		a, err := w.Begin(branch, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Complete(branch, a.ID, nil, "", []string{"backend unavailable"}, fmt.Errorf("load failed"), now)).To(Succeed())
		Expect(w.Branches[0].Current().Nodes["source"].Attempt.Diagnostics).To(Equal([]string{"backend unavailable"}))
		Expect(w.Complete(branch, a.ID, []Value{{Type: "text", Text: "late"}}, "", nil, nil, now)).NotTo(Succeed())

		_, err = w.Begin(branch, now)
		Expect(err).To(MatchError(ContainSubstring("Retry")))
	})

	It("turns malformed backend output into a terminal inspectable failure", func() {
		a, err := w.Begin(branch, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Complete(branch, a.ID, nil, "", nil, nil, now)).To(Succeed())

		state := w.Branches[0].Current().Nodes["source"]
		Expect(state.Status()).To(Equal(Failed))
		Expect(state.Attempt.Failure.Error()).To(ContainSubstring("no output"))

		fork, err := w.Retry(branch, "source", now)
		Expect(err).NotTo(HaveOccurred())

		_, err = w.Begin(fork, now)
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not transfer live jobs when rewinding a running checkpoint", func() {
		_, err := w.Begin(branch, now)
		Expect(err).NotTo(HaveOccurred())

		checkpoint := w.Branches[0].Events()[len(w.Branches[0].Events())-1].ID
		_, err = w.Fork(branch, checkpoint, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Branches[0].Current().Busy()).To(BeTrue())
		Expect(w.Branches[1].Current().Busy()).To(BeFalse())
		Expect(w.Branches[1].Current().Nodes["source"].Status()).To(Equal(Stale))
		Expect(w.Branches[1].Current().Nodes["source"].Attempt.Status).To(Equal(Running))
	})

	It("requires an explicit choice for multiple images", func() {
		g := DefaultGraph()
		g.Nodes[2].Count = 4
		g.Nodes[2].Pause = false
		Expect(g.Validate()).NotTo(Succeed())
	})
})

var _ = Describe("Owner persistence", func() {
	It("survives restart and reconciles uncertain writes without reapplying", func() {
		dir := GinkgoT().TempDir()
		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())

		ctx := context.Background()
		calls := 0
		create := func(w *Workspace) error { calls++; *w = New("Tower", DefaultGraph(), time.Now()); return nil }
		first, err := s.Update(ctx, "resource", "operation", 0, "create", create)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		s, err = Open(dir)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		replayed, err := s.Update(ctx, "resource", "operation", 0, "create", create)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed).To(Equal(first))
		Expect(calls).To(Equal(1))

		_, err = s.Update(ctx, "resource", "operation", 0, "different", create)
		Expect(err).To(MatchError(ErrConflict))

		_, err = s.Update(ctx, "resource", "other", 0, "create", create)
		Expect(err).To(MatchError(ErrConflict))

		got, err := s.Read(ctx, "resource")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(first))
	})

	It("detects conflicting writes from two store connections", func() {
		dir := GinkgoT().TempDir()
		a, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(a.Close)
		b, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(b.Close)
		ctx := context.Background()
		w, err := a.Update(ctx, "r", "create", 0, "create", func(w *Workspace) error { *w = New("one", DefaultGraph(), time.Now()); return nil })
		Expect(err).NotTo(HaveOccurred())

		_, err = b.Update(ctx, "r", "edit", w.Revision, "edit", func(w *Workspace) error { w.Name = "two"; return nil })
		Expect(err).NotTo(HaveOccurred())

		_, err = a.Update(ctx, "r", "stale", w.Revision, "edit", func(w *Workspace) error { w.Name = "lost"; return nil })
		Expect(err).To(MatchError(ErrConflict))

		got, err := a.Read(ctx, "r")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("two"))
	})
})
