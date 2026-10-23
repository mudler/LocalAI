// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func host() contract.HostCompatibility {
	return contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// manifestWith validates the Tables manifest after a change to its tool
// definitions.
func manifestWith(change func(m *contract.Manifest)) error {
	GinkgoHelper()
	m := tablesManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	// Work on copies so one case never changes another.
	defs := append([]contract.ToolDefinition(nil), m.Capabilities.ToolDefinitions...)
	m.Capabilities.ToolDefinitions = defs
	change(&m)
	return contract.ValidateManifest(m, host())
}

func schema(props string, required string, extra string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","additionalProperties":false,"properties":%s,"required":%s%s}`, props, required, extra))
}

var _ = Describe("Manifest tool definitions", func() {
	It("accepts the Tables definitions and classifies every model-facing tool", func() {
		Expect(manifestWith(func(*contract.Manifest) {})).To(Succeed())
		kinds := map[string]string{}
		for _, d := range tablesManifest().Capabilities.ToolDefinitions {
			kinds[d.Name] = d.Kind
		}
		Expect(kinds).To(Equal(map[string]string{
			"databases-create": "create", "databases-inspect": "read", "databases-rename": "schema",
			"tables-create": "schema", "tables-rename": "schema", "tables-drop": "schema",
			"columns-add": "schema", "columns-rename": "schema", "columns-drop": "schema",
			"rows-query": "read", "rows-aggregate": "read", "rows-export": "read", "rows-insert": "write", "rows-update": "write", "rows-delete": "write",
		}))
	})

	It("keeps the schema bytes of a parsed manifest equal to what the runtime echoes back", func() {
		// The supervisor compares the registration it sent with the one the
		// addon returns by deep equality. The echo is encoded by a JSON
		// encoder, which compacts raw schemas, so a manifest parsed from an
		// indented file must already hold the compact form.
		raw, err := os.ReadFile("../../../../addons/tables/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring("\n    "), "the packaged manifest is indented")
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		wire, err := json.Marshal(m.Capabilities)
		Expect(err).NotTo(HaveOccurred())
		var echoed contract.Capabilities
		Expect(json.Unmarshal(wire, &echoed)).To(Succeed())
		Expect(echoed).To(Equal(m.Capabilities))
		// An HTML-significant character in a description survives the same trip.
		m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"type":"string","description":"a & b"}}`, `["resourceId"]`, "")
		withAmp, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		again, err := contract.ParseManifest(withAmp)
		Expect(err).NotTo(HaveOccurred())
		wire, _ = json.Marshal(again.Capabilities)
		Expect(json.Unmarshal(wire, &echoed)).To(Succeed())
		Expect(echoed).To(Equal(again.Capabilities))
	})

	It("rejects a definition that declares an identity, role, approval or operation property", func() {
		for _, name := range []string{"actor", "userId", "user_id", "role", "approved", "confirm", "digest", "proposalId", "operationId", "generation", "tool", "force"} {
			props := fmt.Sprintf(`{"resourceId":{"type":"string"},%q:{"type":"string"}}`, name)
			err := manifestWith(func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = schema(props, `["resourceId"]`, "")
			})
			Expect(err).To(HaveOccurred(), name)
		}
		// The same name nested in an object property is refused as well.
		err := manifestWith(func(m *contract.Manifest) {
			m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"type":"string"},"filter":{"type":"object","properties":{"approved":{"type":"boolean"}}}}`, `["resourceId"]`, "")
		})
		Expect(err).To(HaveOccurred())
	})

	It("rejects an undeclared tool, a repeated tool, an unknown kind and a missing or unbounded schema", func() {
		cases := map[string]func(*contract.Manifest){
			"undeclared": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Name = "databases-delete" },
			"repeated": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[1].Name = m.Capabilities.ToolDefinitions[0].Name
			},
			"kind":  func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Kind = "approve" },
			"empty": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].InputSchema = nil },
			"not object": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = json.RawMessage(`{"type":"string"}`)
			},
			"open schema": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"]}`)
			},
			// A read tool may name no resource; a tool that changes data may not.
			"no resource on a write": func(m *contract.Manifest) {
				for i := range m.Capabilities.ToolDefinitions {
					if m.Capabilities.ToolDefinitions[i].Kind == "write" {
						m.Capabilities.ToolDefinitions[i].InputSchema = schema(`{"table":{"type":"string"}}`, `["table"]`, "")
					}
				}
			},
			"resource on create": func(m *contract.Manifest) {
				for i := range m.Capabilities.ToolDefinitions {
					if m.Capabilities.ToolDefinitions[i].Kind == "create" {
						m.Capabilities.ToolDefinitions[i].InputSchema = schema(`{"resourceId":{"type":"string"},"name":{"type":"string"}}`, `["resourceId","name"]`, "")
					}
				}
			},
			"ref": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"$ref":"#/x"}}`, `["resourceId"]`, "")
			},
			"too large": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Description = strings.Repeat("a", 500) },
			"huge schema": func(m *contract.Manifest) {
				m.Capabilities.ToolDefinitions[0].InputSchema = schema(`{"resourceId":{"type":"string","description":"`+strings.Repeat("a", 9000)+`"}}`, `["resourceId"]`, "")
			},
			"markup": func(m *contract.Manifest) { m.Capabilities.ToolDefinitions[0].Description = "<b>do it</b>" },
		}
		for name, change := range cases {
			Expect(manifestWith(change)).To(HaveOccurred(), name)
		}
	})
})
