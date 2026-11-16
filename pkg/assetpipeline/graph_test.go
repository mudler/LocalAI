// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pipeline input contracts", func() {
	It("allows only processing nodes as the primary output", func() {
		g := DefaultGraph()
		for _, id := range []string{"review", "image", "extract", "mesh"} {
			g.Primary = id
			Expect(g.Validate()).To(Succeed())
		}

		g.Primary = "source"
		Expect(g.Validate()).To(MatchError(ContainSubstring("processing node")))
	})

	It("keeps the asset description a direct text input", func() {
		g := DefaultGraph()
		g.Nodes[0].Input = Binding{Value: &Value{Type: "image", Artifact: "upload"}}
		Expect(g.Validate()).To(MatchError("source: incompatible image input"))

		g.Nodes[0].Input = Binding{From: "review"}
		Expect(g.Validate()).To(MatchError("source must have a direct input"))
	})

	It("rejects future and self connections even when their types match", func() {
		g := DefaultGraph()
		g.Nodes[1].Input = Binding{Value: &Value{Type: "text", Text: "A tower"}}
		g.Nodes[2].Input = Binding{From: "review"}
		g.Nodes[1], g.Nodes[2] = g.Nodes[2], g.Nodes[1]
		Expect(g.Validate()).To(MatchError("image: input must come from an earlier node"))

		g = DefaultGraph()
		g.Nodes[1].Input = Binding{From: "review"}
		Expect(g.Validate()).To(MatchError("review: input must come from an earlier node"))
	})

	It("rejects type-incompatible earlier outputs", func() {
		g := DefaultGraph()
		g.Nodes[3].Input = Binding{From: "source"}
		Expect(g.Validate()).To(MatchError("extract: incompatible text input"))
	})

	It("allows uploaded images directly on extraction or mesh and bypasses unused stages", func() {
		g := DefaultGraph()
		g.Nodes[3].Input = Binding{Value: &Value{Type: "image", Artifact: "upload"}}
		order, err := g.Order()
		Expect(err).NotTo(HaveOccurred())
		Expect(order).To(Equal([]string{"extract", "mesh"}))

		g.Nodes[4].Input = Binding{Value: &Value{Type: "image", Artifact: "upload"}}
		order, err = g.Order()
		Expect(err).NotTo(HaveOccurred())
		Expect(order).To(Equal([]string{"mesh"}))
	})

	It("keeps invalid configurations editable but blocks execution including inactive invalid nodes", func() {
		g := DefaultGraph()
		w := New("Tower", g, time.Now())
		branch := w.Branches[0].ID
		g.Nodes[4].Input = Binding{Value: &Value{Type: "image", Artifact: "upload"}}
		g.Nodes[1].Input = Binding{From: "mesh"}
		Expect(w.Configure(branch, g, time.Now())).To(Succeed())
		Expect(w.Branches[0].Current().Graph.Nodes[1].Input.From).To(Equal("mesh"))

		_, err := w.Begin(branch, time.Now())
		Expect(err).To(MatchError(ContainSubstring("earlier node")))
	})
})
