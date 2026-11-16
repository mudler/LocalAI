// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Recorded multi-input asset stages", func() {
	var g Graph
	BeforeEach(func() {
		g = Graph{Primary: "apply", Nodes: []NodeConfig{
			{
				ID:    "rig",
				Kind:  Rig,
				Model: "skin-tokens",
				Input: Binding{Value: &Value{Type: "mesh", Artifact: "character"}},
			},
			{ID: "prompt", Kind: Source, Input: Binding{Value: &Value{Type: "text", Text: "Wave"}}},
			{ID: "motion", Kind: Motion, Model: "kimodo", Input: Binding{From: "prompt"}},
			{ID: "unused", Kind: Motion, Model: "kimodo", Input: Binding{From: "prompt"}},
			{
				ID:     "apply",
				Kind:   Retarget,
				Model:  "skin-tokens",
				Input:  Binding{From: "rig"},
				Inputs: map[string]Binding{"motion": {From: "motion"}},
			},
		}}
	})

	It("orders both branches once and excludes unrelated processing", func() {
		order, err := g.Order()
		Expect(err).NotTo(HaveOccurred())
		Expect(order).To(Equal([]string{"rig", "prompt", "motion", "apply"}))
		Expect(g.Descendants("rig")).To(Equal(map[string]bool{"rig": true, "apply": true}))
		Expect(g.Descendants("motion")).To(Equal(map[string]bool{"motion": true, "apply": true}))
	})

	It("rejects missing, unknown, future and incompatible named inputs", func() {
		g.Nodes[4].Inputs = nil
		Expect(g.Validate()).NotTo(Succeed())

		g.Nodes[4].Inputs = map[string]Binding{"unknown": {From: "motion"}}
		Expect(g.Validate()).NotTo(Succeed())

		g.Nodes[4].Inputs = map[string]Binding{"motion": {From: "apply"}}
		Expect(g.Validate()).NotTo(Succeed())

		g.Nodes[4].Inputs = map[string]Binding{"motion": {From: "rig"}}
		Expect(g.Validate()).NotTo(Succeed())

		g.Nodes[4].Inputs = map[string]Binding{"motion": {From: "motion", Value: &Value{Type: "motion", Artifact: "clip"}}}
		Expect(g.Validate()).NotTo(Succeed())
	})

	It("records both accepted inputs, blocks stale joins and preserves history when either branch changes", func() {
		now := time.Now()
		w := New("joined", g, now)
		branch := w.Branches[0].ID
		_, err := w.RunNode(branch, "apply", now)
		Expect(err).To(HaveOccurred())

		for _, stage := range []string{"rig", "motion"} {
			_, err = w.RunNode(branch, stage, now)
			Expect(err).NotTo(HaveOccurred())

			a := w.Branches[0].Current().Nodes[stage].Attempt
			typ := "mesh"
			if stage == "motion" {
				typ = "motion"
			}
			Expect(w.Complete(branch, a.ID, []Value{{Type: ValueType(typ), Artifact: stage + "-output"}}, "", nil, nil, now)).To(Succeed())
		}

		_, err = w.RunNode(branch, "apply", now)
		Expect(err).NotTo(HaveOccurred())

		a := w.Branches[0].Current().Nodes["apply"].Attempt
		Expect(a.Arguments.Input.Artifact).To(Equal("rig-output"))
		Expect(a.Arguments.Inputs["motion"].Artifact).To(Equal("motion-output"))
		Expect(w.Complete(branch, a.ID, []Value{{Type: "mesh", Artifact: "animated"}}, "", nil, nil, now)).To(Succeed())

		g.Nodes[4].Inputs["motion"] = Binding{Value: &Value{Type: "motion", Artifact: "other-clip"}}
		Expect(w.Configure(branch, g, now)).To(Succeed())

		s := w.Branches[0].Current()
		Expect(s.Nodes["apply"].Status()).To(Equal(Stale))
		Expect(s.Nodes["rig"].Status()).To(Equal(Succeeded))
		Expect(s.Nodes["motion"].Status()).To(Equal(Succeeded))
		Expect(a.Arguments.Inputs["motion"].Artifact).To(Equal("motion-output"))
		Expect(w.Branches[0].Events()[len(w.Branches[0].Events())-1].Message).To(ContainSubstring("motion input"))
	})

	It("refuses a direct mesh or motion input without an artifact", func() {
		g.Nodes[4].Input = Binding{Value: &Value{Type: "mesh", Artifact: "rig"}}
		g.Nodes[4].Inputs["motion"] = Binding{Value: &Value{Type: "motion"}}
		w := New("missing", g, time.Now())
		_, err := w.RunNode(w.Branches[0].ID, "apply", time.Now())
		Expect(err).To(MatchError(ContainSubstring("motion is missing")))
	})
})
