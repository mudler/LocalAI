package middleware_test

import (
	"encoding/json"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"runtime"
	"strings"
)

var _ = Describe("Probe extraction bounds", func() {
	for _, api := range []string{"openai", "anthropic"} {
		for _, typed := range []bool{false, true} {
			for _, field := range []string{"text", "image"} {
				It(api+" bounds allocations before copying "+field, func() {
					huge := strings.Repeat("x", systemone.MaxImageBodyBytes+1)
					raw := `[{"type":"text","text":"` + huge + `"}]`
					if field == "image" {
						raw = `[{"type":"image_url","image_url":{"url":"` + huge + `"}}]`
						if api == "anthropic" {
							raw = `[{"type":"image","source":{"type":"base64","data":"` + huge + `"}}]`
						}
					}
					var content any
					if typed {
						if api == "openai" {
							var v []schema.Content
							Expect(json.Unmarshal([]byte(raw), &v)).To(Succeed())
							content = v
						} else {
							var v []schema.AnthropicContentBlock
							Expect(json.Unmarshal([]byte(raw), &v)).To(Succeed())
							content = v
						}
					} else {
						Expect(json.Unmarshal([]byte(raw), &content)).To(Succeed())
					}
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					if api == "openai" {
						p := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Content: content}}})
						Expect(p.InputError).To(HaveOccurred())
					} else {
						p, _ := AnthropicProbe(&schema.AnthropicRequest{Messages: []schema.AnthropicMessage{{Content: content}}})
						Expect(p.InputError).To(HaveOccurred())
					}
					runtime.ReadMemStats(&after)
					Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", systemone.MaxBodyBytes))
				})
			}
		}
	}
	It("counts JSON escaping before allocation", func() {
		req := &schema.OpenAIRequest{Messages: []schema.Message{{Content: strings.Repeat("\x00", systemone.MaxImageBodyBytes/len(`\u0000`)+1)}}}
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		p := OpenAIProbeFromRequest(req)
		runtime.ReadMemStats(&b)
		Expect(p.InputError).To(HaveOccurred())
		Expect(b.TotalAlloc - a.TotalAlloc).To(BeNumerically("<", systemone.MaxBodyBytes))
	})
	It("preserves text at the ordinary text budget", func() {
		p := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Content: strings.Repeat("\x00", systemone.MaxBodyBytes)}}})
		Expect(p.InputError).NotTo(HaveOccurred())
	})
})

var _ = Describe("Probe escaping boundaries", func() {
	It("accepts exactly the serialized limit and rejects the next escaped byte", func() {
		const escapeBytes = len(`\u0000`)
		req := &schema.OpenAIRequest{Messages: []schema.Message{{Content: ""}}}
		empty, err := json.Marshal(req.Messages)
		Expect(err).NotTo(HaveOccurred())
		available := systemone.MaxImageBodyBytes - len(empty)
		text := strings.Repeat("\x00", available/escapeBytes) + strings.Repeat("a", available%escapeBytes)
		req.Messages[0].Content = text
		p := OpenAIProbeFromRequest(req)
		Expect(p.InputError).NotTo(HaveOccurred())
		Expect(p.State).To(HaveLen(systemone.MaxImageBodyBytes))
		req.Messages[0].Content = text + "\x00"
		p = OpenAIProbeFromRequest(req)
		Expect(p.InputError).To(HaveOccurred())
		Expect(p.State).To(BeEmpty())
		Expect(p.Prompt).To(BeEmpty())
	})
	It("bounds recursive direct internal values", func() {
		cycle := map[string]any{}
		cycle["self"] = cycle
		p := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Content: cycle}}})
		Expect(p.InputError).To(HaveOccurred())
	})
})
