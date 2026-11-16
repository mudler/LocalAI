// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Workbook execution", func() {
	var w Workspace
	var branch string
	var now time.Time
	BeforeEach(func() {
		now = time.Now()
		g := DefaultGraph()
		g.Primary = "image"
		g.Nodes[0].Input.Value.Text = "tower"
		g.Nodes[1].Model, g.Nodes[2].Model = "reviewer", "painter"
		g.Nodes[2].Pause = false
		w = New("test", g, now)
		branch = w.Branches[0].ID
	})
	finish := func(id, node, typ string) {
		b, err := w.Branch(id)
		Expect(err).NotTo(HaveOccurred())

		a := b.Current().Nodes[node].Attempt
		Expect(a).NotTo(BeNil())
		Expect(w.Complete(id, a.ID, []Value{{Type: ValueType(typ), Text: "accepted", Artifact: "image"}}, "", nil, nil, now)).To(Succeed())
	}
	It("materializes source data and only runs the requested processing node", func() {
		id, err := w.RunNode(branch, "review", now)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(branch))

		s := w.Branches[0].Current()
		Expect(s.Nodes["source"].Status()).To(Equal(Succeeded))
		Expect(s.Nodes["review"].Attempt.Arguments.Input.Text).To(Equal("tower"))
		Expect(s.Nodes["image"].Status()).To(Equal(Stale))
		Expect(s.RunAll).To(BeFalse())
	})

	It("rejects stale processing input, source execution and inactive nodes", func() {
		_, err := w.RunNode(branch, "image", now)
		Expect(err).To(MatchError(ContainSubstring("input is stale")))

		_, err = w.RunNode(branch, "source", now)
		Expect(err).To(HaveOccurred())

		_, err = w.RunNode(branch, "mesh", now)
		Expect(err).To(HaveOccurred())
	})

	It("forks a rerun but executes already stale nodes without a redundant branch", func() {
		_, err := w.RunNode(branch, "review", now)
		Expect(err).NotTo(HaveOccurred())

		finish(branch, "review", "text")
		original := w.Branches[0].Current().Nodes["review"].Attempt.ID
		id, err := w.RunNode(branch, "review", now)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(Equal(branch))
		Expect(w.Branches[0].Current().Nodes["review"].Attempt.ID).To(Equal(original))

		finish(id, "review", "text")
		b, _ := w.Branch(id)
		g := b.Current().Graph
		g.Nodes[1].Model = "new-reviewer"
		Expect(w.Configure(id, g, now)).To(Succeed())

		same, err := w.RunNode(id, "review", now)
		Expect(err).NotTo(HaveOccurred())
		Expect(same).To(Equal(id))
		Expect(w.Branches).To(HaveLen(2))
	})

	It("continues a paused Run all without rerunning predecessors and restarts all explicitly", func() {
		id, err := w.RunAll(branch, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(Equal(branch))

		finish(id, "review", "text")
		Expect(w.Continue(id, now)).NotTo(Succeed())
		Expect(w.Select(id, "review", 0, nil, now)).To(Succeed())

		b, _ := w.Branch(id)
		review := b.Current().Nodes["review"].Attempt.ID
		Expect(b.Current().RunAll).To(BeTrue())
		Expect(w.Continue(id, now)).To(Succeed())
		Expect(b.Current().Nodes["review"].Attempt.ID).To(Equal(review))

		finish(id, "image", "image")
		Expect(b.Current().RunAll).To(BeFalse())

		next, err := w.RunAll(id, now)
		Expect(err).NotTo(HaveOccurred())

		b, _ = w.Branch(next)
		Expect(b.Current().Nodes["review"].Attempt.ID).NotTo(Equal(review))
		Expect(b.Current().Nodes["image"].Status()).To(Equal(Stale))
		Expect(b.Current().Nodes["mesh"].Attempt).To(BeNil())
	})
})
