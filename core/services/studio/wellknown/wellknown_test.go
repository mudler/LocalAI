package wellknown_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

const alice = "alice"

func newService() (*wellknown.Service, *files.Service) {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "workspace"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	f := files.New(ws, files.WithBlobs(blobs))
	Expect(f.EnsureHome(context.Background(), alice)).To(Succeed())
	Expect(f.EnsureHome(context.Background(), "bob")).To(Succeed())
	return wellknown.New(f), f
}

// raw writes bytes the way the assistant's file tools do: no validation.
func raw(f *files.Service, who, rel, body string) {
	abs, err := f.Abs(who, rel)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.Store().Put(context.Background(), f.Actor(who), abs, strings.NewReader(body), workspace.PutOptions{Overwrite: true, Title: "agent edit"})
	Expect(err).NotTo(HaveOccurred())
}

func readRaw(f *files.Service, who, rel string) map[string]any {
	abs, _ := f.Abs(who, rel)
	r, _, err := f.Store().Open(context.Background(), f.Actor(who), abs)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = r.Close() }()
	var m map[string]any
	Expect(json.NewDecoder(r).Decode(&m)).To(Succeed())
	return m
}

func base(v int64) *int64 { return &v }

var _ = Describe("well-known files", func() {
	ctx := context.Background()

	It("ships one schema per file, in the built-in skill, readable by the validator", func() {
		for _, n := range wellknown.Names() {
			data, err := wellknown.Schema(n)
			Expect(err).NotTo(HaveOccurred(), string(n))
			Expect(string(data)).To(ContainSubstring("https://json-schema.org/draft/2020-12/schema"), string(n))
			fromSkill, err := fs.ReadFile(builtinskills.FS(), "localai-workspace/schemas/"+string(n)+".schema.json")
			Expect(err).NotTo(HaveOccurred(), string(n))
			Expect(fromSkill).To(Equal(data), "the skill and the server must ship the same schema for %s", n)
		}
		_, err := wellknown.Schema("nope")
		Expect(err).To(MatchError(wellknown.ErrUnknown))
	})

	Describe("setups", func() {
		setup := func(id, name string) map[string]any {
			return map[string]any{"id": id, "name": name}
		}
		doc := func(setups ...any) map[string]any { return map[string]any{"setups": setups} }
		bad := func(v any) []string {
			data, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			obj, errs := wellknown.Validate(wellknown.Setups, data)
			Expect(obj).To(BeNil())
			return errs
		}

		It("is a kind with a fixed path", func() {
			n, err := wellknown.Parse("setups")
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(wellknown.Setups))
			Expect(wellknown.File(wellknown.Setups)).To(Equal("/LocalAI/setups.json"))
		})

		It("accepts a document with two setups", func() {
			ok := map[string]any{
				"default": "coding",
				"setups": []any{
					map[string]any{"id": "coding", "name": "Coding", "model": "m1", "effort": "high",
						"tools": map[string]any{"builtins_off": []any{"web_search"}, "builtins_on": []any{"x"}, "addons_off": []any{"a"}, "mcp_off": []any{"b"}, "skills_off": []any{"c"}}},
					map[string]any{"id": "quick-2", "name": "Quick", "effort": "", "tools": map[string]any{}},
				},
			}
			data, err := json.Marshal(ok)
			Expect(err).NotTo(HaveOccurred())
			obj, errs := wellknown.Validate(wellknown.Setups, data)
			Expect(errs).To(BeEmpty())
			Expect(obj).To(HaveKey("setups"))
		})

		It("refuses 51 setups and accepts 50", func() {
			many := make([]any, 51)
			for i := range many {
				many[i] = setup(fmt.Sprintf("s%d", i), "S")
			}
			Expect(bad(doc(many...))).To(ContainElement(ContainSubstring("setups")))
			data, err := json.Marshal(doc(many[:50]...))
			Expect(err).NotTo(HaveOccurred())
			_, errs := wellknown.Validate(wellknown.Setups, data)
			Expect(errs).To(BeEmpty())
		})

		It("refuses a name of 61 characters and accepts one of 60", func() {
			Expect(bad(doc(setup("a", strings.Repeat("n", 61))))).To(ContainElement(ContainSubstring("name")))
			_, errs := wellknown.Validate(wellknown.Setups, []byte(`{"setups":[{"id":"a","name":"`+strings.Repeat("n", 60)+`"}]}`))
			Expect(errs).To(BeEmpty())
		})

		It("accepts an id of 40 characters and refuses one of 41", func() {
			data, err := json.Marshal(doc(setup(strings.Repeat("a", 40), "A")))
			Expect(err).NotTo(HaveOccurred())
			_, errs := wellknown.Validate(wellknown.Setups, data)
			Expect(errs).To(BeEmpty())
			Expect(bad(doc(setup(strings.Repeat("a", 41), "A")))).To(ContainElement(ContainSubstring("id")))
		})

		DescribeTable("refuses, naming what is wrong",
			func(mutate func(m map[string]any) map[string]any, want string) {
				Expect(bad(mutate(doc(setup("a", "A"))))).To(ContainElement(ContainSubstring(want)))
			},
			Entry("an unknown key at the top", func(m map[string]any) map[string]any { m["extra"] = 1; return m }, "extra"),
			Entry("an unknown key in a setup", func(m map[string]any) map[string]any {
				m["setups"].([]any)[0].(map[string]any)["extra"] = 1
				return m
			}, "extra"),
			Entry("an unknown key in tools", func(m map[string]any) map[string]any {
				m["setups"].([]any)[0].(map[string]any)["tools"] = map[string]any{"nope": []any{"x"}}
				return m
			}, "nope"),
			Entry("an id with an uppercase letter", func(m map[string]any) map[string]any {
				m["setups"].([]any)[0].(map[string]any)["id"] = "Coding"
				return m
			}, "id"),
			Entry("an empty id", func(m map[string]any) map[string]any {
				m["setups"].([]any)[0].(map[string]any)["id"] = ""
				return m
			}, "id"),
			Entry("an effort that is not in the list", func(m map[string]any) map[string]any {
				m["setups"].([]any)[0].(map[string]any)["effort"] = "extreme"
				return m
			}, "effort"),
			Entry("more than 200 names in a tool list", func(m map[string]any) map[string]any {
				names := make([]any, 201)
				for i := range names {
					names[i] = "t"
				}
				m["setups"].([]any)[0].(map[string]any)["tools"] = map[string]any{"mcp_off": names}
				return m
			}, "mcp_off"),
		)

		It("refuses two setups with the same id, in words", func() {
			Expect(bad(doc(setup("a", "A"), setup("b", "B"), setup("a", "Again")))).To(ContainElement(
				And(ContainSubstring("setups[2]"), ContainSubstring(`"a"`), ContainSubstring("setups[0]"), ContainSubstring("own id"))))
		})

		It("refuses a default that names no setup, in words, and accepts none or an empty one", func() {
			m := doc(setup("a", "A"))
			m["default"] = "gone"
			Expect(bad(m)).To(ContainElement(And(ContainSubstring("default"), ContainSubstring(`"gone"`), ContainSubstring("leave default out"))))
			for _, d := range []any{"", "a"} {
				m["default"] = d
				data, err := json.Marshal(m)
				Expect(err).NotTo(HaveOccurred())
				_, errs := wellknown.Validate(wellknown.Setups, data)
				Expect(errs).To(BeEmpty(), "default %v", d)
			}
			delete(m, "default")
			data, err := json.Marshal(m)
			Expect(err).NotTo(HaveOccurred())
			_, errs := wellknown.Validate(wellknown.Setups, data)
			Expect(errs).To(BeEmpty())
		})
	})

	It("reads a missing file as empty, not as an error", func() {
		s, _ := newService()
		doc, err := s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Exists).To(BeFalse())
		Expect(doc.Valid).To(BeTrue())
		Expect(doc.Path).To(Equal("/LocalAI/persona.json"))
		Expect(doc.Data).To(BeEmpty())
	})

	It("writes a file, reads it back, and keeps the keys the editor did not send", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Ada","system_prompt":"be brief","x_mood":"calm"}`)
		cur, err := s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		doc, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Ada Lovelace"}, Base: base(cur.Version)})
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Data).To(HaveKeyWithValue("name", "Ada Lovelace"))
		Expect(doc.Version).To(BeNumerically(">", cur.Version))
		on := readRaw(f, alice, "/LocalAI/persona.json")
		Expect(on).To(HaveKeyWithValue("x_mood", "calm"))
		Expect(on).To(HaveKeyWithValue("system_prompt", "be brief"))
	})

	It("removes a key that the editor sends as null", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.User, Values: map[string]any{"name": "Marta", "bio": "designer"}})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.User, Values: map[string]any{"bio": nil}})
		Expect(err).NotTo(HaveOccurred())
		Expect(readRaw(f, alice, "/LocalAI/user.json")).NotTo(HaveKey("bio"))
	})

	It("replaces the whole file from the source view", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Ada","x_mood":"calm"}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Bea"}, Replace: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(readRaw(f, alice, "/LocalAI/persona.json")).To(Equal(map[string]any{"name": "Bea"}))
	})

	It("refuses a write that breaks the schema and lists why", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Shortcuts, Values: map[string]any{"shortcuts": []any{map[string]any{"label": ""}}}})
		var inv *wellknown.InvalidError
		Expect(errors.As(err, &inv)).To(BeTrue(), "%v", err)
		Expect(inv.Errors).NotTo(BeEmpty())
		abs, _ := f.Abs(alice, "/LocalAI/shortcuts.json")
		_, err = f.Store().Stat(ctx, f.Actor(alice), abs)
		Expect(err).To(MatchError(workspace.ErrNotFound), "nothing is written")
	})

	It("keeps the record of what was shown and offered under seen in user.json", func() {
		s, _ := newService()
		seen := map[string]any{
			"home_examples": map[string]any{"hidden": "2026-10-30"},
			"offers_off":    false,
			"new_off":       true,
			"last_offer":    "2026-10-30T09:12:00Z",
			"offer_chats":   []any{"c1"},
			"offers":        map[string]any{"profile": map[string]any{"state": "later", "at": "2026-10-30", "count": 1}},
			"new":           map[string]any{"pages": "2026-10-30"},
		}
		doc, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.User, Values: map[string]any{"seen": seen}})
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeTrue())

		By("refusing an answer that is not one of the states")
		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.User, Values: map[string]any{
			"seen": map[string]any{"offers": map[string]any{"profile": map[string]any{"state": "maybe"}}},
		}})
		var inv *wellknown.InvalidError
		Expect(errors.As(err, &inv)).To(BeTrue(), "%v", err)

		By("keeping a record in the older flat shape valid")
		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.User, Values: map[string]any{
			"seen": map[string]any{"profile": map[string]any{"state": "declined"}},
		}})
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a file over the size cap", func() {
		s, _ := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"x_blob": strings.Repeat("x", wellknown.MaxSize)}})
		Expect(err).To(MatchError(wellknown.ErrTooLarge))
	})

	It("answers a conflict with the newer values when the base is old", func() {
		s, f := newService()
		first, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Ada"}, Base: base(0)})
		Expect(err).NotTo(HaveOccurred())
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Changed by the assistant"}`)
		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Mine"}, Base: base(first.Version)})
		var cf *wellknown.ConflictError
		Expect(errors.As(err, &cf)).To(BeTrue(), "%v", err)
		Expect(cf.Current.Data).To(HaveKeyWithValue("name", "Changed by the assistant"))
		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Mine"}, Base: base(0)})
		Expect(errors.As(err, &cf)).To(BeTrue(), "base 0 means the file must not exist")
	})

	It("answers a conflict from the commit race with the newer file too, so a client can re-base", func() {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "workspace"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		armed := false
		f := files.New(racing{Store: ws, armed: &armed}, files.WithBlobs(blobs))
		Expect(f.EnsureHome(ctx, alice)).To(Succeed())
		s := wellknown.New(f)
		first, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Ada"}, Base: base(0)})
		Expect(err).NotTo(HaveOccurred())
		armed = true
		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"name": "Mine"}, Base: base(first.Version)})
		var cf *wellknown.ConflictError
		Expect(errors.As(err, &cf)).To(BeTrue(), "%v", err)
		Expect(cf.Current.Exists).To(BeTrue())
		Expect(cf.Current.Version).To(Equal(first.Version))
		Expect(cf.Current.Data).To(HaveKeyWithValue("name", "Ada"))
	})

	It("lists the versions of a file, newest first, and reverts to any valid one as a new change", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Ada"}`)
		raw(f, alice, "/LocalAI/persona.json", `{"name": 42}`)
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Bea"}`)
		vs, err := s.Versions(ctx, alice, wellknown.Persona, "", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(vs).To(HaveLen(3))
		Expect(vs[0].Data).To(HaveKeyWithValue("name", "Bea"))
		Expect(vs[1].Valid).To(BeFalse())
		Expect(vs[1].Errors).NotTo(BeEmpty())
		Expect(vs[2].Data).To(HaveKeyWithValue("name", "Ada"))
		Expect(vs[0].Rev).To(BeNumerically(">", vs[2].Rev))

		doc, err := s.RevertTo(ctx, alice, wellknown.Persona, "", vs[2].Rev)
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Data).To(HaveKeyWithValue("name", "Ada"))
		after, err := s.Versions(ctx, alice, wellknown.Persona, "", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(HaveLen(4), "the revert is a new version; the newer one stays in the list")

		_, err = s.RevertTo(ctx, alice, wellknown.Persona, "", vs[1].Rev)
		Expect(err).To(MatchError(wellknown.ErrNoPrevious), "an invalid version cannot come back")
		_, err = s.RevertTo(ctx, alice, wellknown.Persona, "", 999999)
		Expect(err).To(MatchError(wellknown.ErrNoPrevious))
	})

	It("shows the last good version when the file on disk is broken", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Ada"}`)
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Bea"}`)
		raw(f, alice, "/LocalAI/persona.json", `{"name": 42}`)
		doc, err := s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Errors).NotTo(BeEmpty())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.FallbackRev).To(BeNumerically(">", 0))
		Expect(doc.Data).To(HaveKeyWithValue("name", "Bea"))

		raw(f, alice, "/LocalAI/persona.json", `not json`)
		doc, err = s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Data).To(HaveKeyWithValue("name", "Bea"))
	})

	It("reverts to the previous good version, as a new change", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Ada"}`)
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Bea"}`)
		doc, err := s.Revert(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Data).To(HaveKeyWithValue("name", "Ada"))
		Expect(readRaw(f, alice, "/LocalAI/persona.json")).To(HaveKeyWithValue("name", "Ada"))

		raw(f, alice, "/LocalAI/persona.json", `{"name": 1}`)
		doc, err = s.Revert(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeTrue())
		Expect(doc.Data).To(HaveKeyWithValue("name", "Ada"), "a broken file goes back to the last good one")

		_, err = wellknown.New(f).Revert(ctx, "bob", wellknown.Persona, "")
		Expect(err).To(MatchError(wellknown.ErrNoPrevious))
	})

	It("keeps widget data out of the settings folder, so it cannot overwrite a settings file", func() {
		s, f := newService()
		for _, p := range []string{"/LocalAI/setups.json", "LocalAI/persona.json", "/LocalAI/data/x.json"} {
			_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.WidgetData, Path: p, Values: map[string]any{"items": []any{}}})
			Expect(err).To(MatchError(wellknown.ErrBadPath), p)
			_, err = s.Read(ctx, alice, wellknown.WidgetData, p)
			Expect(err).To(MatchError(wellknown.ErrBadPath), p)
		}
		Expect(exists(f, alice, "/LocalAI/setups.json")).To(BeFalse())
		// A data folder of the same name elsewhere is fine.
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.WidgetData, Path: "/data/LocalAI.json", Values: map[string]any{"items": []any{}}})
		Expect(err).NotTo(HaveOccurred())
	})

	It("reads widget data from a path in the home and nowhere else", func() {
		s, f := newService()
		raw(f, alice, "/data/tasks.json", `{"title":"Open tasks","items":[{"label":"Call the dentist","meta":"Fri"}]}`)
		doc, err := s.Read(ctx, alice, wellknown.WidgetData, "data/tasks.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Path).To(Equal("/data/tasks.json"))
		Expect(doc.Data["items"]).To(HaveLen(1))
		for _, bad := range []string{"", "../bob/x.json", "/data/tasks.txt"} {
			_, err := s.Read(ctx, alice, wellknown.WidgetData, bad)
			Expect(err).To(HaveOccurred(), bad)
		}
		doc, err = s.Read(ctx, "bob", wellknown.WidgetData, "data/tasks.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Exists).To(BeFalse(), "bob's path names his own home")
	})
})

var _ = Describe("the turn prompt", func() {
	ctx := context.Background()

	It("adds nothing when the files are absent", func() {
		s, _ := newService()
		block, key := s.TurnPrompt(ctx, alice)
		Expect(block).To(BeEmpty())
		Expect(key).To(BeEmpty())
	})

	It("adds the persona and a short block about the user", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Ada","system_prompt":"Keep answers short."}`)
		raw(f, alice, "/LocalAI/user.json", `{"name":"Marta","bio":"Product designer in Milan.","language":"it","timezone":"Europe/Rome","seen":{"bio":{"state":"declined"}}}`)
		block, key := s.TurnPrompt(ctx, alice)
		Expect(key).NotTo(BeEmpty())
		Expect(block).To(ContainSubstring("Ada"))
		Expect(block).To(ContainSubstring("Keep answers short."))
		Expect(block).To(ContainSubstring("About the user"))
		for _, want := range []string{"Marta", "Product designer in Milan.", "it", "Europe/Rome"} {
			Expect(block).To(ContainSubstring(want))
		}
		Expect(block).NotTo(ContainSubstring("declined"), "the hint record stays out of the prompt")
		Expect(strings.Index(block, "Keep answers short.")).To(BeNumerically("<", strings.Index(block, "About the user")))

		raw(f, alice, "/LocalAI/persona.json", `{"name":"Bea"}`)
		block2, key2 := s.TurnPrompt(ctx, alice)
		Expect(key2).NotTo(Equal(key))
		Expect(block2).To(ContainSubstring("Bea"))
	})

	It("caps the block about the user", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/user.json", `{"name":"Marta","bio":"`+strings.Repeat("long words ", 90)+`"}`)
		block, _ := s.TurnPrompt(ctx, alice)
		Expect(len(block)).To(BeNumerically("<=", wellknown.MaxUserBlock))
	})

	It("uses the last good persona when the file is broken", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"Ada"}`)
		raw(f, alice, "/LocalAI/persona.json", `{"name":["broken"]}`)
		block, _ := s.TurnPrompt(ctx, alice)
		Expect(block).To(ContainSubstring("Ada"))
	})
})

var _ = Describe("widget data and home widgets", func() {
	DescribeTable("takes an item url that is an http(s) address or a path of the app",
		func(url string, ok bool) {
			doc, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"label": "a", "url": url}}})
			_, errs := wellknown.Validate(wellknown.WidgetData, doc)
			Expect(wellknown.ItemURLOK(url)).To(Equal(ok), "the code and the schema agree")
			if ok {
				Expect(errs).To(BeEmpty())
				return
			}
			Expect(errs).NotTo(BeEmpty())
			Expect(strings.Join(errs, "\n")).To(ContainSubstring("items[0].url: a link is an http or https address"))
		},
		Entry("https", "https://news.example.com/a?b=1#c", true),
		Entry("http with a port", "http://example.com:8080", true),
		Entry("a page", "/app/pages/news", true),
		Entry("the chats", "/app/chats", true),
		Entry("a chat", "/app/chat/model-x?x=1", true),
		Entry("javascript", "javascript:alert(1)", false),
		Entry("data", "data:text/html,x", false),
		Entry("protocol relative", "//example.com/x", false),
		Entry("credentials", "https://user:pass@example.com/", false),
		Entry("a backslash in the host", `https://example.com\@evil.test/`, false),
		Entry("relative", "pages/news", false),
		Entry("another app path", "/app/users", false),
		Entry("a prefix that only starts the same", "/app/pagesx", false),
		Entry("traversal", "/app/pages/../users", false),
		Entry("a double slash", "/app/pages//evil.example", false),
		Entry("outside the app", "/api/chats/files", false),
		Entry("whitespace", "https://example.com/a b", false),
		Entry("empty", "", false),
	)

	It("says which item has a bad url, among other errors", func() {
		_, errs := wellknown.Validate(wellknown.WidgetData, []byte(`{"items":[{"label":"ok","url":"https://example.com"},{"label":"x","url":"ftp://x"},{"label":"y","url":3}]}`))
		joined := strings.Join(errs, "\n")
		Expect(joined).To(ContainSubstring(`items[1].url:`))
		Expect(joined).To(ContainSubstring(`"ftp://x" is neither`))
		Expect(joined).To(ContainSubstring(`items[2].url:`))
		Expect(joined).NotTo(ContainSubstring(`items[0]`))
	})

	It("checks the time and the source of widget data", func() {
		good := `{"title":"Inbox","updatedAt":"2026-10-30T07:59:00Z","source":{"kind":"trigger","trigger":"a1b2c3","note":"Mail from the last 24 hours"},` +
			`"items":[{"label":"3 need a reply"}]}`
		_, errs := wellknown.Validate(wellknown.WidgetData, []byte(good))
		Expect(errs).To(BeEmpty())
		_, errs = wellknown.Validate(wellknown.WidgetData, []byte(`{"updatedAt":"2026-10-30T07:59:00+01:00","source":{"kind":"page","page":"tasks"},"items":[]}`))
		Expect(errs).To(BeEmpty(), "a time with an offset and a page source")
		for name, bad := range map[string]string{
			"a time that is not RFC 3339": `{"updatedAt":"yesterday","items":[]}`,
			"a source without a kind":     `{"source":{"trigger":"a"},"items":[]}`,
			"an unknown kind":             `{"source":{"kind":"cron"},"items":[]}`,
			"an unknown source key":       `{"source":{"kind":"you","url":"https://example.com"},"items":[]}`,
			"a page name with a slash":    `{"source":{"kind":"page","page":"../x"},"items":[]}`,
		} {
			_, errs := wellknown.Validate(wellknown.WidgetData, []byte(bad))
			Expect(errs).NotTo(BeEmpty(), name)
		}
	})

	It("takes a page widget with a page name, a title it may leave out and a shared folder", func() {
		_, errs := wellknown.Validate(wellknown.Home, []byte(`{"widgets":[{"id":"clock","type":"page","page":"clock"},`+
			`{"id":"news","type":"page","page":"news-ticker","title":"News","folder":42},{"id":"t","type":"list","title":"Tasks","source":"data/t.json"}]}`))
		Expect(errs).To(BeEmpty())
	})

	It("keeps the title required for the other widgets", func() {
		_, errs := wellknown.Validate(wellknown.Home, []byte(`{"widgets":[{"id":"c","type":"chats"}]}`))
		Expect(errs).NotTo(BeEmpty())
	})

	It("takes at most three page widgets and says why in words", func() {
		w := func(n int) string {
			var parts []string
			for i := range n {
				parts = append(parts, fmt.Sprintf(`{"id":"p%d","type":"page","page":"p%d"}`, i, i))
			}
			return `{"widgets":[` + strings.Join(parts, ",") + `,{"id":"c","type":"chats","title":"Chats"}]}`
		}
		_, errs := wellknown.Validate(wellknown.Home, []byte(w(3)))
		Expect(errs).To(BeEmpty())
		_, errs = wellknown.Validate(wellknown.Home, []byte(w(4)))
		Expect(strings.Join(errs, "\n")).To(ContainSubstring("widgets: Home shows at most 3 page widgets, because each one runs its page; this layout has 4, so remove 1"))
	})

	DescribeTable("refuses a page widget without a page name, in words",
		func(widget, want string) {
			_, errs := wellknown.Validate(wellknown.Home, []byte(`{"widgets":[{"id":"ok","type":"chats","title":"Chats"},`+widget+`]}`))
			Expect(strings.Join(errs, "\n")).To(ContainSubstring(want))
		},
		Entry("no page", `{"id":"p","type":"page"}`, `widgets[1].page: a page widget names its page`),
		Entry("an empty page", `{"id":"p","type":"page","page":""}`, `widgets[1].page: a page widget names its page`),
		Entry("a path", `{"id":"p","type":"page","page":"../x"}`, `widgets[1].page: "../x" is not a page name`),
		Entry("capitals", `{"id":"p","type":"page","page":"News"}`, `widgets[1].page: "News" is not a page name`),
		Entry("a number", `{"id":"p","type":"page","page":7}`, `widgets[1].page: "7" is not a page name`),
	)

	It("refuses a shared folder that is not a folder id", func() {
		_, errs := wellknown.Validate(wellknown.Home, []byte(`{"widgets":[{"id":"p","type":"page","page":"news","folder":"team"}]}`))
		Expect(errs).NotTo(BeEmpty())
	})

	It("lets a home widget name the trigger that feeds it and keeps other keys", func() {
		_, errs := wellknown.Validate(wellknown.Home, []byte(`{"widgets":[{"id":"inbox","type":"list","title":"Inbox","source":"data/inbox.json","trigger":"0f3c9a","note":"kept"}]}`))
		Expect(errs).To(BeEmpty())
		_, errs = wellknown.Validate(wellknown.Home, []byte(`{"widgets":[{"id":"inbox","type":"list","title":"Inbox","source":"data/inbox.json","trigger":"../x"}]}`))
		Expect(errs).NotTo(BeEmpty(), "a trigger id with a path")
	})
})
