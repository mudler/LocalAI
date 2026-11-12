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

	DescribeTable("takes the sizes s, m and l on every type of widget and refuses another",
		func(widget string, ok bool) {
			_, errs := wellknown.Validate(wellknown.Home, []byte(`{"widgets":[`+widget+`]}`))
			if ok {
				Expect(errs).To(BeEmpty())
			} else {
				Expect(errs).NotTo(BeEmpty())
			}
		},
		Entry("a list, s", `{"id":"a","type":"list","title":"A","source":"data/a.json","size":"s"}`, true),
		Entry("a metric, m", `{"id":"a","type":"metric","title":"A","source":"data/a.json","size":"m"}`, true),
		Entry("chats, l", `{"id":"a","type":"chats","title":"Chats","size":"l"}`, true),
		Entry("a page, l", `{"id":"a","type":"page","page":"news","size":"l"}`, true),
		Entry("no size, as before", `{"id":"a","type":"list","title":"A","source":"data/a.json"}`, true),
		Entry("a page size word", `{"id":"a","type":"page","page":"news","size":"medium"}`, false),
		Entry("a capital", `{"id":"a","type":"chats","title":"Chats","size":"M"}`, false),
		Entry("a number", `{"id":"a","type":"chats","title":"Chats","size":2}`, false),
	)

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

	DescribeTable("takes a pageArea that is null, a page name or a page of a shared folder",
		func(area string) {
			_, errs := wellknown.Validate(wellknown.Home, []byte(`{"pageArea":`+area+`,"widgets":[]}`))
			Expect(errs).To(BeEmpty())
		},
		Entry("null", `null`),
		Entry("a page of your home", `"today"`),
		Entry("a page of a shared folder", `{"page":"today","folder":3}`),
		Entry("an object without a folder", `{"page":"today"}`),
	)

	It("takes a home.json without a pageArea, as before", func() {
		_, errs := wellknown.Validate(wellknown.Home, []byte(`{"widgets":[]}`))
		Expect(errs).To(BeEmpty())
	})

	DescribeTable("refuses a pageArea that is not a page, in words",
		func(area, want string) {
			_, errs := wellknown.Validate(wellknown.Home, []byte(`{"pageArea":`+area+`}`))
			Expect(strings.Join(errs, "\n")).To(ContainSubstring(want))
		},
		Entry("a path", `"../x"`, `pageArea: "../x" is not a page name`),
		Entry("capitals", `"Today"`, `pageArea: "Today" is not a page name`),
		Entry("an empty name", `""`, `pageArea: "" is not a page name`),
		Entry("a number", `7`, `pageArea names a page`),
		Entry("a list", `["today"]`, `pageArea names a page`),
		Entry("an object without a page", `{"folder":3}`, `pageArea.page:`),
		Entry("a folder that is not an id", `{"page":"today","folder":"team"}`, `pageArea.folder:`),
		Entry("a folder below one", `{"page":"today","folder":0}`, `pageArea.folder:`),
		Entry("a key it does not know", `{"page":"today","size":"l"}`, `pageArea.size: no such key`),
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

var _ = Describe("the Home background of ui.json", func() {
	ctx := context.Background()
	check := func(bg any) []string {
		data, err := json.Marshal(map[string]any{"theme": "dark", "homeBackground": bg})
		Expect(err).NotTo(HaveOccurred())
		_, errs := wellknown.Validate(wellknown.UI, data)
		return errs
	}

	It("accepts a colour, a gradient and a picture with its tuning", func() {
		Expect(check(map[string]any{"color": "#1e293b"})).To(BeEmpty())
		Expect(check(map[string]any{"gradient": map[string]any{"from": "#0f172a", "to": "#3b82f6", "angle": 135}})).To(BeEmpty())
		Expect(check(map[string]any{"image": "Pictures/sky.JPG", "fit": "contain", "opacity": 0.8, "blur": 12, "overlay": 0.5})).To(BeEmpty())
		Expect(check(map[string]any{})).To(BeEmpty())
	})

	It("keeps keys it does not know inside the background", func() {
		Expect(check(map[string]any{"color": "#112233", "x_future": true})).To(BeEmpty())
	})

	DescribeTable("refuses a bad value",
		func(bg map[string]any, want string) {
			Expect(check(bg)).To(ContainElement(ContainSubstring(want)))
		},
		Entry("a colour that is not a hex code", map[string]any{"color": "red"}, "homeBackground"),
		Entry("a gradient without an end", map[string]any{"gradient": map[string]any{"from": "#000000"}}, "homeBackground"),
		Entry("a gradient with a CSS function in it", map[string]any{"gradient": map[string]any{"from": "url(x)", "to": "#000000"}}, "homeBackground"),
		Entry("an angle above 360", map[string]any{"gradient": map[string]any{"from": "#000000", "to": "#ffffff", "angle": 400}}, "homeBackground"),
		Entry("a fit that is not in the list", map[string]any{"fit": "stretch"}, "homeBackground"),
		Entry("an opacity above 1", map[string]any{"opacity": 2}, "homeBackground"),
		Entry("a blur above 40", map[string]any{"blur": 41}, "homeBackground"),
		Entry("a path that climbs out of the home", map[string]any{"image": "../secret/a.png"}, "path of a picture"),
		Entry("a path that climbs from the middle", map[string]any{"image": "Pictures/../../a.png"}, "path of a picture"),
		Entry("an absolute path", map[string]any{"image": "/etc/a.png"}, "homeBackground.image"),
		Entry("a web address", map[string]any{"image": "https://example.com/a.png"}, "homeBackground.image"),
		Entry("a data address", map[string]any{"image": "data:image/png;base64,AAAA"}, "homeBackground.image"),
		Entry("a backslash path", map[string]any{"image": `Pictures\a.png`}, "homeBackground.image"),
		Entry("a file that is not a picture", map[string]any{"image": "Docs/notes.txt"}, "path of a picture"),
		Entry("an SVG document", map[string]any{"image": "Pictures/a.svg"}, "path of a picture"),
		Entry("a query string", map[string]any{"image": "a.png?x=1"}, "homeBackground.image"),
		Entry("a path that is not a string", map[string]any{"image": 5}, "homeBackground"),
	)

	DescribeTable("BackgroundImageOK",
		func(p string, want bool) { Expect(wellknown.BackgroundImageOK(p)).To(Equal(want)) },
		Entry("a file at the top", "sky.png", true),
		Entry("a file in a folder, upper-case extension", "Pictures/Sky.AVIF", true),
		Entry("a name with spaces", "My pictures/blue sky.webp", true),
		Entry("empty", "", false),
		Entry("dot segment", "./a.png", false),
		Entry("double slash", "a//b.png", false),
		Entry("trailing slash", "a.png/", false),
		Entry("control character", "a\n.png", false),
		Entry("no extension", "Pictures/sky", false),
	)

	It("saves from the editor, keeps the other keys and removes the background when cleared", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"theme":"dark","x_mood":"calm"}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{
			"homeBackground": map[string]any{"image": "Pictures/sky.jpg", "fit": "cover"},
		}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/ui.json")
		Expect(on).To(HaveKeyWithValue("x_mood", "calm"))
		Expect(on).To(HaveKeyWithValue("theme", "dark"))
		Expect(on["homeBackground"]).To(HaveKeyWithValue("image", "Pictures/sky.jpg"))

		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"homeBackground": nil}})
		Expect(err).NotTo(HaveOccurred())
		Expect(readRaw(f, alice, "/LocalAI/ui.json")).NotTo(HaveKey("homeBackground"))
	})

	It("refuses a write with a traversal path and leaves the file as it was", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"homeBackground":{"color":"#112233"}}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{
			"homeBackground": map[string]any{"image": "../../etc/passwd.png"},
		}})
		var invalid *wellknown.InvalidError
		Expect(errors.As(err, &invalid)).To(BeTrue())
		Expect(readRaw(f, alice, "/LocalAI/ui.json")).To(HaveKeyWithValue("homeBackground", map[string]any{"color": "#112233"}))
	})

	It("shows the last good version when the assistant writes a broken background", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"homeBackground": map[string]any{"color": "#112233"}}})
		Expect(err).NotTo(HaveOccurred())
		raw(f, alice, "/LocalAI/ui.json", `{"homeBackground":{"image":"../a.png"}}`)
		doc, err := s.Read(ctx, alice, wellknown.UI, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.Data).To(HaveKeyWithValue("homeBackground", map[string]any{"color": "#112233"}))
	})
})

var _ = Describe("the chat background of ui.json", func() {
	ctx := context.Background()
	check := func(bg any) []string {
		data, err := json.Marshal(map[string]any{"theme": "dark", "chatBackground": bg})
		Expect(err).NotTo(HaveOccurred())
		_, errs := wellknown.Validate(wellknown.UI, data)
		return errs
	}

	It("takes the same values as the Home background", func() {
		Expect(check(map[string]any{"color": "#1e293b"})).To(BeEmpty())
		Expect(check(map[string]any{"gradient": map[string]any{"from": "#0f172a", "to": "#3b82f6", "angle": 135}})).To(BeEmpty())
		Expect(check(map[string]any{"image": "Pictures/sky.JPG", "fit": "contain", "opacity": 0.8, "blur": 12, "overlay": 0.5})).To(BeEmpty())
		Expect(check(map[string]any{"color": "#112233", "x_future": true})).To(BeEmpty())
	})

	DescribeTable("refuses a bad value and names the key",
		func(bg map[string]any) {
			Expect(check(bg)).To(ContainElement(ContainSubstring("chatBackground")))
		},
		Entry("a colour that is not a hex code", map[string]any{"color": "red"}),
		Entry("a gradient with a CSS function in it", map[string]any{"gradient": map[string]any{"from": "url(x)", "to": "#000000"}}),
		Entry("an opacity above 1", map[string]any{"opacity": 2}),
		Entry("a blur above 40", map[string]any{"blur": 41}),
		Entry("a path that climbs out of the home", map[string]any{"image": "../secret/a.png"}),
		Entry("a path that climbs from the middle", map[string]any{"image": "Pictures/../../a.png"}),
		Entry("an absolute path", map[string]any{"image": "/etc/a.png"}),
		Entry("a web address", map[string]any{"image": "https://example.com/a.png"}),
		Entry("a data address", map[string]any{"image": "data:image/png;base64,AAAA"}),
		Entry("a backslash path", map[string]any{"image": `Pictures\a.png`}),
		Entry("a file that is not a picture", map[string]any{"image": "Docs/notes.txt"}),
		Entry("an SVG document", map[string]any{"image": "Pictures/a.svg"}),
	)

	It("checks both backgrounds in one file and names each bad one", func() {
		data := []byte(`{"homeBackground":{"image":"../a.png"},"chatBackground":{"image":"/b.png"}}`)
		_, errs := wellknown.Validate(wellknown.UI, data)
		Expect(errs).To(ContainElement(ContainSubstring("homeBackground.image")))
		Expect(errs).To(ContainElement(ContainSubstring("chatBackground.image")))
	})

	It("describes the chat background with the same keys as the Home one", func() {
		doc, err := wellknown.Schema(wellknown.UI)
		Expect(err).NotTo(HaveOccurred())
		var parsed struct {
			Properties map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
				XUI        map[string]any             `json:"x-ui"`
			} `json:"properties"`
		}
		Expect(json.Unmarshal(doc, &parsed)).To(Succeed())
		home, chat := parsed.Properties["homeBackground"], parsed.Properties["chatBackground"]
		Expect(chat.XUI).To(Equal(home.XUI))
		Expect(chat.Properties).To(HaveLen(len(home.Properties)))
		for k := range home.Properties {
			Expect(chat.Properties).To(HaveKey(k))
		}
	})

	It("saves from the editor beside the Home background, keeps the other keys and removes only the one cleared", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"theme":"dark","x_mood":"calm","homeBackground":{"color":"#112233"}}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{
			"chatBackground": map[string]any{"image": "Pictures/sky.jpg", "fit": "cover"},
		}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/ui.json")
		Expect(on).To(HaveKeyWithValue("x_mood", "calm"))
		Expect(on).To(HaveKeyWithValue("homeBackground", map[string]any{"color": "#112233"}))
		Expect(on["chatBackground"]).To(HaveKeyWithValue("image", "Pictures/sky.jpg"))

		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"chatBackground": nil}})
		Expect(err).NotTo(HaveOccurred())
		after := readRaw(f, alice, "/LocalAI/ui.json")
		Expect(after).NotTo(HaveKey("chatBackground"))
		Expect(after).To(HaveKey("homeBackground"))
	})

	It("refuses a write with a traversal path and leaves the file as it was", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"chatBackground":{"color":"#112233"}}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{
			"chatBackground": map[string]any{"image": "../../etc/passwd.png"},
		}})
		var invalid *wellknown.InvalidError
		Expect(errors.As(err, &invalid)).To(BeTrue())
		Expect(readRaw(f, alice, "/LocalAI/ui.json")).To(HaveKeyWithValue("chatBackground", map[string]any{"color": "#112233"}))
	})

	It("shows the last good version when the assistant writes a broken chat background", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"chatBackground": map[string]any{"color": "#112233"}}})
		Expect(err).NotTo(HaveOccurred())
		raw(f, alice, "/LocalAI/ui.json", `{"chatBackground":{"image":"../a.png"}}`)
		doc, err := s.Read(ctx, alice, wellknown.UI, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.Data).To(HaveKeyWithValue("chatBackground", map[string]any{"color": "#112233"}))
	})
})

var _ = Describe("the sidebar order of nav.json", func() {
	ctx := context.Background()
	check := func(items ...any) []string {
		if items == nil {
			items = []any{}
		}
		data, err := json.Marshal(map[string]any{"items": items})
		Expect(err).NotTo(HaveOccurred())
		_, errs := wellknown.Validate(wellknown.Nav, data)
		return errs
	}
	item := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}

	It("is a well-known file at LocalAI/nav.json", func() {
		n, err := wellknown.Parse("nav")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(wellknown.Nav))
		Expect(wellknown.File(wellknown.Nav)).To(Equal("/LocalAI/nav.json"))
	})

	It("lists the built-in ids in its schema, the same as the server", func() {
		data, err := wellknown.Schema(wellknown.Nav)
		Expect(err).NotTo(HaveOccurred())
		var top struct {
			IDs []string `json:"x-builtin-ids"`
		}
		Expect(json.Unmarshal(data, &top)).To(Succeed())
		Expect(top.IDs).To(Equal(wellknown.NavBuiltinIDs))
	})

	It("has no way to name New chat, Chats, settings or the search", func() {
		Expect(wellknown.NavBuiltinIDs).NotTo(ContainElements("new-chat", "chats", "settings", "palette", "search", "operate"))
	})

	It("accepts built-in entries, groups and the three kinds of link", func() {
		Expect(check(
			item("id", "home"),
			item("id", "agents", "hidden", true),
			item("id", "skills", "label", "Recipes"),
			item("group", "Create"),
			item("type", "page", "page", "task-board", "label", "Board", "icon", "file-code"),
			item("type", "chat", "chat", "c_8f2a", "label", "Gate repair"),
			item("type", "url", "url", "https://localai.io/docs", "label", "Docs"),
		)).To(BeEmpty())
		Expect(check()).To(BeEmpty())
	})

	It("keeps keys it does not know on an item", func() {
		Expect(check(item("id", "files", "x_future", 1))).To(BeEmpty())
	})

	It("refuses to hide Home, the one built-in entry that always shows", func() {
		Expect(wellknown.NavAlwaysShown).To(ConsistOf("home"))
		Expect(check(item("id", "home", "hidden", true))).To(ContainElement(ContainSubstring("items[0]: Home cannot be hidden")))
		Expect(check(item("id", "home", "hidden", false))).To(BeEmpty())
		Expect(check(item("id", "files", "hidden", true))).To(BeEmpty())
	})

	DescribeTable("refuses a bad list",
		func(items []any, want string) {
			Expect(check(items...)).To(ContainElement(ContainSubstring(want)))
		},
		Entry("an item with nothing in it", []any{item("label", "x")}, "exactly one of id"),
		Entry("an item that is a built-in entry and a group", []any{item("id", "home", "group", "x")}, "exactly one of id"),
		Entry("a page link without a page", []any{item("type", "page")}, "needs \"page\""),
		Entry("a url link without an address", []any{item("type", "url")}, "needs \"url\""),
		Entry("a page that is not a page name", []any{item("type", "page", "page", "../x")}, "is not a page name"),
		Entry("a javascript address", []any{item("type", "url", "url", "javascript:alert(1)")}, "http or https"),
		Entry("an address with a password", []any{item("type", "url", "url", "https://me:pw@example.com/")}, "http or https"),
		Entry("a relative address", []any{item("type", "url", "url", "/app/models")}, "http or https"),
		Entry("a link type that does not exist", []any{item("type", "file", "file", "a")}, "items"),
		Entry("hidden that is not a boolean", []any{item("id", "files", "hidden", "yes")}, "items"),
		Entry("a label of 41 characters", []any{item("id", "files", "label", strings.Repeat("l", 41))}, "items"),
		Entry("an empty label", []any{item("id", "files", "label", "")}, "items"),
		Entry("an id in capitals", []any{item("id", "Files")}, "items"),
		Entry("a built-in entry twice", []any{item("id", "files"), item("id", "files", "hidden", true)}, "same entry as items[0]"),
		Entry("a group title twice", []any{item("group", "A"), item("group", "A")}, "same entry as items[0]"),
		Entry("the same link twice", []any{item("type", "chat", "chat", "c1"), item("type", "chat", "chat", "c1", "label", "x")}, "same entry as items[0]"),
	)

	It("allows the same title for a group and a label of a built-in entry", func() {
		Expect(check(item("group", "files"), item("id", "files", "label", "files"))).To(BeEmpty())
	})

	It("refuses 61 items, 13 groups and 31 links, and accepts the limits", func() {
		var many []any
		for i := range 60 {
			many = append(many, item("group", fmt.Sprintf("g%d", i)))
		}
		Expect(check(many...)).To(ContainElement(ContainSubstring("at most 12 groups")))
		many = nil
		for i := range 31 {
			many = append(many, item("type", "chat", "chat", fmt.Sprintf("c%d", i)))
		}
		Expect(check(many...)).To(ContainElement(ContainSubstring("at most 30 custom links")))
		many = nil
		for i := range 61 {
			many = append(many, item("type", "page", "page", fmt.Sprintf("p%d", i)))
		}
		Expect(check(many...)).To(ContainElement(ContainSubstring("at most 60 items")))
		many = nil
		for i := range 12 {
			many = append(many, item("group", fmt.Sprintf("g%d", i)))
		}
		for i := range 30 {
			many = append(many, item("type", "chat", "chat", fmt.Sprintf("c%d", i)))
		}
		for _, id := range wellknown.NavBuiltinIDs {
			many = append(many, item("id", id))
		}
		Expect(check(many...)).To(BeEmpty())
	})

	DescribeTable("NavURLOK",
		func(u string, want bool) { Expect(wellknown.NavURLOK(u)).To(Equal(want)) },
		Entry("https", "https://localai.io/docs?a=1#b", true),
		Entry("http with a port", "http://localhost:8080/", true),
		Entry("an app path", "/app/files", false),
		Entry("a data address", "data:text/html,x", false),
		Entry("a user name", "https://a@b.com/", false),
		Entry("no host", "https:///x", false),
		Entry("a space", "https://a.com/a b", false),
		Entry("a backslash", `https://a.com\b`, false),
		Entry("empty", "", false),
	)

	It("saves from the editor, keeps the other keys and the order, and removes the list when cleared", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/nav.json", `{"items":[{"id":"files"}],"x_note":"kept"}`)
		items := []any{map[string]any{"id": "skills", "label": "Recipes"}, map[string]any{"group": "Tools"}, map[string]any{"id": "files", "hidden": true}}
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Nav, Values: map[string]any{"items": items}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/nav.json")
		Expect(on).To(HaveKeyWithValue("x_note", "kept"))
		Expect(on["items"]).To(Equal(items))

		_, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Nav, Values: map[string]any{"items": nil}})
		Expect(err).To(HaveOccurred(), "items is required, so a save that removes it leaves an invalid file")
	})

	It("refuses a write with an address that is not http or https and leaves the file as it was", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/nav.json", `{"items":[{"id":"files"}]}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Nav, Values: map[string]any{
			"items": []any{map[string]any{"type": "url", "url": "javascript:alert(1)"}},
		}})
		var invalid *wellknown.InvalidError
		Expect(errors.As(err, &invalid)).To(BeTrue())
		Expect(readRaw(f, alice, "/LocalAI/nav.json")["items"]).To(Equal([]any{map[string]any{"id": "files"}}))
	})

	It("shows the last good version when the assistant writes a broken list", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Nav, Values: map[string]any{"items": []any{map[string]any{"id": "files"}}}})
		Expect(err).NotTo(HaveOccurred())
		raw(f, alice, "/LocalAI/nav.json", `{"items":[{"id":"files"},{"id":"files"}]}`)
		doc, err := s.Read(ctx, alice, wellknown.Nav, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.Errors).To(ContainElement(ContainSubstring("same entry")))
		Expect(doc.Data["items"]).To(Equal([]any{map[string]any{"id": "files"}}))
	})

	It("is not adopted from the top of the home: a file there belongs to the user", func() {
		s, f := newService()
		raw(f, alice, "/nav.json", `{"items":[{"id":"files"}]}`)
		s.AdoptTopLevelFiles(ctx, alice)
		doc, err := s.Read(ctx, alice, wellknown.Nav, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Exists).To(BeFalse())
	})
})
