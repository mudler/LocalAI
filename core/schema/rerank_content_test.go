// SPDX-License-Identifier: MIT
package schema_test

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Multimodal rerank request", func() {
	const png = "data:image/png;base64,iVBORw0KGgo="
	image := `{"type":"image_url","image_url":{"url":"` + png + `"}}`
	text := func(s string) string { return `{"type":"text","text":"` + s + `"}` }
	parse := func(body string) (*schema.RerankContent, bool, error) {
		return schema.ParseRerankRequest(json.RawMessage(body))
	}
	status := func(err error) (int, string) {
		var e *embeddingmedia.Error
		Expect(errors.As(err, &e)).To(BeTrue(), "error %v is not classified", err)
		return e.Status, e.Code
	}

	It("leaves the legacy form to the existing binder", func() {
		for _, body := range []string{
			`{"model":"m","query":"q","documents":["a","b"]}`,
			`{"model":"m","query":"q","documents":[]}`,
			`{"model":"m","query":5,"documents":["a"]}`,
			`not json`, `[]`, ``,
		} {
			content, structured, err := parse(body)
			Expect(content).To(BeNil(), body)
			Expect(structured).To(BeFalse(), body)
			Expect(err).NotTo(HaveOccurred(), body)
		}
	})

	It("parses a text query with text and image documents in order", func() {
		content, structured, err := parse(`{"model":"m","query":"find the cat","documents":["a dog",{"content":[` + text("a cat") + `,` + image + `]},{"content":[` + image + `]}],"top_n":2}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(structured).To(BeTrue())
		Expect(*content.Query.Text).To(Equal("find the cat"))
		Expect(content.Documents).To(HaveLen(3))
		Expect(*content.Documents[0].Text).To(Equal("a dog"))
		Expect(content.Documents[1].Content).To(HaveLen(2))
		Expect(content.Documents[1].Content[1].Type).To(Equal("image_url"))
		Expect(content.HasMedia()).To(BeTrue())
	})

	It("accepts an image query", func() {
		content, structured, err := parse(`{"query":{"content":[` + text("which one?") + `,` + image + `]},"documents":["a","b"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(structured).To(BeTrue())
		Expect(content.Query.Content).To(HaveLen(2))
		Expect(content.HasMedia()).To(BeTrue())
	})

	It("reports a content object of text parts as having no media", func() {
		content, structured, err := parse(`{"query":"q","documents":[{"content":[` + text("a") + `]},"b"]}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(structured).To(BeTrue())
		Expect(content.HasMedia()).To(BeFalse())
	})

	It("matches case-insensitive keys the way the binder does, and rejects duplicates", func() {
		_, structured, err := parse(`{"Query":"q","query":"p","documents":[{"content":[` + text("a") + `]}]}`)
		Expect(structured).To(BeTrue())
		code, _ := status(err)
		Expect(code).To(Equal(400))
	})

	for name, tc := range map[string]struct {
		body   string
		status int
	}{
		"audio part is not a rerank modality":    {`{"query":"q","documents":[{"content":[{"type":"input_audio","input_audio":{"data":"YQ=="}}]}]}`, 422},
		"video part is not a rerank modality":    {`{"query":"q","documents":[{"content":[{"type":"video_url","video_url":{"data":"YQ=="}}]}]}`, 422},
		"text after an image would be reordered": {`{"query":"q","documents":[{"content":[` + image + `,` + text("late") + `]}]}`, 400},
		"unknown field in a part":                {`{"query":"q","documents":[{"content":[{"type":"text","text":"a","x":1}]}]}`, 400},
		"empty content":                          {`{"query":"q","documents":[{"content":[]}]}`, 400},
		"documents is not an array":              {`{"query":{"content":[` + image + `]},"documents":"a"}`, 400},
		"no documents":                           {`{"query":{"content":[` + image + `]},"documents":[]}`, 400},
		"query is a number":                      {`{"query":7,"documents":[{"content":[` + image + `]}]}`, 400},
		"remote file url":                        {`{"query":"q","documents":[{"content":[{"type":"image_url","image_url":{"url":"file:///etc/passwd"}}]}]}`, 400},
		"bad base64":                             {`{"query":"q","documents":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,!!"}}]}]}`, 400},
	} {
		It("rejects: "+name, func() {
			_, structured, err := parse(tc.body)
			Expect(structured).To(BeTrue())
			code, _ := status(err)
			Expect(code).To(Equal(tc.status))
		})
	}

	It("charges the embedding budgets: parts and text", func() {
		part := text("a")
		parts := strings.TrimSuffix(strings.Repeat(part+",", 129), ",")
		_, _, err := parse(`{"query":"q","documents":[{"content":[` + parts + `]}]}`)
		code, _ := status(err)
		Expect(code).To(Equal(413))

		big := strings.Repeat("a", int(embeddingmedia.MaxTextBytes)+1)
		_, _, err = parse(`{"query":"q","documents":[{"content":[` + text(big) + `]}]}`)
		code, _ = status(err)
		Expect(code).To(Equal(413))
	})

	It("charges media bytes against the request-wide allowance", func() {
		payload := strings.Repeat("A", 4<<20) // 3 MiB decoded
		one := `{"type":"image_url","image_url":{"url":"data:image/png;base64,` + payload + `"}}`
		docs := make([]string, 11)
		for i := range docs {
			docs[i] = `{"content":[` + one + `]}`
		}
		_, _, err := parse(`{"query":"q","documents":[` + strings.Join(docs, ",") + `]}`)
		code, _ := status(err)
		Expect(code).To(Equal(413))
	})

	It("refuses a body over the cap before parsing it", func() {
		pad := strings.Repeat("x", int(embeddingmedia.MaxBodyBytes))
		_, structured, err := parse(`{"pad":"` + pad + `","query":{"content":[` + image + `]},"documents":["a"]}`)
		Expect(structured).To(BeTrue())
		code, _ := status(err)
		Expect(code).To(Equal(413))
	})

	Describe("llama.cpp-style object shape", func() {
		It("maps the object onto the same item as the content shape", func() {
			a, _, err := parse(`{"query":{"text":"find","image":"` + png + `"},"documents":[{"text":["a ","cat"],"image":["` + png + `","https://media.example.com/b.png"]},{"text":"plain"},"str"]}`)
			Expect(err).NotTo(HaveOccurred())
			b, _, err := parse(`{"query":{"content":[` + text("find") + `,` + image + `]},"documents":[{"content":[` + text("a cat") + `,` + image + `,{"type":"image_url","image_url":{"url":"https://media.example.com/b.png"}}]},{"content":[` + text("plain") + `]},"str"]}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(a.Query).To(Equal(b.Query))
			Expect(a.Documents[0]).To(Equal(b.Documents[0]))
			Expect(a.HasMedia()).To(BeTrue())
			// A text-only object is a plain string item.
			Expect(*a.Documents[1].Text).To(Equal("plain"))
			Expect(a.Documents[1].Content).To(BeEmpty())
			Expect(*a.Documents[2].Text).To(Equal("str"))
		})
		It("keeps several images in order, text first", func() {
			c, _, err := parse(`{"query":"q","documents":[{"image":["https://a.example.com/1.png","https://a.example.com/2.png"],"text":"t"}]}`)
			Expect(err).NotTo(HaveOccurred())
			parts := c.Documents[0].Content
			Expect(parts).To(HaveLen(3))
			Expect(parts[0].Type).To(Equal("text"))
			Expect(parts[1].Source.URL).To(HaveSuffix("1.png"))
			Expect(parts[2].Source.URL).To(HaveSuffix("2.png"))
		})
		It("accepts a raw base64 image as inline data, sniffed later at acquisition", func() {
			c, _, err := parse(`{"query":"q","documents":[{"image":"iVBORw0KGgo="}]}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Documents[0].Content[0].Source.Data).To(Equal("iVBORw0KGgo="))
			Expect(c.Documents[0].Content[0].Source.URL).To(BeEmpty())
		})
		It("has no media for a text-only object", func() {
			c, structured, err := parse(`{"query":{"text":"q"},"documents":[{"text":"a"}]}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(structured).To(BeTrue())
			Expect(c.HasMedia()).To(BeFalse())
		})
		for name, tc := range map[string]struct {
			body   string
			status int
			code   string
		}{
			"content mixed with text":  {`{"query":"q","documents":[{"content":[` + text("a") + `],"text":"b"}]}`, 400, "rerank_mixed_shapes"},
			"content mixed with image": {`{"query":"q","documents":[{"content":[` + image + `],"image":"` + png + `"}]}`, 400, "rerank_mixed_shapes"},
			"unknown key":              {`{"query":"q","documents":[{"text":"a","url":"x"}]}`, 400, ""},
			"duplicate key":            {`{"query":"q","documents":[{"text":"a","text":"b"}]}`, 400, ""},
			"empty object":             {`{"query":"q","documents":[{}]}`, 400, "invalid_rerank_input"},
			"empty image array":        {`{"query":"q","documents":[{"image":[]}]}`, 400, "invalid_rerank_input"},
			"empty text array only":    {`{"query":"q","documents":[{"text":[]}]}`, 400, "invalid_rerank_input"},
			"text is a number":         {`{"query":"q","documents":[{"text":5}]}`, 400, ""},
			"text array has a number":  {`{"query":"q","documents":[{"text":["a",5]}]}`, 400, ""},
			"image is null":            {`{"query":"q","documents":[{"image":null}]}`, 400, ""},
			"image is an object":       {`{"query":"q","documents":[{"image":{"url":"x"}}]}`, 400, "invalid_rerank_input"},
			"empty image string":       {`{"query":"q","documents":[{"image":""}]}`, 400, "invalid_rerank_input"},
			"file url":                 {`{"query":"q","documents":[{"image":"file:///etc/passwd"}]}`, 400, "invalid_rerank_input"},
			"ftp url":                  {`{"query":"q","documents":[{"image":"ftp://h.example.com/a.png"}]}`, 400, "invalid_rerank_input"},
			"credentials in url":       {`{"query":"q","documents":[{"image":"https://u:p@h.example.com/a.png"}]}`, 400, "invalid_rerank_input"},
			"absolute path":            {`{"query":"q","documents":[{"image":"/etc/passwd"}]}`, 400, "rerank_image_use_data_uri"},
			"relative path":            {`{"query":"q","documents":[{"image":"./cat.png"}]}`, 400, "rerank_image_use_data_uri"},
			"bare file name":           {`{"query":"q","documents":[{"image":"cat.png"}]}`, 400, "rerank_image_use_data_uri"},
			"raw string, not base64":   {`{"query":"q","documents":[{"image":"not base64 !"}]}`, 400, "rerank_image_use_data_uri"},
			"data uri without base64":  {`{"query":"q","documents":[{"image":"data:image/png,abc"}]}`, 400, "invalid_rerank_input"},
			"bad base64 in a data uri": {`{"query":"q","documents":[{"image":"data:image/png;base64,!!"}]}`, 400, ""},
			"query mixes the shapes":   {`{"query":{"content":[` + image + `],"image":"` + png + `"},"documents":["a"]}`, 400, "rerank_mixed_shapes"},
		} {
			It("rejects: "+name, func() {
				_, structured, err := parse(tc.body)
				Expect(structured).To(BeTrue())
				st, code := status(err)
				Expect(st).To(Equal(tc.status))
				if tc.code != "" {
					Expect(code).To(Equal(tc.code))
				}
			})
		}
		It("charges the same budgets: parts, text and media bytes", func() {
			imgs := strings.TrimSuffix(strings.Repeat(`"`+png+`",`, 129), ",")
			_, _, err := parse(`{"query":"q","documents":[{"image":[` + imgs + `]}]}`)
			st, _ := status(err)
			Expect(st).To(Equal(413))
			big := strings.Repeat("a", int(embeddingmedia.MaxTextBytes)+1)
			_, _, err = parse(`{"query":"q","documents":[{"text":"` + big + `"}]}`)
			st, _ = status(err)
			Expect(st).To(Equal(413))
			payload := strings.Repeat("A", 4<<20)
			docs := make([]string, 11)
			for i := range docs {
				docs[i] = `{"image":"` + payload + `"}`
			}
			_, _, err = parse(`{"query":"q","documents":[` + strings.Join(docs, ",") + `]}`)
			st, _ = status(err)
			Expect(st).To(Equal(413))
		})
	})

	Describe("JINARerankRequest JSON", func() {
		It("decodes the legacy form exactly as before", func() {
			var r schema.JINARerankRequest
			Expect(json.Unmarshal([]byte(`{"model":"m","query":"q","documents":["a","b"],"top_n":1,"backend":"x"}`), &r)).To(Succeed())
			Expect(r.Model).To(Equal("m"))
			Expect(r.Query).To(Equal("q"))
			Expect(r.Documents).To(Equal([]string{"a", "b"}))
			Expect(*r.TopN).To(Equal(1))
			Expect(r.Typed).To(BeNil())
		})
		It("keeps failing on a wrong type that is not a content object", func() {
			var r schema.JINARerankRequest
			Expect(json.Unmarshal([]byte(`{"query":5,"documents":["a"]}`), &r)).NotTo(Succeed())
			Expect(json.Unmarshal([]byte(`{"query":"q","documents":[1]}`), &r)).NotTo(Succeed())
		})
		It("tolerates content objects and leaves the legacy fields empty", func() {
			var r schema.JINARerankRequest
			Expect(json.Unmarshal([]byte(`{"model":"m","query":{"content":[`+image+`]},"documents":["a",{"content":[`+text("b")+`]}],"top_n":2}`), &r)).To(Succeed())
			Expect(r.Model).To(Equal("m"))
			Expect(r.Query).To(BeEmpty())
			Expect(r.Documents).To(BeEmpty())
			Expect(*r.TopN).To(Equal(2))
		})
	})
})
