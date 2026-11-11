package studio_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the dock of a chat and the open_page tool", func() {
	ctx := context.Background()

	// setup builds a service whose model calls open_page with the JSON it is
	// told; alice has the pages tasks and clock, bob has tasks.
	setup := func(opts ...func(*studio.Deps)) (*studio.Service, *studiotest.ToolHarness) {
		f := newFiles()
		Expect(f.EnsureHome(ctx, alice)).To(Succeed())
		Expect(f.EnsureHome(ctx, bob)).To(Succeed())
		put := func(user, rel string) {
			abs, err := f.Abs(user, rel)
			Expect(err).NotTo(HaveOccurred())
			_, err = f.Store().Put(ctx, f.Actor(user), abs, strings.NewReader("<p>x</p>"), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
		}
		put(alice, "/pages/tasks/index.html")
		put(alice, "/pages/clock/index.html")
		put(bob, "/pages/notes/index.html")
		return newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if !strings.HasPrefix(text, "{") {
				return []studiotest.ToolStep{{Say: "ok"}}
			}
			return []studiotest.ToolStep{{Tool: "open_page", Args: text}, {Say: "done"}}
		}, append([]func(*studio.Deps){func(d *studio.Deps) { d.Files = f }}, opts...)...)
	}
	call := func(svc *studio.Service, h *studiotest.ToolHarness, user, id, args string) studiotest.ToolResult {
		before := len(h.ResultsFor("open_page"))
		sent(svc, user, id, args)
		idle(svc, user, id)
		got := h.ResultsFor("open_page")
		Expect(got).To(HaveLen(before+1), "the model called open_page")
		return got[before]
	}

	It("opens a page of the user's home in the dock of the chat, in front, without asking", func() {
		svc, h := setup()
		id := create(svc, alice)
		out := call(svc, h, alice, id, `{"name":"tasks"}`)
		Expect(out.Err).NotTo(HaveOccurred())
		Expect(out.Out).To(ContainSubstring("tasks is open in the dock"))
		d, err := svc.Dock(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(*d).To(Equal(session.Dock{Open: true, Tabs: []session.DockTab{{Page: "tasks"}}, Active: 0}))

		// The person closed the panel and docked another page meanwhile.
		_, err = svc.SetDock(ctx, alice, id, session.Dock{Open: false, Width: 520, Tabs: []session.DockTab{{Page: "tasks"}, {Page: "news", Folder: 4}}, Active: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(call(svc, h, alice, id, `{"name":"clock"}`).Err).NotTo(HaveOccurred())
		d, _ = svc.Dock(ctx, alice, id)
		Expect(*d).To(Equal(session.Dock{Open: true, Width: 520, Tabs: []session.DockTab{{Page: "tasks"}, {Page: "news", Folder: 4}, {Page: "clock"}}, Active: 2}),
			"the new tab is added to what the person had, in front, and the panel opens")
		Expect(call(svc, h, alice, id, `{"name":"pages/tasks/"}`).Err).NotTo(HaveOccurred())
		d, _ = svc.Dock(ctx, alice, id)
		Expect(d.Tabs).To(HaveLen(3), "a docked page is not docked twice")
		Expect(d.Active).To(Equal(0))
	})

	It("refuses a shared page, a page of another user and a page that does not exist", func() {
		svc, h := setup()
		id := create(svc, alice)
		Expect(call(svc, h, alice, id, `{"name":"tasks","folder":3}`).Err).To(MatchError(ContainSubstring("only the user's own pages")))
		Expect(call(svc, h, alice, id, `{"name":"notes"}`).Err).To(MatchError(ContainSubstring("no page called notes")))
		Expect(call(svc, h, alice, id, `{"name":"../x"}`).Err).To(HaveOccurred())
		d, err := svc.Dock(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(BeNil(), "nothing was docked")
	})

	It("is offered as a read-only tool, and not without the pages feature", func() {
		svc, h := setup()
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).To(ContainElement("open_page"))

		off, ho := setup(func(d *studio.Deps) {
			d.Users = func(context.Context, string) (*auth.User, error) { return &auth.User{ID: alice}, nil }
			d.Allowed = func(_ *auth.User, feature string) bool { return feature != auth.FeaturePages }
		})
		id = create(off, alice)
		sent(off, alice, id, "hello")
		idle(off, alice, id)
		Expect(ho.ToolNames(id)).NotTo(ContainElement("open_page"))
	})

	It("keeps the dock the person sets within its bounds, per user", func() {
		svc, _ := setup()
		id := create(svc, alice)
		d, err := svc.SetDock(ctx, alice, id, session.Dock{Open: true, Width: 400, Tabs: []session.DockTab{{Page: "tasks"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Width).To(Equal(400))
		for _, bad := range []session.Dock{
			{Tabs: []session.DockTab{{Page: "Bad Name"}}},
			{Tabs: []session.DockTab{{Page: "a"}, {Page: "a"}}},
			{Tabs: []session.DockTab{{Page: "a"}}, Active: 1},
			{Tabs: []session.DockTab{{Page: "a"}}, Width: 100},
			{Tabs: []session.DockTab{{Page: "a", Folder: -1}}},
			{Tabs: []session.DockTab{{Page: "a"}, {Page: "b"}, {Page: "c"}, {Page: "d"}, {Page: "e"}, {Page: "f"}, {Page: "g"}}},
		} {
			_, err := svc.SetDock(ctx, alice, id, bad)
			Expect(err).To(MatchError(session.ErrInvalidDock), "%+v", bad)
		}
		_, err = svc.SetDock(ctx, bob, id, session.Dock{Tabs: []session.DockTab{{Page: "tasks"}}})
		Expect(err).To(MatchError(session.ErrNotFound), "another user's chat")
		d, err = svc.SetDock(ctx, alice, id, session.Dock{Open: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(BeNil(), "a dock with no tab clears it")
		got, _ := svc.Dock(ctx, alice, id)
		Expect(got).To(BeNil())
	})
})

var _ = Describe("Dock.OpenTab", func() {
	It("drops the oldest tab when the dock is full", func() {
		var d *session.Dock
		for _, p := range []string{"a", "b", "c", "d", "e", "f", "g"} {
			d = d.OpenTab(session.DockTab{Page: p})
		}
		Expect(d.Tabs).To(HaveLen(session.MaxDockTabs))
		Expect(d.Tabs[0].Page).To(Equal("b"))
		Expect(d.Active).To(Equal(session.MaxDockTabs - 1))
		Expect(d.Validate()).To(Succeed())
	})
})
