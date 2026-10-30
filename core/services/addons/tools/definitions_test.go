// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/tools"
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

var _ = Describe("Tools for the add-on's own page", func() {
	ctx := context.Background()
	It("are never offered to a model and are unknown when a model calls them", func() {
		app := apps.App{ID: "sandbox", Name: "sandbox", Generation: 1, Enabled: true, State: "running", Tools: []string{"status", "set-backend"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "status", Kind: contract.ToolKindRead, Description: "Say whether it works.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
				{Name: "set-backend", Kind: contract.ToolKindRead, Description: "Choose the backend.", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","properties":{"backend":{"type":"string"}},"additionalProperties":false}`)},
			}}
		f := &fake{list: apps.List{Apps: []apps.App{app}}}
		for name, a := range map[string]*tools.ToolAdapter{"plain": tools.NewToolAdapter(f), "chats": tools.NewChatToolAdapter(f)} {
			defs, err := a.Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(defs).To(HaveLen(1), name)
			Expect(defs[0].Tool).To(Equal("status"), name)
			_, err = a.Execute(ctx, member, tools.ToolName("sandbox", "set-backend"), json.RawMessage(`{"backend":"host"}`))
			Expect(err).To(MatchError(tools.ErrUnknownTool), name)
		}
		Expect(f.calls()).To(BeZero())
	})
})

var _ = Describe("A tool that shadows a built-in chat tool", func() {
	ctx := context.Background()
	schema := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)
	It("keeps its namespaced name and carries the built-in it shadows, only for a replaceable name", func() {
		app := apps.App{ID: "sandbox", Name: "sandbox", Generation: 1, Enabled: true, State: "running", Tools: []string{"bash", "write"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "bash", Kind: contract.ToolKindAction, Description: "Run a script.", InputSchema: schema, Shadows: "bash"},
				// A manifest that got past install with a name off the list
				// still never shadows it.
				{Name: "write", Kind: contract.ToolKindAction, Description: "Write.", InputSchema: schema, Shadows: "write_file"},
			}}
		f := &fake{list: apps.List{Apps: []apps.App{app}}}
		defs, err := tools.NewChatToolAdapter(f).Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(defs).To(HaveLen(2))
		byTool := map[string]tools.ToolDefinition{}
		for _, d := range defs {
			byTool[d.Tool] = d
		}
		Expect(byTool["bash"].Name).To(Equal(tools.ToolName("sandbox", "bash")))
		Expect(byTool["bash"].Shadows).To(Equal("bash"))
		Expect(byTool["write"].Shadows).To(BeEmpty())
	})
})
