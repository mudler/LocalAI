// SPDX-License-Identifier: MIT
package piiadapter

import (
	"encoding/json"
	"strings"

	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// parseOR decodes a request body the way the HTTP layer does, so Input
// carries the same []any / map[string]any shapes the adapter sees live.
func parseOR(body string) *schema.OpenResponsesRequest {
	GinkgoHelper()
	req := &schema.OpenResponsesRequest{}
	Expect(json.Unmarshal([]byte(body), req)).To(Succeed())
	return req
}

// redactSecret mimics a detector: only the secret is rewritten, clean
// text passes through unchanged.
func redactSecret(s string) string { return strings.ReplaceAll(s, "sk-secret", "[REDACTED]") }

func orItems(req *schema.OpenResponsesRequest) []any {
	GinkgoHelper()
	items, ok := req.Input.([]any)
	Expect(ok).To(BeTrue())
	return items
}

var _ = Describe("OpenResponses adapter", func() {
	a := OpenResponses()

	It("scans and redacts a plain string input", func() {
		req := parseOR(`{"model":"m","input":"my key is sk-secret"}`)
		got := a.Scan(req)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Text).To(Equal("my key is sk-secret"))
		applyAll(a, req, redactSecret)
		Expect(req.Input).To(Equal("my key is [REDACTED]"))
	})

	It("leaves a clean string input byte-identical", func() {
		req := parseOR(`{"model":"m","input":"nothing to hide here"}`)
		applyAll(a, req, redactSecret)
		Expect(req.Input).To(Equal("nothing to hide here"))
	})

	It("scans and redacts instructions without touching clean input", func() {
		req := parseOR(`{"model":"m","instructions":"use sk-secret for the API","input":"hello"}`)
		Expect(a.Scan(req)).To(HaveLen(2))
		applyAll(a, req, redactSecret)
		Expect(req.Instructions).To(Equal("use [REDACTED] for the API"))
		Expect(req.Input).To(Equal("hello"))
	})

	It("redacts message items with string content, including typeless {role, content}", func() {
		req := parseOR(`{"model":"m","input":[
			{"type":"message","role":"user","content":"first sk-secret"},
			{"role":"user","content":"second sk-secret"},
			{"type":"message","role":"user","content":"clean text"}
		]}`)
		Expect(a.Scan(req)).To(HaveLen(3))
		applyAll(a, req, redactSecret)
		items := orItems(req)
		Expect(items[0].(map[string]any)["content"]).To(Equal("first [REDACTED]"))
		Expect(items[1].(map[string]any)["content"]).To(Equal("second [REDACTED]"))
		Expect(items[2].(map[string]any)["content"]).To(Equal("clean text"))
	})

	It("redacts text content parts and leaves non-text parts untouched", func() {
		req := parseOR(`{"model":"m","input":[{"type":"message","role":"user","content":[
			{"type":"input_text","text":"look at sk-secret"},
			{"type":"input_image","image_url":"data:image/png;base64,sk-secret"},
			{"type":"input_file","file_data":"sk-secret"},
			{"type":"input_text","text":"clean part"}
		]}]}`)
		got := a.Scan(req)
		Expect(got).To(HaveLen(2), "only the two input_text parts are scanned")
		applyAll(a, req, redactSecret)
		parts := orItems(req)[0].(map[string]any)["content"].([]any)
		Expect(parts[0].(map[string]any)["text"]).To(Equal("look at [REDACTED]"))
		Expect(parts[1].(map[string]any)["image_url"]).To(Equal("data:image/png;base64,sk-secret"))
		Expect(parts[2].(map[string]any)["file_data"]).To(Equal("sk-secret"))
		Expect(parts[3].(map[string]any)["text"]).To(Equal("clean part"))
	})

	It("redacts tool output given as a string", func() {
		req := parseOR(`{"model":"m","input":[
			{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{\"q\":\"sk-secret\"}"},
			{"type":"function_call_output","call_id":"c1","output":"result sk-secret"},
			{"type":"function_call_output","call_id":"c2","output":"clean result"}
		]}`)
		Expect(a.Scan(req)).To(HaveLen(3))
		applyAll(a, req, redactSecret)
		items := orItems(req)
		Expect(items[0].(map[string]any)["arguments"]).To(Equal(`{"q":"[REDACTED]"}`))
		Expect(items[1].(map[string]any)["output"]).To(Equal("result [REDACTED]"))
		Expect(items[2].(map[string]any)["output"]).To(Equal("clean result"))
	})

	It("scans structured tool output as its JSON encoding (what the handler forwards)", func() {
		req := parseOR(`{"model":"m","input":[{"type":"function_call_output","call_id":"c1","output":{"token":"sk-secret"}}]}`)
		got := a.Scan(req)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Text).To(Equal(`{"token":"sk-secret"}`))
		applyAll(a, req, redactSecret)
		Expect(orItems(req)[0].(map[string]any)["output"]).To(Equal(`{"token":"[REDACTED]"}`))
	})

	It("leaves structured tool output unchanged when nothing is redacted", func() {
		req := parseOR(`{"model":"m","input":[{"type":"function_call_output","call_id":"c1","output":{"ok":true}}]}`)
		Expect(a.Scan(req)).To(HaveLen(1))
		// The middleware only applies spans that were actually redacted;
		// with no updates the original object must survive as-is.
		a.Apply(req, nil)
		Expect(orItems(req)[0].(map[string]any)["output"]).To(Equal(map[string]any{"ok": true}))
	})

	It("redacts reasoning content parts and skips item references", func() {
		req := parseOR(`{"model":"m","input":[
			{"type":"reasoning","content":[{"type":"output_text","text":"user said sk-secret"}]},
			{"type":"item_reference","id":"msg_sk-secret"}
		]}`)
		Expect(a.Scan(req)).To(HaveLen(1))
		applyAll(a, req, redactSecret)
		items := orItems(req)
		parts := items[0].(map[string]any)["content"].([]any)
		Expect(parts[0].(map[string]any)["text"]).To(Equal("user said [REDACTED]"))
		Expect(items[1].(map[string]any)["id"]).To(Equal("msg_sk-secret"))
	})

	It("writes each update back only to its own position", func() {
		req := parseOR(`{"model":"m","instructions":"inst","input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}]},
			{"type":"message","role":"user","content":"c"}
		]}`)
		scans := a.Scan(req)
		Expect(scans).To(HaveLen(4))
		// Redact only the second part of the first item.
		scans[2].Text = "B"
		a.Apply(req, scans[2:3])
		Expect(req.Instructions).To(Equal("inst"))
		items := orItems(req)
		parts := items[0].(map[string]any)["content"].([]any)
		Expect(parts[0].(map[string]any)["text"]).To(Equal("a"))
		Expect(parts[1].(map[string]any)["text"]).To(Equal("B"))
		Expect(items[1].(map[string]any)["content"]).To(Equal("c"))
	})

	It("returns nothing for empty or foreign requests", func() {
		Expect(a.Scan(&schema.OpenResponsesRequest{})).To(BeEmpty())
		Expect(a.Scan(&schema.OpenAIRequest{})).To(BeNil())
		Expect(a.Scan(nil)).To(BeNil())
	})

	It("round-trips the index encoding including the sentinels", func() {
		for _, c := range []struct{ item, part int }{
			{orInstructions, -1}, {orInputString, -1}, {0, -1}, {0, 0}, {5, 3}, {100, 70},
		} {
			i, p := decOR(encOR(c.item, c.part))
			Expect(i).To(Equal(c.item))
			Expect(p).To(Equal(c.part))
		}
	})
})
