// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"strings"

	"github.com/mudler/LocalAI/core/config"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Motion adapter admission", func() {
	var a pipeline.Arguments
	var cfg *config.ModelConfig
	BeforeEach(func() {
		a = pipeline.Arguments{
			Node:  pipeline.NodeConfig{Kind: pipeline.Motion, Seed: 42, Params: map[string]string{"frames": "90"}},
			Input: pipeline.Value{Type: "text", Text: "A person walks forward."},
		}
		cfg = &config.ModelConfig{Backend: "kimodocpp"}
	})

	It("preserves the recorded prompt and seed without mutating configuration", func() {
		r, err := motionRequest(a, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Inputs["prompt"].Data).To(Equal(a.Input.Text))
		Expect(r.Params).To(Equal(map[string]string{"frames": "90", "seed": "42"}))
		Expect(a.Node.Params).To(Equal(map[string]string{"frames": "90"}))
	})

	It("rejects ambiguous seeds and unsupported or out-of-range parameters", func() {
		for _, params := range []map[string]string{{"seed": "43"}, {"frames": "151"}, {"steps": "0"}, {"text_guidance": "NaN"}, {"quality": "512"}} {
			a.Node.Params = params
			_, err := motionRequest(a, cfg)
			Expect(err).To(HaveOccurred())
		}

		a.Node.Params = nil
		a.Node.Seed = -1
		_, err := motionRequest(a, cfg)
		Expect(err).To(HaveOccurred())
	})

	It("uses the same prompt limits as the animation endpoint", func() {
		for _, prompt := range []string{" ", "walk\x00", string([]byte{0xff}), strings.Repeat("a", 4097)} {
			a.Input.Text = prompt
			_, err := motionRequest(a, cfg)
			Expect(err).To(HaveOccurred())
		}
	})

	It("rejects incompatible inputs and models before inference", func() {
		_, err := motionRequest(a, &config.ModelConfig{Backend: "trellis2cpp"})
		Expect(err).To(HaveOccurred())

		a.Input.Type = "mesh"
		_, err = motionRequest(a, cfg)
		Expect(err).To(HaveOccurred())

		a.Input.Type = "text"
		a.Inputs = map[string]pipeline.Value{"unexpected": {Type: "mesh"}}
		_, err = motionRequest(a, cfg)
		Expect(err).To(HaveOccurred())
	})
})
