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

var _ = Describe("Office creation compatibility", func() {
	It("names the replacement string for legacy arguments at the chat boundary without sending them", func() {
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
})
