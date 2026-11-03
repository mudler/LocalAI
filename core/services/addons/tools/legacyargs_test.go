// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"os"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Legacy arguments declared by a tool", func() {
	It("names the replacement string for the legacy arguments the shipped Office manifest declares, without sending them", func() {
		raw, err := os.ReadFile("../../../../addons/office/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		f := &actFake{fake: &fake{list: apps.List{Apps: []apps.App{{ID: "office", Enabled: true, State: "running", Generation: 1, Tools: m.Capabilities.Tools, ToolDefinitions: m.Capabilities.ToolDefinitions}}}}}
		a := tools.NewChatToolAdapter(f)
		for _, tc := range []struct{ tool, legacy, field string }{{"create-docx", "blocks", "markdown"}, {"create-pdf", "blocks", "markdown"}, {"create-xlsx", "sheets", "csv"}} {
			for _, mixed := range []bool{false, true} {
				args := map[string]any{"path": "/new", tc.legacy: []any{map[string]any{"text": "PRIVATE"}}}
				if mixed {
					args[tc.field] = "new"
				}
				b, err := json.Marshal(args)
				Expect(err).NotTo(HaveOccurred())
				res, err := a.ExecuteChat(context.Background(), member, tools.NewChatCall("bob", "conv1"), tools.ToolName("office", tc.tool), b)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Kind).To(Equal(tools.ResultError))
				Expect(res.Code).To(Equal(tools.CodeInvalidRequest))
				Expect(res.Message).To(ContainSubstring(tc.legacy))
				Expect(res.Message).To(ContainSubstring(tc.field))
				Expect(res.JSON()).NotTo(ContainSubstring("PRIVATE"))
			}
		}
		Expect(f.acts).To(BeEmpty())
		Expect(f.calls()).To(BeZero())
	})

	It("works for any add-on that declares legacyArgs, and for none that does not", func() {
		schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"text":{"type":"string"}},"required":["path","text"],"additionalProperties":false}`)
		def := func(legacy ...contract.LegacyArgument) contract.ToolDefinition {
			return contract.ToolDefinition{Name: "make-note", Description: "Make a note.", Kind: contract.ToolKindAction, InputSchema: schema, LegacyArgs: legacy}
		}
		call := func(d contract.ToolDefinition) tools.ToolResult {
			f := &actFake{fake: &fake{list: apps.List{Apps: []apps.App{{ID: "notes", Enabled: true, State: "running", Generation: 1, Tools: []string{"make-note"}, ToolDefinitions: []contract.ToolDefinition{d}}}}}}
			res, err := tools.NewChatToolAdapter(f).ExecuteChat(context.Background(), member, tools.NewChatCall("bob", "conv1"), tools.ToolName("notes", "make-note"), json.RawMessage(`{"path":"/n","body":"PRIVATE"}`))
			if err != nil {
				return tools.ToolResult{}
			}
			return res
		}
		res := call(def(contract.LegacyArgument{Name: "body", Hint: "use the text string"}))
		Expect(res.Code).To(Equal(tools.CodeInvalidRequest))
		Expect(res.Message).To(Equal("body is no longer supported; use the text string"))
		Expect(call(def()).Message).To(BeEmpty(), "a tool that declares nothing keeps the plain refusal")
	})
})
