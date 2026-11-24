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

	It("advertises exactly the three string-only text creation contracts", func() {
		raw, err := os.ReadFile(manifestPath)
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		for _, defs := range [][]contract.ToolDefinition{tools.Definitions(), m.Capabilities.ToolDefinitions} {
			seen := 0
			for _, d := range defs {
				field := "markdown"
				keys := []string{"path", "markdown", "title"}
				switch d.Name {
				case tools.ToolCreateDocx, tools.ToolCreatePdf:
				case tools.ToolCreateXlsx:
					field, keys = "csv", []string{"path", "csv", "sheet", "title"}
				default:
					continue
				}
				seen++
				var schema map[string]any
				Expect(json.Unmarshal(d.InputSchema, &schema)).To(Succeed())
				Expect(schema["type"]).To(Equal("object"))
				Expect(schema["additionalProperties"]).To(Equal(false))
				Expect(schema["required"]).To(ConsistOf("path", field))
				props := schema["properties"].(map[string]any)
				Expect(props).To(HaveLen(len(keys)))
				for _, key := range keys {
					Expect(props).To(HaveKey(key))
					Expect(props[key].(map[string]any)["type"]).To(Equal("string"))
				}
				for _, forbidden := range []string{`"array"`, `"items"`, `"oneOf"`, `"anyOf"`, `"allOf"`, `"blocks"`, `"sheets"`, `"rows"`, "2000"} {
					Expect(string(d.InputSchema)).NotTo(ContainSubstring(forbidden), d.Name)
				}
			}
			Expect(seen).To(Equal(3))
		}
	})

	It("makes only the edit and create tools actions", func() {
		for _, d := range tools.Definitions() {
			wantAction := d.Name[:4] == "edit" || d.Name[:6] == "create"
			Expect(d.Kind == contract.ToolKindAction).To(Equal(wantAction), "%s has kind %s", d.Name, d.Kind)
		}
	})
})
