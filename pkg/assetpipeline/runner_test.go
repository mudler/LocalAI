// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"context"
	"sync/atomic"
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type mockExecutor struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (m *mockExecutor) Execute(ctx context.Context, _, _ string, a Arguments, progress func(string)) (Result, error) {
	m.calls.Add(1)
	progress("Inference is running")
	select {
	case m.entered <- struct{}{}:
	default:
	}
	select {
	case <-m.release:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	return Result{Outputs: []Value{a.Input}}, nil
}

var _ = Describe("Durable execution runner", func() {
	It("runs every active processing stage again on each Run all", func() {
		ctx := context.Background()
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		adapter := &mockExecutor{release: make(chan struct{})}
		close(adapter.release)
		r, err := NewRunner(ctx, s, adapter)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		g := Graph{Primary: "second", Nodes: []NodeConfig{
			{ID: "source", Kind: Source, Input: Binding{Value: &Value{Type: "text", Text: "tower"}}},
			{ID: "first", Kind: Review, Model: "reviewer", Input: Binding{From: "source"}},
			{ID: "second", Kind: Review, Model: "reviewer", Input: Binding{From: "first"}},
			{ID: "inactive", Kind: Review, Model: "reviewer", Input: Binding{From: "source"}},
		}}
		w, err := r.Apply(ctx, "alice", "continuous", "create", 0, Command{Action: "create", Graph: &g})
		Expect(err).NotTo(HaveOccurred())

		branch := w.Branches[0].ID
		for i, operation := range []string{"first-all", "second-all"} {
			Eventually(func() error {
				latest, err := r.Read(ctx, "continuous")
				if err != nil {
					return err
				}

				w, err = r.Apply(ctx, "alice", "continuous", operation, latest.Revision, Command{Action: "run-all", Branch: branch})
				return err
			}).Should(Succeed())
			branch = w.ResultBranch
			Eventually(func() Status {
				got, _ := r.Read(ctx, "continuous")
				b, _ := got.Branch(branch)
				return b.Current().Nodes["second"].Status()
			}).Should(Equal(Succeeded))
			Expect(adapter.calls.Load()).To(Equal(int32((i + 1) * 2)))

			got, err := r.Read(ctx, "continuous")
			Expect(err).NotTo(HaveOccurred())

			b, _ := got.Branch(branch)
			Expect(b.Current().RunAll).To(BeFalse())
			Expect(b.Current().Nodes["inactive"].Attempt).To(BeNil())
		}
	})
	DescribeTable("records workbook commands atomically and never replays inference", func(action string) {
		ctx := context.Background()
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		adapter := &mockExecutor{entered: make(chan struct{}, 1), release: make(chan struct{})}
		r, err := NewRunner(ctx, s, adapter)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		g := DefaultGraph()
		g.Primary = "review"
		g.Nodes[0].Input.Value.Text = "tower"
		g.Nodes[1].Model, g.Nodes[1].Pause = "reviewer", false
		w, err := r.Apply(ctx, "alice", "workbook", "create", 0, Command{Action: "create", Graph: &g})
		Expect(err).NotTo(HaveOccurred())

		cmd := Command{Action: CommandAction(action), Branch: w.Branches[0].ID, Node: "review"}
		started, err := r.Apply(ctx, "alice", "workbook", "run", w.Revision, cmd)
		Expect(err).NotTo(HaveOccurred())

		branch := started.ResultBranch
		b, err := started.Branch(branch)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.Current().Nodes["source"].Status()).To(Equal(Succeeded))
		Eventually(adapter.entered).Should(Receive())
		_, err = r.Apply(ctx, "alice", "workbook", "run", w.Revision, cmd)
		Expect(err).NotTo(HaveOccurred())

		close(adapter.release)
		Eventually(func() Status {
			got, _ := r.Read(ctx, "workbook")
			b, _ := got.Branch(branch)
			return b.Current().Nodes["review"].Status()
		}).Should(Equal(Succeeded))
		_, err = r.Apply(ctx, "alice", "workbook", "run", w.Revision, cmd)
		Expect(err).NotTo(HaveOccurred())
		Consistently(func() int32 { return adapter.calls.Load() }, 100*time.Millisecond).Should(Equal(int32(1)))
	}, Entry("single node", "run-node"), Entry("run all", "run-all"))
	It("retains a cancelled Run all session and resolves it with a node rerun", func() {
		ctx := context.Background()
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		adapter := &mockExecutor{entered: make(chan struct{}, 1), release: make(chan struct{})}
		r, err := NewRunner(ctx, s, adapter)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		g := DefaultGraph()
		g.Primary = "review"
		g.Nodes[0].Input.Value.Text = "tower"
		g.Nodes[1].Model, g.Nodes[1].Pause = "reviewer", false
		w, err := r.Apply(ctx, "alice", "cancelled", "create", 0, Command{Action: "create", Graph: &g})
		Expect(err).NotTo(HaveOccurred())

		w, err = r.Apply(ctx, "alice", "cancelled", "all", w.Revision, Command{Action: "run-all", Branch: w.Branches[0].ID})
		Expect(err).NotTo(HaveOccurred())

		branch := w.ResultBranch
		Eventually(adapter.entered).Should(Receive())
		Eventually(func() error {
			latest, err := r.Read(ctx, "cancelled")
			if err != nil {
				return err
			}

			_, err = r.Apply(ctx, "alice", "cancelled", "cancel", latest.Revision, Command{Action: "cancel", Branch: branch})
			return err
		}).Should(Succeed())
		Eventually(func() Status {
			got, _ := r.Read(ctx, "cancelled")
			b, _ := got.Branch(branch)
			return b.Current().Nodes["review"].Status()
		}).Should(Equal(Failed))
		w, err = r.Read(ctx, "cancelled")
		Expect(err).NotTo(HaveOccurred())

		b, _ := w.Branch(branch)
		Expect(b.Current().RunAll).To(BeTrue())
		Expect(b.Current().Nodes["review"].Attempt.Status).To(Equal(Cancelled))

		_, err = r.Apply(ctx, "alice", "cancelled", "continue", w.Revision, Command{Action: "continue", Branch: branch})
		Expect(err).To(HaveOccurred())

		close(adapter.release)
		Eventually(func() error {
			latest, err := r.Read(ctx, "cancelled")
			if err != nil {
				return err
			}

			w, err = r.Apply(ctx, "alice", "cancelled", "rerun", latest.Revision, Command{Action: "run-node", Branch: branch, Node: "review"})
			return err
		}).Should(Succeed())
		Expect(w.ResultBranch).NotTo(Equal(branch))
		Eventually(func() bool {
			got, _ := r.Read(ctx, "cancelled")
			b, _ := got.Branch(w.ResultBranch)
			return !b.Current().RunAll && b.Current().Nodes["review"].Status() == Succeeded
		}).Should(BeTrue())
	})

	It("runs outside the submitting request and does not replay completed inference", func() {
		ctx := context.Background()
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		adapter := &mockExecutor{entered: make(chan struct{}, 1), release: make(chan struct{})}
		r, err := NewRunner(ctx, s, adapter)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		g := DefaultGraph()
		g.Primary = "review"
		g.Nodes[1].Input = Binding{Value: &Value{Type: "text", Text: "tower"}}
		g.Nodes[1].Pause = false
		g.Nodes[1].Model = "test-model"
		g.Nodes[0].Input.Value.Text = "tower"
		w, err := r.Apply(ctx, "alice", "resource", "create", 0, Command{Action: "create", Name: "tower", Graph: &g})
		Expect(err).NotTo(HaveOccurred())

		branch := w.Branches[0].ID
		submit, cancel := context.WithCancel(ctx)
		cmd := Command{Action: "run", Branch: branch}
		started, err := r.Apply(submit, "alice", "resource", "start", w.Revision, cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(started.Branches[0].Current().Busy()).To(BeTrue())

		cancel()
		Eventually(adapter.entered).Should(Receive())
		Eventually(func() string {
			got, _ := r.Read(ctx, "resource")
			return got.Branches[0].Current().Nodes["review"].Attempt.Progress
		}).Should(Equal("Inference is running"))
		close(adapter.release)
		Eventually(func() Status {
			got, _ := r.Read(ctx, "resource")
			return got.Branches[0].Current().Nodes["review"].Status()
		}).Should(Equal(Succeeded))
		_, err = r.Apply(ctx, "alice", "resource", "start", w.Revision, cmd)
		Expect(err).NotTo(HaveOccurred())
		Consistently(func() int32 { return adapter.calls.Load() }, 100*time.Millisecond).Should(Equal(int32(1)))
	})

	It("marks an uncertain attempt interrupted on restart without invoking the adapter", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())

		_, err = s.Update(ctx, "resource", "create", 0, "create", func(w *Workspace) error {
			g := DefaultGraph()
			g.Nodes[0].Input.Value.Text = "tower"
			*w = New("tower", g, time.Now())
			_, err := w.Begin(w.Branches[0].ID, time.Now())
			return err
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		s, err = Open(dir)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		adapter := &mockExecutor{}
		r, err := NewRunner(ctx, s, adapter)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		w, err := r.Read(ctx, "resource")
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Branches[0].Current().Nodes["source"].Status()).To(Equal(Interrupted))
		Expect(adapter.calls.Load()).To(BeZero())
	})
})
