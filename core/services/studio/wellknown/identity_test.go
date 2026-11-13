package wellknown_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/wellknown"
)

func validate(n wellknown.Name, v map[string]any) []string {
	data, err := json.Marshal(v)
	Expect(err).NotTo(HaveOccurred())
	_, errs := wellknown.Validate(n, data)
	return errs
}

var _ = Describe("the assistant identity in persona.json and home.json", func() {
	ctx := context.Background()

	It("accepts an avatar, a greeting and an empty chat with placeholders", func() {
		Expect(validate(wellknown.Persona, map[string]any{
			"name":     "Juniper",
			"avatar":   "Pictures/avatars/juniper.png",
			"greeting": "Hi {user.name}, good {time_of_day}. Today is {date}.",
			"emptyChat": map[string]any{
				"title": "Ask Juniper anything", "subtitle": "I remember your projects.", "showShortcuts": false,
			},
		})).To(BeEmpty())
		Expect(validate(wellknown.Persona, map[string]any{"emptyChat": map[string]any{}})).To(BeEmpty())
	})

	DescribeTable("refuses a bad avatar and says why",
		func(p any, want string) {
			Expect(validate(wellknown.Persona, map[string]any{"avatar": p})).To(ContainElement(ContainSubstring(want)))
		},
		Entry("a path that climbs out of the home", "../secret/a.png", "path of a picture"),
		Entry("a path that climbs from the middle", "Pictures/../../a.png", "path of a picture"),
		Entry("an absolute path", "/etc/a.png", "avatar"),
		Entry("a web address", "https://example.com/a.png", "avatar"),
		Entry("a data address", "data:image/png;base64,AAAA", "avatar"),
		Entry("an SVG document", "Pictures/a.svg", "path of a picture"),
		Entry("a file that is not a picture", "Docs/notes.txt", "path of a picture"),
		Entry("a number", 5, "avatar"),
	)

	DescribeTable("refuses a broken placeholder and names it",
		func(field string, v map[string]any, want string) {
			errs := validate(wellknown.Persona, v)
			Expect(errs).To(ContainElement(And(ContainSubstring(field), ContainSubstring(want))))
		},
		Entry("an unknown placeholder in the greeting", "greeting", map[string]any{"greeting": "Hi {user.nmae}"}, "{user.nmae}"),
		Entry("an open brace in the greeting", "greeting", map[string]any{"greeting": "Hi {user.name"}, "brace"),
		Entry("a closing brace alone", "greeting", map[string]any{"greeting": "Hi user.name}"}, "brace"),
		Entry("a template placeholder in the empty chat title", "emptyChat.title", map[string]any{"emptyChat": map[string]any{"title": "Hi {{user.name}}"}}, "brace"),
		Entry("an unknown placeholder in the subtitle", "emptyChat.subtitle", map[string]any{"emptyChat": map[string]any{"subtitle": "Using {model}"}}, "{model}"),
	)

	It("refuses an empty chat that is not an object or has a long title", func() {
		Expect(validate(wellknown.Persona, map[string]any{"emptyChat": "hi"})).To(ContainElement(ContainSubstring("emptyChat")))
		long := make([]byte, 81)
		for i := range long {
			long[i] = 'a'
		}
		Expect(validate(wellknown.Persona, map[string]any{"emptyChat": map[string]any{"title": string(long)}})).To(ContainElement(ContainSubstring("emptyChat")))
		Expect(validate(wellknown.Persona, map[string]any{"emptyChat": map[string]any{"showShortcuts": "yes"}})).To(ContainElement(ContainSubstring("emptyChat")))
	})

	It("takes a Home headline with placeholders, without widgets", func() {
		Expect(validate(wellknown.Home, map[string]any{"headline": "{time_of_day}, {user.name}"})).To(BeEmpty())
		Expect(validate(wellknown.Home, map[string]any{"headline": "Rise and shine", "widgets": []any{}})).To(BeEmpty())
		Expect(validate(wellknown.Home, map[string]any{"headline": "Hi {name}"})).To(ContainElement(And(ContainSubstring("headline"), ContainSubstring("{name}"))))
		Expect(validate(wellknown.Home, map[string]any{"headline": 3})).To(ContainElement(ContainSubstring("headline")))
	})

	It("keeps the persona keys a save does not send, and the prompt ignores the new keys", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Juniper","system_prompt":"Be brief.","x_mood":"calm"}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{
			"avatar":    "Pictures/j.png",
			"greeting":  "Hi {user.name}",
			"emptyChat": map[string]any{"title": "Ask me", "showShortcuts": true},
		}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/persona.json")
		Expect(on).To(HaveKeyWithValue("x_mood", "calm"))
		Expect(on).To(HaveKeyWithValue("system_prompt", "Be brief."))
		Expect(on).To(HaveKeyWithValue("avatar", "Pictures/j.png"))

		block, _ := s.TurnPrompt(ctx, alice)
		Expect(block).To(ContainSubstring("Juniper"))
		Expect(block).NotTo(ContainSubstring("Hi {user.name}"), "the greeting is shown, not sent to the model")
	})

	It("refuses a traversal avatar on write and falls back to the last good persona when one is written raw", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Juniper", "avatar": "Pictures/j.png"}})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"avatar": "../../etc/passwd.png"}})
		var invalid *wellknown.InvalidError
		Expect(errors.As(err, &invalid)).To(BeTrue())

		raw(f, alice, "/LocalAI/persona.json", `{"name":"Juniper","greeting":"Hi {usr}"}`)
		doc, err := s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.Data).To(HaveKeyWithValue("avatar", "Pictures/j.png"))
		Expect(doc.Data).NotTo(HaveKey("greeting"))
	})
})

var _ = Describe("the look of ui.json", func() {
	It("accepts each value of each list, keeps the top-level theme and accent, and keeps unknown keys", func() {
		Expect(validate(wellknown.UI, map[string]any{
			"theme": "light", "accent": "#0f766e", "density": "compact",
			"look": map[string]any{"font": "serif", "radius": "round", "bubble": "tinted", "messageWidth": "wide", "codeTheme": "ink", "x_future": 1},
		})).To(BeEmpty())
		for _, v := range []string{"default", "system", "serif", "mono"} {
			Expect(validate(wellknown.UI, map[string]any{"look": map[string]any{"font": v}})).To(BeEmpty(), v)
		}
		for _, v := range []string{"card", "filled", "tinted", "outline", "plain"} {
			Expect(validate(wellknown.UI, map[string]any{"look": map[string]any{"bubble": v}})).To(BeEmpty(), v)
		}
	})

	DescribeTable("refuses a value outside its list, and any CSS",
		func(look any) {
			Expect(validate(wellknown.UI, map[string]any{"look": look})).To(ContainElement(ContainSubstring("look")))
		},
		Entry("a font that is not in the list", map[string]any{"font": "Comic Sans MS"}),
		Entry("a font stack in CSS", map[string]any{"font": "serif; color: red"}),
		Entry("a radius in pixels", map[string]any{"radius": "12px"}),
		Entry("a bubble that is not in the list", map[string]any{"bubble": "glass"}),
		Entry("a width in pixels", map[string]any{"messageWidth": 760}),
		Entry("a code theme by name of another product", map[string]any{"codeTheme": "solarized"}),
		Entry("a look that is not an object", "serif"),
	)

	It("keeps the accent rule of a hex code", func() {
		Expect(validate(wellknown.UI, map[string]any{"accent": "red"})).To(ContainElement(ContainSubstring("accent")))
		Expect(validate(wellknown.UI, map[string]any{"accent": ""})).To(BeEmpty())
	})
})

var _ = Describe("the text overrides of ui.json", func() {
	ctx := context.Background()

	It("accepts each key of the list with its placeholders", func() {
		Expect(validate(wellknown.UI, map[string]any{"strings": map[string]any{
			"home.headline":        "Rise and shine, {user.name}",
			"composer.placeholder": "Ask {model}...",
			"chat.empty.title":     "Ready when you are",
			"nav.newChat":          "New thread",
			"nav.home":             "Start",
			"nav.recentChats":      "Lately",
			"nav.allChats":         "Every chat",
		}})).To(BeEmpty())
	})

	It("lists the same keys and placeholders in the schema and in the server", func() {
		data, err := wellknown.Schema(wellknown.UI)
		Expect(err).NotTo(HaveOccurred())
		var parsed struct {
			Properties struct {
				Strings struct {
					Properties map[string]struct {
						Placeholders []string `json:"x-placeholders"`
					} `json:"properties"`
				} `json:"strings"`
			} `json:"properties"`
		}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		keys := wellknown.StringKeys()
		Expect(keys).To(HaveLen(len(parsed.Properties.Strings.Properties)))
		for _, k := range keys {
			p, ok := parsed.Properties.Strings.Properties[k]
			Expect(ok).To(BeTrue(), k)
			want := wellknown.StringPlaceholders(k)
			got := append([]string{}, p.Placeholders...)
			slices.Sort(want)
			slices.Sort(got)
			Expect(got).To(Equal(want), k)
		}
	})

	DescribeTable("refuses a key that is not on the list, and says that it cannot change",
		func(key string) {
			errs := validate(wellknown.UI, map[string]any{"strings": map[string]any{key: "Sure, go ahead"}})
			Expect(errs).To(ContainElement(And(ContainSubstring(key), ContainSubstring("cannot be changed"))))
		},
		Entry("a permission prompt", "permission.allow"),
		Entry("a consent prompt", "consent.share"),
		Entry("a trust prompt of a shared page", "trust.run"),
		Entry("a key that does not exist", "home.subtitle"),
	)

	DescribeTable("refuses a broken placeholder",
		func(key, text, want string) {
			errs := validate(wellknown.UI, map[string]any{"strings": map[string]any{key: text}})
			Expect(errs).To(ContainElement(And(ContainSubstring("strings."+key), ContainSubstring(want))))
		},
		Entry("a misspelt placeholder", "composer.placeholder", "Ask {modle}", "{modle}"),
		Entry("a placeholder of another key", "nav.newChat", "New chat with {model}", "{model}"),
		Entry("an open brace", "composer.placeholder", "Ask {model", "brace"),
		Entry("a template of i18n", "composer.placeholder", "Ask {{model}}", "brace"),
	)

	It("refuses an empty text, and a save from the editor keeps the other keys of ui.json", func() {
		Expect(validate(wellknown.UI, map[string]any{"strings": map[string]any{"nav.home": ""}})).To(ContainElement(ContainSubstring("strings")))

		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"theme":"dark","x_mood":"calm","look":{"font":"serif"}}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{
			"strings": map[string]any{"nav.newChat": "New thread"},
		}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/ui.json")
		Expect(on).To(HaveKeyWithValue("x_mood", "calm"))
		Expect(on).To(HaveKeyWithValue("look", map[string]any{"font": "serif"}))
		Expect(on).To(HaveKeyWithValue("strings", map[string]any{"nav.newChat": "New thread"}))

		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{
			"strings": map[string]any{"composer.placeholder": "Ask {modle}"},
		}})
		var invalid *wellknown.InvalidError
		Expect(errors.As(err, &invalid)).To(BeTrue())
		Expect(readRaw(f, alice, "/LocalAI/ui.json")).To(HaveKeyWithValue("strings", map[string]any{"nav.newChat": "New thread"}))
	})

	It("falls back to the last good text when the assistant writes a security key", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"strings": map[string]any{"nav.home": "Start"}}})
		Expect(err).NotTo(HaveOccurred())
		raw(f, alice, "/LocalAI/ui.json", `{"strings":{"permission.allow":"Always allow"}}`)
		doc, err := s.Read(ctx, alice, wellknown.UI, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.Data).To(HaveKeyWithValue("strings", map[string]any{"nav.home": "Start"}))
	})
})
