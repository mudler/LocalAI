// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"os"

	"github.com/mudler/LocalAI/addons/office/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const manifestPath = "../../manifest.json"

var _ = Describe("manifest", func() {
	// The manifest declares exactly the tools of the runtime, with the
	// definitions the runtime builds. Run with UPDATE_MANIFEST=1 to rewrite the
	// declared part after a change.
	It("declares the tools of the runtime", func() {
		raw, err := os.ReadFile(manifestPath)
		Expect(err).NotTo(HaveOccurred())
		var doc map[string]any
		Expect(json.Unmarshal(raw, &doc)).To(Succeed())
		caps := doc["capabilities"].(map[string]any)
		if os.Getenv("UPDATE_MANIFEST") == "1" {
			caps["tools"] = tools.Tools
			caps["toolDefinitions"] = tools.Definitions()
			caps["actions"] = true
			out, _ := json.MarshalIndent(doc, "", "  ")
			Expect(os.WriteFile(manifestPath, append(out, '\n'), 0o644)).To(Succeed())
			raw = append(out, '\n')
		}
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred(), "the manifest does not parse")
		Expect(m.Capabilities.Tools).To(Equal(tools.Tools), "manifest tools")
		Expect(m.Capabilities.Actions).To(BeTrue(), "the manifest does not say it has action tools")
		want := tools.Definitions()
		Expect(m.Capabilities.ToolDefinitions).To(HaveLen(len(want)), "manifest definitions")
		for i, d := range m.Capabilities.ToolDefinitions {
			Expect(d.Name).To(Equal(want[i].Name), "definition %d differs", i)
			Expect(d.Kind).To(Equal(want[i].Kind), "definition %d differs: %s", i, d.Name)
			Expect(d.Description).To(Equal(want[i].Description), "definition %d differs: %s", i, d.Name)
			var a, b any
			_ = json.Unmarshal(d.InputSchema, &a)
			_ = json.Unmarshal(want[i].InputSchema, &b)
			Expect(a).To(Equal(b), "the schema of %s differs from the runtime", d.Name)
			Expect(contract.ValidateToolSchema(d.InputSchema, d.Kind)).To(Succeed(), "the schema of %s is refused", d.Name)
		}
	})

	It("makes only the edit and create tools actions", func() {
		for _, d := range tools.Definitions() {
			wantAction := d.Name[:4] == "edit" || d.Name[:6] == "create"
			Expect(d.Kind == contract.ToolKindAction).To(Equal(wantAction), "%s has kind %s", d.Name, d.Kind)
		}
	})
})
