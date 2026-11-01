package studio_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// listedAddons is a broker that also lists the add-ons that replace a
// built-in tool in any state, as the host does.
type listedAddons struct {
	*fakeAddons
	shadowers []studio.AddonShadower
}

func (l listedAddons) AddonShadowers(context.Context, string) ([]studio.AddonShadower, error) {
	return l.shadowers, nil
}

// The resolution of a tool that add-ons replace, for the cases where the
// add-on cannot serve: the built-in serves, with a note, and another add-on
// does not take over a pin.
var _ = Describe("a replaced tool whose add-on cannot serve", func() {
	searcher := func(id string) studio.AddonTool {
		t := addonTool(id, "search")
		t.Shadows = "web_search"
		return t
	}
	// "old" is installed first and "new" later; both replace web_search.
	installed := []studio.AddonShadower{
		{AddonID: "old", Title: "Old search", InstalledAt: "2026-01-01T00:00:00Z", Tools: []string{"web_search"}},
		{AddonID: "new", Title: "New search", InstalledAt: "2026-02-01T00:00:00Z", Tools: []string{"web_search"}},
	}
	running := func(ids ...string) []studio.AddonShadower {
		out := make([]studio.AddonShadower, 0, len(installed))
		for _, a := range installed {
			for _, id := range ids {
				if a.AddonID == id {
					a.Running = true
				}
			}
			out = append(out, a)
		}
		return out
	}
	service := func(tools []studio.AddonTool, list []studio.AddonShadower, pins map[string]string, more ...func(*studio.Deps)) *studio.Service {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: tools}}
		opts := []func(*studio.Deps){withWeb(&fakeWeb{search: true, fetch: true}), func(d *studio.Deps) {
			d.AddonTools = listedAddons{fa, list}
			d.ToolPins = func() map[string]string { return pins }
		}}
		svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, append(opts, more...)...)
		return svc
	}
	served := func(svc *studio.Service, id string) (string, []studio.ToolFallback) {
		panel, err := svc.Tools(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		for _, g := range panel.Builtins {
			if g.Group == "web" {
				return g.Providers["web_search"], panel.Fallbacks
			}
		}
		Fail("no web group in the panel")
		return "", nil
	}

	It("gives the built-in, not another add-on, when the pinned add-on is stopped", func() {
		svc := service([]studio.AddonTool{searcher("new")}, running("new"), map[string]string{"web_search": "old"})
		by, notes := served(svc, create(svc, alice))
		Expect(by).To(Equal(studio.BuiltinProvider))
		Expect(notes).To(Equal([]studio.ToolFallback{{Tool: "web_search", AddonID: "old", Title: "Old search", Builtin: true}}))

		list, err := svc.AdminBuiltinTools(context.Background(), alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list[0].ID).To(Equal("web_search"))
		Expect(list[0].Provider).To(Equal(studio.BuiltinProvider))
		Expect(list[0].Candidates).To(Equal([]string{"old", "new"}))
		Expect(list[0].Stopped).To(Equal([]string{"old"}))
		Expect(list[0].Default).To(Equal("new"))
		Expect(list[0].Titles).To(HaveKeyWithValue("old", "Old search"))
		Expect(list[0].Note).To(Equal("not running"))
	})

	It("gives the oldest running add-on when nothing is pinned and the oldest install is stopped", func() {
		svc := service([]studio.AddonTool{searcher("new")}, running("new"), nil)
		by, notes := served(svc, create(svc, alice))
		Expect(by).To(Equal("new"))
		Expect(notes).To(BeEmpty())
	})

	It("ignores a pin to an add-on that is not installed", func() {
		svc := service([]studio.AddonTool{searcher("new")}, running("new"), map[string]string{"web_search": "gone"})
		by, notes := served(svc, create(svc, alice))
		Expect(by).To(Equal("new"))
		Expect(notes).To(BeEmpty())
	})

	It("notes a stopped add-on with no pin, and the built-in serves", func() {
		svc := service(nil, running(), nil)
		by, notes := served(svc, create(svc, alice))
		Expect(by).To(Equal(studio.BuiltinProvider))
		Expect(notes).To(Equal([]studio.ToolFallback{{Tool: "web_search", AddonID: "old", Title: "Old search", Builtin: true}}))
	})

	It("gives the built-in when the chat switched the pinned add-on off", func() {
		svc := service([]studio.AddonTool{searcher("old"), searcher("new")}, running("old", "new"), map[string]string{"web_search": "old"})
		id := create(svc, alice)
		by, notes := served(svc, id)
		Expect(by).To(Equal("old"))
		Expect(notes).To(BeEmpty())
		Expect(svc.SetAddonsOff(context.Background(), alice, id, []string{"old"})).To(Succeed())
		by, notes = served(svc, id)
		Expect(by).To(Equal(studio.BuiltinProvider))
		Expect(notes).To(HaveLen(1))
		Expect(notes[0].AddonID).To(Equal("old"))
	})

	It("says when the built-in is off too", func() {
		svc := service(nil, running(), nil)
		id := create(svc, alice)
		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{"web"})).To(Succeed())
		_, notes := served(svc, id)
		Expect(notes).To(HaveLen(1))
		Expect(notes[0].Builtin).To(BeFalse())
	})

	It("gives the built-in when the chat's agent does not grant the pinned add-on", func() {
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Put(context.Background(), agents.Agent{ID: "ag", Principal: alice, Name: "Reader", Addons: []string{"new"}, Created: time.Now().UTC()})).To(Succeed())
		svc := service([]studio.AddonTool{searcher("old"), searcher("new")}, running("old", "new"), map[string]string{"web_search": "old"},
			func(d *studio.Deps) { d.Agents = as })
		rec, err := svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: "ag"})
		Expect(err).NotTo(HaveOccurred())
		by, notes := served(svc, rec.ID)
		Expect(by).To(Equal(studio.BuiltinProvider))
		Expect(notes).To(HaveLen(1))
		Expect(notes[0].AddonID).To(Equal("old"))
	})
})
