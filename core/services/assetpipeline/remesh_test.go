// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"github.com/mudler/LocalAI/core/config"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Remeshing stage admission", func() {
	It("requires a mesh and the standalone processor rather than a generation model", func() {
		a := pipeline.Arguments{
			Input: pipeline.Value{Type: "mesh", Artifact: "mesh"},
			Node:  pipeline.NodeConfig{Kind: pipeline.Remesh, Params: map[string]string{"mode": "quad"}},
		}
		params, err := remeshParams(a, &config.ModelConfig{Backend: "meshremesh"})
		Expect(err).NotTo(HaveOccurred())
		Expect(params["operation"]).To(Equal("remesh"))
		Expect(a.Node.Params).NotTo(HaveKey("operation"))

		_, err = remeshParams(a, &config.ModelConfig{Backend: "trellis2cpp"})
		Expect(err).To(HaveOccurred())

		a.Input.Type = "image"
		_, err = remeshParams(a, &config.ModelConfig{Backend: "meshremesh"})
		Expect(err).To(HaveOccurred())
	})
})
