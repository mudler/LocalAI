// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"strings"
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Configuration checkpoint details", func() {
	var w Workspace
	var graph Graph
	var branch string
	BeforeEach(func() {
		graph = DefaultGraph()
		w = New("Configuration details", graph, time.Now())
		branch = w.Branches[0].ID
		graph = w.Branches[0].Current().Graph
	})
	message := func() string {
		Expect(w.Configure(branch, graph, time.Now())).To(Succeed())

		for _, event := range w.Branches[0].Events() {
			if event.Kind == "configuration" && event.Message != "" {
				return event.Message
			}
		}
		Fail("configuration checkpoint missing")
		return ""
	}
	It("records fields and values for a batch of edits", func() {
		graph.Primary = "image"
		graph.Nodes[0].Input.Value.Text = "A bronze shield"
		graph.Nodes[2].Model = "z-image"
		graph.Nodes[2].Count = 3
		graph.Nodes[2].Seed = 42
		graph.Nodes[3].Pause = false
		graph.Nodes[3].Params["mode"] = "black"
		text := message()
		Expect(text).To(ContainSubstring("Configuration saved:"))
		Expect(text).To(ContainSubstring("Primary output: 3D generation → Image generation"))
		Expect(text).To(ContainSubstring("Asset description input: direct text: (empty) → direct text: A bronze shield"))
		Expect(text).To(ContainSubstring("Image generation model: (empty) → z-image"))
		Expect(text).To(ContainSubstring("Image generation image count: 1 → 3"))
		Expect(text).To(ContainSubstring("Image generation seed: 0 → 42"))
		Expect(text).To(ContainSubstring("Object extraction pause for selection: true → false"))
		Expect(text).To(ContainSubstring("Object extraction mode: keep → black"))
	})

	It("describes upstream and direct image input changes", func() {
		graph.Nodes[2].Input = Binding{From: "source"}
		graph.Nodes[3].Input = Binding{Value: &Value{Type: "image", Artifact: "uploaded-image.png"}}
		text := message()
		Expect(text).To(ContainSubstring("Image generation input: output from Prompt review → output from Asset description"))
		Expect(text).To(ContainSubstring("Object extraction input: output from Image generation → direct image: uploaded-image.png"))
	})

	It("keeps saved details alongside invalid graph errors and advice warnings", func() {
		graph.Target.Inputs = []string{"text"}
		graph.Target.Advice = "White background"
		text := message()
		Expect(text).To(ContainSubstring("3D engine accepted inputs: [image] → [text]"))
		Expect(text).To(ContainSubstring("3D engine prompting advice:"))
		Expect(text).To(ContainSubstring("Graph cannot run:"))
		Expect(w.Branches[0].Events()[len(w.Branches[0].Events())-1].Kind).To(Equal(WarningKind))
	})

	It("bounds Unicode descriptions without losing the full checkpoint", func() {
		graph.Nodes[0].Input.Value.Text = strings.Repeat("猫", 1000)
		text := message()
		Expect(text).To(ContainSubstring(strings.Repeat("猫", 120) + "…"))
		Expect(len([]rune(text))).To(BeNumerically("<", 300))
		Expect(w.Branches[0].Current().Graph.Nodes[0].Input.Value.Text).To(Equal(strings.Repeat("猫", 1000)))
	})

	It("does not create a checkpoint when configuration is unchanged", func() {
		count := len(w.Branches[0].Events())
		Expect(w.Configure(branch, graph, time.Now())).To(Succeed())
		Expect(w.Branches[0].Events()).To(HaveLen(count))
	})

	It("records parameter removals and stage additions deterministically", func() {
		delete(graph.Nodes[3].Params, "mode")
		graph.Nodes[3].Params["points"] = "20,30,1"
		graph.Nodes[3].Params["background"] = "black"
		graph.Nodes = append(graph.Nodes, NodeConfig{ID: "extra", Kind: Review, Input: Binding{From: "source"}})
		text := message()
		Expect(text).To(ContainSubstring("Object extraction background: (empty) → black\nObject extraction mode: keep → (empty)\nObject extraction points: (empty) → 20,30,1"))
		Expect(text).To(ContainSubstring("Prompt review: stage added"))
	})
})
