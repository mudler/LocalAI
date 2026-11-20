// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"github.com/mudler/LocalAI/core/config"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Skinning stage inputs", func() {
	It("records both branches without mutating configuration or adding seeds to retargeting", func() {
		a := pipeline.Arguments{
			Node:   pipeline.NodeConfig{Kind: pipeline.Skin, Seed: 7, Params: map[string]string{"fit": "global"}},
			Input:  pipeline.Value{Type: "mesh", Artifact: "asset"},
			Inputs: map[string]pipeline.Value{"skeleton": {Type: "motion", Artifact: "motion"}},
		}
		cfg := &config.ModelConfig{Backend: "skintokenscpp"}
		r, err := skinningRequest(a, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Inputs["mesh"].Data).To(Equal("asset"))
		Expect(r.Inputs["skeleton"].Data).To(Equal("motion"))
		Expect(r.Params["seed"]).To(Equal("7"))
		Expect(a.Node.Params).NotTo(HaveKey("seed"))

		a.Node.Kind = pipeline.Retarget
		a.Node.Params = nil
		a.Inputs = map[string]pipeline.Value{"motion": {Type: "motion", Artifact: "motion"}}
		r, err = skinningRequest(a, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Params).To(BeEmpty())
	})

	It("rejects missing, incompatible and unknown secondary inputs before staging files", func() {
		cfg := &config.ModelConfig{Backend: "skintokenscpp"}
		for _, inputs := range []map[string]pipeline.Value{
			nil,
			{"skeleton": {Type: "image", Artifact: "image"}},
			{"skeleton": {Type: "motion"}},
			{"motion": {Type: "motion", Artifact: "motion"}},
		} {
			a := pipeline.Arguments{
				Node:   pipeline.NodeConfig{Kind: pipeline.Skin},
				Input:  pipeline.Value{Type: "mesh", Artifact: "asset"},
				Inputs: inputs,
			}
			_, err := skinningRequest(a, cfg)
			Expect(err).To(HaveOccurred())
		}
	})

	It("does not treat a text-to-motion engine as a rigging engine", func() {
		a := pipeline.Arguments{
			Node:  pipeline.NodeConfig{Kind: pipeline.Rig},
			Input: pipeline.Value{Type: "mesh", Artifact: "asset"},
		}
		_, err := skinningRequest(a, &config.ModelConfig{Backend: "kimodocpp"})
		Expect(err).To(HaveOccurred())

		_, err = skinningRequest(a, &config.ModelConfig{Backend: "skintokenscpp"})
		Expect(err).NotTo(HaveOccurred())
	})
})
