// SPDX-License-Identifier: MIT
package schema_test

import (
	"encoding/json"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
)

var _ = Describe("Multimodal contracts", func() {
	It("preserves ordered mixed content and video alias", func() {
		items, structured, err := schema.ParseEmbeddingInput(json.RawMessage(`["a",{"content":[{"type":"text","text":""},{"type":"video_url","video_url":{"data":"YQ=="}}]}]`))
		Expect(err).NotTo(HaveOccurred())
		Expect(structured).To(BeTrue())
		Expect(items).To(HaveLen(2))
		Expect(*items[0].Text).To(Equal("a"))
		Expect(items[1].Content[1].Type).To(Equal("input_video"))
	})
	for _, raw := range []string{
		`{}`, `{"content":null}`, `{"content":[]}`, `{"content":[null]}`, `{"content":[{"type":"text"}]}`,
		`{"content":[{"type":"text","text":null}]}`, `{"content":[{"type":"other"}]}`,
		`{"content":[],"content":[]}`, `{"content":[],"extra":1}`,
		`{"content":[{"type":"text","text":"a","text":"b"}]}`,
		`{"content":[{"type":"text","text":"a","extra":1}]}`,
		`{"content":[{"type":"input_audio","input_audio":{"data":"YQ==","url":"https://example.com/a"}}]}`,
		`{"content":[{"type":"input_audio","input_audio":{"data":"!"}}]}`,
		`{"content":[{"type":"image_url","image_url":{"url":"file:///a"}}]}`,
		`{"content":[{"type":"input_video","input_video":{"url":"https://example.com/a","url":"https://example.com/b"}}]}`,
		`[{"content":[{"type":"text","text":"a"}]},[1,2]]`,
	} {
		It("rejects malformed structure "+raw, func() {
			_, structured, err := schema.ParseEmbeddingInput(json.RawMessage(raw))
			Expect(structured).To(BeTrue())
			Expect(err).To(HaveOccurred())
		})
	}
	It("counts UTF-8 bytes and item/part limits", func() {
		for _, n := range []int{(1 << 19) - 1, 1 << 19, (1 << 19) + 1} {
			raw, _ := json.Marshal(map[string]any{"content": []any{map[string]string{"type": "text", "text": strings.Repeat("é", n)}}})
			_, _, err := schema.ParseEmbeddingInput(raw)
			if n > 1<<19 {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		}
		for _, limit := range []int{16, 128} {
			for _, n := range []int{limit - 1, limit, limit + 1} {
				part := `{"type":"text","text":"a"}`
				raw := `{"content":[` + strings.TrimSuffix(strings.Repeat(part+",", n), ",") + `]}`
				if limit == 16 {
					raw = `[` + strings.TrimSuffix(strings.Repeat(`{"content":[`+part+`]},`, n), ",") + `]`
				}
				_, _, err := schema.ParseEmbeddingInput(json.RawMessage(raw))
				if n > limit {
					Expect(err).To(HaveOccurred())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			}
		}
	})
})
var _ = Describe("Legacy embedding parsing", func() {
	for _, raw := range []string{`"hello"`, `"{\"content\":[]}"`, `["a","b"]`, `[1,2]`, `[[1,2],[3]]`, `null`, `[]`} {
		It("leaves legacy input unchanged "+raw, func() {
			_, structured, err := schema.ParseEmbeddingInput(json.RawMessage(raw))
			Expect(err).NotTo(HaveOccurred())
			Expect(structured).To(BeFalse())
		})
	}
})

var _ = Describe("Multimodal contracts", func() {
	for _, raw := range []string{
		`{"content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}`,
		`{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,YQ=="}}]}`,
		`{"content":[{"type":"input_audio","input_audio":{"data":"YQ==","format":"wav"}}]}`,
		`{"content":[{"type":"input_audio","input_audio":{"url":"https://example.com/a.flac"}}]}`,
		`{"content":[{"type":"input_video","input_video":{"data":"YQ=="}}]}`,
	} {
		It("accepts source syntax without acquiring or decoding media "+raw, func() {
			items, structured, err := schema.ParseEmbeddingInput(json.RawMessage(raw))
			Expect(err).NotTo(HaveOccurred())
			Expect(structured).To(BeTrue())
			Expect(items).To(HaveLen(1))
		})
	}
	for _, source := range []string{`{}`, `null`, `{"data":null}`, `{"data":""}`, `{"url":""}`, `{"data":"YQ==","extra":true}`, `{"data":"YQ==","data":"Yg=="}`, `{"data":"YQ==","format":null}`, `{"data":"YR=="}`, `{"data":"YQ"}`, `{"data":"YQ==\n"}`, `{"url":"https://user:password@example.com/a"}`, `{"url":"data:audio/wav,YQ=="}`} {
		It("rejects invalid media source "+source, func() {
			_, structured, err := schema.ParseEmbeddingInput(json.RawMessage(`{"content":[{"type":"input_audio","input_audio":` + source + `}]}`))
			Expect(structured).To(BeTrue())
			Expect(err).To(HaveOccurred())
		})
	}
	It("counts string items as source parts in a mixed batch", func() {
		raw := `["a",{"content":[` + strings.TrimSuffix(strings.Repeat(`{"type":"text","text":""},`, 128), ",") + `]}]`
		_, _, err := schema.ParseEmbeddingInput(json.RawMessage(raw))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Multimodal contracts", func() {
	It("rejects case-insensitive input aliases instead of binding a hidden object", func() {
		for _, raw := range []string{`{"Input":{"content":[{"type":"text","text":"a"}]}}`, `{"input":"a","INPUT":{"content":[]}}`} {
			_, structured, err := schema.ParseEmbeddingRequest(json.RawMessage(raw))
			Expect(structured).To(BeTrue())
			Expect(err).To(HaveOccurred())
		}
	})
	It("rejects malformed non-UTF8 structured text", func() {
		_, _, err := schema.ParseEmbeddingInput(json.RawMessage("{\"content\":[{\"type\":\"text\",\"text\":\"\xff\"}]}"))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Multimodal contracts", func() {
	const input = `"input":{"content":[{"type":"text","text":"a"}]}`
	for _, empty := range []string{`null`, `[]`, `[ ]`} {
		It("does not treat empty messages "+empty+" as a conflict", func() {
			items, structured, err := schema.ParseEmbeddingRequest(json.RawMessage(`{` + input + `,"messages":` + empty + `}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(structured).To(BeTrue())
			Expect(items).To(HaveLen(1))
		})
	}
	It("rejects nonempty messages alongside structured input", func() {
		_, structured, err := schema.ParseEmbeddingRequest(json.RawMessage(`{` + input + `,"messages":[{"role":"user","content":"x"}]}`))
		Expect(structured).To(BeTrue())
		Expect(err).To(HaveOccurred())
	})
	It("leaves unrelated envelope fields alone; strictness covers content objects only", func() {
		items, structured, err := schema.ParseEmbeddingRequest(json.RawMessage(`{` + input + `,"truncate":"END","unexpected":true}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(structured).To(BeTrue())
		Expect(items).To(HaveLen(1))
	})
})

var _ = Describe("Multimodal contracts", func() {
	const input = `"input":{"content":[{"type":"text","text":"a"}]}`
	// encoding/json folds names with unicode.SimpleFold, so these spellings bind
	// to messages and user even though they are not ASCII.
	It("rejects a folded messages alias that the JSON binder still binds", func() {
		_, structured, err := schema.ParseEmbeddingRequest(json.RawMessage(`{` + input + `,"meſſages":[{"role":"user","content":"hi"}]}`))
		Expect(structured).To(BeTrue())
		Expect(err).To(HaveOccurred())
	})
	It("reports a folded duplicate key as a duplicate", func() {
		_, structured, err := schema.ParseEmbeddingRequest(json.RawMessage(`{` + input + `,"user":"a","uſer":"b"}`))
		Expect(structured).To(BeTrue())
		Expect(err).To(HaveOccurred())
	})
})
