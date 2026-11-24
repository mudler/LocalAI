// SPDX-License-Identifier: MIT
package middleware_test

import (
	"encoding/json"
	"runtime"
	"strings"

	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type pointerJSON struct {
	Calls *int `json:"-"`
}

func (p *pointerJSON) MarshalJSON() ([]byte, error) { *p.Calls++; return []byte(`"custom"`), nil }

type pointerText struct {
	Calls *int `json:"-"`
}

func (p *pointerText) MarshalText() ([]byte, error) { *p.Calls++; return []byte(`custom`), nil }

type quotedContent struct {
	Text string `json:"text,string"`
}
type promotedContent struct {
	Text string `json:"text"`
}
type embeddedContent struct{ promotedContent }

var _ = Describe("Probe accepted representations", func() {
	It("rejects pointer marshalers without invoking them", func() {
		calls := 0
		for _, content := range []any{[]pointerJSON{{&calls}}, []pointerText{{&calls}}} {
			p := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Content: content}}})
			Expect(calls).To(BeZero())
			Expect(p.InputError).To(HaveOccurred())
		}
	})
	for _, kind := range []string{"quoted", "promoted"} {
		It("rejects unsupported "+kind+" structs before allocating", func() {
			var content any = quotedContent{strings.Repeat("\x00", systemone.MaxImageBodyBytes/6-100)}
			if kind == "promoted" {
				content = embeddedContent{promotedContent{strings.Repeat("x", systemone.MaxImageBodyBytes+1)}}
			}
			runtime.GC()
			var a, b runtime.MemStats
			runtime.ReadMemStats(&a)
			p := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Content: content}}})
			runtime.ReadMemStats(&b)
			Expect(p.InputError).To(HaveOccurred())
			Expect(b.TotalAlloc - a.TotalAlloc).To(BeNumerically("<", systemone.MaxBodyBytes))
		})
	}
	It("preserves raw tool JSON and schema tool content", func() {
		p := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Content: json.RawMessage(`[{"type":"text","text":"hello"}]`), FunctionCall: schema.FunctionCall{Name: "tool", Arguments: `{}`}, ToolCalls: []schema.ToolCall{{FunctionCall: schema.FunctionCall{Arguments: `{}`}}}}}})
		Expect(p.InputError).NotTo(HaveOccurred())
		Expect(json.Valid(p.State)).To(BeTrue())
	})
})
