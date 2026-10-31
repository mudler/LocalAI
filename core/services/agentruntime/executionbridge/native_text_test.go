// SPDX-License-Identifier: MIT
package executionbridge

import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	chat "github.com/mudler/nib/chat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pinned native text normalization", func() {
	It("accepts plain nil Data text", func() {
		s, e := normalizeNativeText(chat.ToolExecutionResult{Text: "plain"})
		Expect(e).NotTo(HaveOccurred())
		Expect(s).To(Equal("plain"))
	})
	It("accepts exactly concatenated pinned text-only MCP wrapper", func() {
		s, e := normalizeNativeText(chat.ToolExecutionResult{Text: "onetwo", Data: &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "one"}, &sdkmcp.TextContent{Text: "two"}}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(s).To(Equal("onetwo"))
	})
	It("rejects typed nil MCP wrappers", func() {
		var r *sdkmcp.CallToolResult
		_, e := normalizeNativeText(chat.ToolExecutionResult{Data: r})
		Expect(e).To(MatchError(ErrHold))
	})
	It("rejects mismatched parallel native text", func() {
		_, e := normalizeNativeText(chat.ToolExecutionResult{Text: "wrong", Data: &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "right"}}}})
		Expect(e).To(MatchError(ErrHold))
	})
	It("rejects wrapper metadata even when empty", func() {
		_, e := normalizeNativeText(chat.ToolExecutionResult{Data: &sdkmcp.CallToolResult{Meta: sdkmcp.Meta{}}})
		Expect(e).To(MatchError(ErrHold))
	})
	It("rejects text metadata", func() {
		_, e := normalizeNativeText(chat.ToolExecutionResult{Data: &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Meta: sdkmcp.Meta{}}}}})
		Expect(e).To(MatchError(ErrHold))
	})
	It("rejects annotations even when empty", func() {
		_, e := normalizeNativeText(chat.ToolExecutionResult{Data: &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Annotations: &sdkmcp.Annotations{}}}}})
		Expect(e).To(MatchError(ErrHold))
	})
	It("rejects mixed text and media", func() {
		_, e := normalizeNativeText(chat.ToolExecutionResult{Text: "one", Data: &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "one"}, &sdkmcp.ImageContent{}}}})
		Expect(e).To(MatchError(ErrHold))
	})
	It("rejects successful-looking MCP errors", func() {
		_, e := normalizeNativeText(chat.ToolExecutionResult{Data: &sdkmcp.CallToolResult{IsError: true}})
		Expect(e).To(MatchError(ErrHold))
	})
	It("rejects structured output including typed nil", func() {
		var p *string
		for _, v := range []any{map[string]any{}, p} {
			_, e := normalizeNativeText(chat.ToolExecutionResult{Data: &sdkmcp.CallToolResult{StructuredContent: v}})
			Expect(e).To(MatchError(ErrHold))
		}
	})
	It("rejects nil and typed nil content", func() {
		var p *sdkmcp.TextContent
		for _, v := range []sdkmcp.Content{nil, p} {
			_, e := normalizeNativeText(chat.ToolExecutionResult{Data: &sdkmcp.CallToolResult{Content: []sdkmcp.Content{v}}})
			Expect(e).To(MatchError(ErrHold))
		}
	})
	It("rejects other Data without stringification", func() {
		for _, v := range []any{sdkmcp.CallToolResult{}, "text", []byte("text"), (*string)(nil)} {
			_, e := normalizeNativeText(chat.ToolExecutionResult{Data: v})
			Expect(e).To(MatchError(ErrHold))
		}
	})
	It("rejects resources", func() {
		_, e := normalizeNativeText(chat.ToolExecutionResult{Data: &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.ResourceLink{}}}})
		Expect(e).To(MatchError(ErrHold))
	})
})
