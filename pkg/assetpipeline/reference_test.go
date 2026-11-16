// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("Image reference inputs", func() {
	var g Graph
	BeforeEach(func() {
		g = Graph{Primary: "edit", Nodes: []NodeConfig{
			{ID: "reference", Kind: Extract, Input: Binding{Value: &Value{Type: "image", Artifact: "original"}}, Params: map[string]string{"mode": "keep"}},
			{ID: "edit", Kind: Image, Model: "editor", Count: 1, Input: Binding{Value: &Value{Type: "text", Text: "Make it red"}}, Inputs: map[string]Binding{ReferenceInput: {From: "reference"}}},
		}}
	})
	It("records both inputs, replays them and invalidates edits when the reference changes", func() {
		now := time.Now()
		Expect(g.Validate()).To(Succeed())
		order, err := g.Order()
		Expect(err).NotTo(HaveOccurred())
		Expect(order).To(Equal([]string{"reference", "edit"}))
		w := New("editing", g, now)
		b := w.Branches[0]
		_, err = w.RunNode(b.ID, "edit", now)
		Expect(err).To(HaveOccurred())
		_, err = w.RunNode(b.ID, "reference", now)
		Expect(err).NotTo(HaveOccurred())
		a := b.Current().Nodes["reference"].Attempt
		Expect(w.Complete(b.ID, a.ID, []Value{{Type: "image", Artifact: "original"}}, "", nil, nil, now)).To(Succeed())
		_, err = w.RunNode(b.ID, "edit", now)
		Expect(err).NotTo(HaveOccurred())
		a = b.Current().Nodes["edit"].Attempt
		Expect(a.Arguments.Input.Text).To(Equal("Make it red"))
		Expect(a.Arguments.Inputs[ReferenceInput].Artifact).To(Equal("original"))
		Expect(w.Complete(b.ID, a.ID, []Value{{Type: "image", Artifact: "edited"}}, "", nil, nil, now)).To(Succeed())
		Expect(roundTrip(w).Branches[0].Current()).To(Equal(b.Current()))
		g.Nodes[0].Input.Value.Artifact = "replacement"
		Expect(w.Configure(b.ID, g, now)).To(Succeed())
		Expect(string(b.Current().Nodes["edit"].Status())).To(Equal("stale"))
		Expect(a.Arguments.Inputs[ReferenceInput].Artifact).To(Equal("original"))
	})
	It("accepts a direct reference or no reference, and rejects wrong types and forward links", func() {
		g.Nodes[1].Inputs = map[string]Binding{ReferenceInput: {Value: &Value{Type: "image", Artifact: "upload"}}}
		Expect(g.Validate()).To(Succeed())
		Expect(roundTrip(New("direct", g, time.Now())).Branches[0].Current().Graph).To(Equal(g))
		g.Nodes[1].Inputs = nil
		Expect(g.Validate()).To(Succeed())
		for _, binding := range []Binding{{From: "edit"}, {Value: &Value{Type: "mesh", Artifact: "wrong"}}, {From: "reference", Value: &Value{Type: "image", Artifact: "both"}}} {
			g.Nodes[1].Inputs = map[string]Binding{ReferenceInput: binding}
			Expect(g.Validate()).NotTo(Succeed())
		}
	})
})
