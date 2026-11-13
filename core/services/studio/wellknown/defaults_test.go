package wellknown_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/wellknown"
)

var _ = Describe("defaults.json", func() {
	validate := func(body string) []string {
		_, errs := wellknown.Validate(wellknown.Defaults, []byte(body))
		return errs
	}

	Describe("the file", func() {
		It("is a well-known file at LocalAI/defaults.json", func() {
			n, err := wellknown.Parse("defaults")
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(wellknown.Defaults))
			Expect(wellknown.File(wellknown.Defaults)).To(Equal("/LocalAI/defaults.json"))
		})

		It("accepts a model name, ask and null for each capability", func() {
			Expect(validate(`{"chat":"qwen3-8b-instruct","image":"flux.1-schnell","tts":"kokoro-82m","stt":"whisper-large-v3-turbo","embeddings":"bge-m3","rerank":"bge-reranker-v2-m3","video":null,"vision":"ask"}`)).To(BeEmpty())
			Expect(validate(`{}`)).To(BeEmpty())
		})

		It("keeps a key it does not know", func() {
			obj, errs := wellknown.Validate(wellknown.Defaults, []byte(`{"image":"m","note":"mine"}`))
			Expect(errs).To(BeEmpty())
			Expect(obj).To(HaveKeyWithValue("note", "mine"))
		})

		DescribeTable("refuses a value that is not a model name, ask or null",
			func(body, want string) {
				Expect(validate(body)).To(ContainElement(ContainSubstring(want)))
			},
			Entry("a number", `{"image":3}`, "image"),
			Entry("a list", `{"tts":["a"]}`, "tts"),
			Entry("a name that is too long", `{"stt":"`+strings.Repeat("m", 201)+`"}`, "stt"),
			Entry("a blank name, in words", `{"video":"   "}`, "use the name of an installed model"),
			Entry("an empty name, in words", `{"rerank":""}`, "use the name of an installed model"),
			Entry("a control character", `{"embeddings":"a\u0007b"}`, "embeddings"),
		)

		It("serves the same schema in the skill and from the server", func() {
			data, err := wellknown.Schema(wellknown.Defaults)
			Expect(err).NotTo(HaveOccurred())
			for _, k := range wellknown.DefaultCapabilities {
				Expect(string(data)).To(ContainSubstring(`"` + k + `"`))
			}
		})
	})

	Describe("DefaultFor", func() {
		d := func(m map[string]any) map[string]any { return m }
		DescribeTable("reads the choice for a capability",
			func(defaults, ui map[string]any, capability string, want wellknown.Default) {
				Expect(wellknown.DefaultFor(defaults, ui, capability)).To(Equal(want))
			},
			Entry("a model is set", d(map[string]any{"image": "flux"}), nil, "image",
				wellknown.Default{Kind: wellknown.DefaultModel, Model: "flux", From: "defaults.json"}),
			Entry("ask", d(map[string]any{"tts": "ask"}), nil, "tts",
				wellknown.Default{Kind: wellknown.DefaultAsk, From: "defaults.json"}),
			Entry("ask in any case", d(map[string]any{"tts": " Ask "}), nil, "tts",
				wellknown.Default{Kind: wellknown.DefaultAsk, From: "defaults.json"}),
			Entry("null is unset", d(map[string]any{"video": nil}), nil, "video", wellknown.Default{}),
			Entry("a missing key is unset", d(map[string]any{}), nil, "stt", wellknown.Default{}),
			Entry("no file is unset", nil, nil, "rerank", wellknown.Default{}),
			Entry("the old defaultModel is the chat fallback", nil, d(map[string]any{"defaultModel": "old-chat"}), "chat",
				wellknown.Default{Kind: wellknown.DefaultModel, Model: "old-chat", From: "ui.json"}),
			Entry("defaults.json chat wins over the old defaultModel", d(map[string]any{"chat": "new-chat"}), d(map[string]any{"defaultModel": "old-chat"}), "chat",
				wellknown.Default{Kind: wellknown.DefaultModel, Model: "new-chat", From: "defaults.json"}),
			Entry("chat ask wins over the old defaultModel", d(map[string]any{"chat": "ask"}), d(map[string]any{"defaultModel": "old-chat"}), "chat",
				wellknown.Default{Kind: wellknown.DefaultAsk, From: "defaults.json"}),
			Entry("a null chat falls back to the old defaultModel", d(map[string]any{"chat": nil}), d(map[string]any{"defaultModel": "old-chat"}), "chat",
				wellknown.Default{Kind: wellknown.DefaultModel, Model: "old-chat", From: "ui.json"}),
			Entry("the old defaultModel is for chat only", nil, d(map[string]any{"defaultModel": "old-chat"}), "image", wellknown.Default{}),
		)
	})

	Describe("the service", func() {
		ctx := context.Background()

		It("reads the default of the user, and not of another user", func() {
			svc, _ := newService()
			_, err := svc.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Defaults, Values: map[string]any{"image": "flux", "tts": "ask"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.DefaultFor(ctx, alice, "image")).To(Equal(wellknown.Default{Kind: wellknown.DefaultModel, Model: "flux", From: "defaults.json"}))
			Expect(svc.DefaultFor(ctx, alice, "tts").Kind).To(Equal(wellknown.DefaultAsk))
			Expect(svc.DefaultFor(ctx, "bob", "image")).To(Equal(wellknown.Default{}))
		})

		It("falls back to the old defaultModel of ui.json for chat, and the editor never writes it", func() {
			svc, f := newService()
			_, err := svc.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"defaultModel": "old-chat"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.DefaultFor(ctx, alice, "chat")).To(Equal(wellknown.Default{Kind: wellknown.DefaultModel, Model: "old-chat", From: "ui.json"}))
			_, err = svc.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Defaults, Values: map[string]any{"chat": "new-chat"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.DefaultFor(ctx, alice, "chat").Model).To(Equal("new-chat"))
			Expect(readRaw(f, alice, "/LocalAI/ui.json")).To(HaveKeyWithValue("defaultModel", "old-chat"))
		})

		It("uses the last good version when the file is broken", func() {
			svc, f := newService()
			_, err := svc.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Defaults, Values: map[string]any{"image": "flux"}})
			Expect(err).NotTo(HaveOccurred())
			raw(f, alice, "/LocalAI/defaults.json", `{"image": 7}`)
			Expect(svc.DefaultFor(ctx, alice, "image").Model).To(Equal("flux"))
		})

		It("refuses a write that breaks the schema and writes nothing", func() {
			svc, _ := newService()
			_, err := svc.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Defaults, Values: map[string]any{"image": 7}})
			var inv *wellknown.InvalidError
			Expect(err).To(BeAssignableToTypeOf(inv))
			Expect(svc.DefaultFor(ctx, alice, "image")).To(Equal(wellknown.Default{}))
		})

		It("removes a key when a save sends null", func() {
			svc, _ := newService()
			_, err := svc.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Defaults, Values: map[string]any{"image": "flux", "tts": "kokoro"}})
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Defaults, Values: map[string]any{"image": nil}})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.DefaultFor(ctx, alice, "image")).To(Equal(wellknown.Default{}))
			Expect(svc.DefaultFor(ctx, alice, "tts").Model).To(Equal("kokoro"))
		})
	})
})
