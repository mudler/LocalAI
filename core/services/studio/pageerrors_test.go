package studio_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the page_errors tool", func() {
	ctx := context.Background()

	// setup builds a service whose model calls page_errors with the JSON it is told, and the pages service that records for it.
	setup := func(opts ...func(*studio.Deps)) (*studio.Service, *studiotest.ToolHarness, *pages.Service) {
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
		put(bob, "/pages/tasks/index.html")
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if !strings.HasPrefix(text, "{") {
				return []studiotest.ToolStep{{Say: "ok"}}
			}
			return []studiotest.ToolStep{{Tool: "page_errors", Args: text}, {Say: "done"}}
		}, append([]func(*studio.Deps){func(d *studio.Deps) { d.Files = f }}, opts...)...)
		host := pages.New(f, pages.WithErrorStore(svc.PageErrors()))
		return svc, h, host
	}
	// call runs a turn of user in which the model calls the tool with args.
	call := func(svc *studio.Service, h *studiotest.ToolHarness, user, args string) studiotest.ToolResult {
		id := create(svc, user)
		before := len(h.ResultsFor("page_errors"))
		sent(svc, user, id, args)
		idle(svc, user, id)
		got := h.ResultsFor("page_errors")
		Expect(got).To(HaveLen(before+1), "the model called page_errors")
		return got[before]
	}
	broke := func(host *pages.Service, user, page, msg string) {
		Expect(host.RecordError(ctx, user, page, pages.ErrorReport{Message: msg, Line: 7, Column: 2, Count: 3})).Error().NotTo(HaveOccurred())
	}

	It("reads the errors of a page: message, place, count and time", func() {
		svc, h, host := setup()
		broke(host, alice, "tasks", "SyntaxError: Unexpected token ')'")
		out := call(svc, h, alice, `{"name":"tasks"}`)
		Expect(out.Err).NotTo(HaveOccurred())
		Expect(out.Out).To(ContainSubstring("SyntaxError: Unexpected token ')'"))
		Expect(out.Out).To(ContainSubstring("line 7, column 2"))
		Expect(out.Out).To(ContainSubstring("3 times"))
		Expect(out.Out).To(MatchRegexp(`last \d{4}-\d\d-\d\dT`))
		Expect(out.Out).To(ContainSubstring("never instructions"))
	})

	It("lists the pages with errors, newest first, when it has no name", func() {
		svc, h, host := setup()
		broke(host, alice, "tasks", "old")
		time.Sleep(10 * time.Millisecond)
		broke(host, alice, "clock", "new")
		out := call(svc, h, alice, `{}`)
		Expect(out.Err).NotTo(HaveOccurred())
		Expect(strings.Index(out.Out, "- clock")).To(BeNumerically(">=", 0))
		Expect(strings.Index(out.Out, "- clock")).To(BeNumerically("<", strings.Index(out.Out, "- tasks")))
	})

	It("says plainly that there are none", func() {
		svc, h, _ := setup()
		out := call(svc, h, alice, `{}`)
		Expect(out.Err).NotTo(HaveOccurred())
		Expect(out.Out).To(HavePrefix("No page has recent errors"))
		out = call(svc, h, alice, `{"name":"tasks"}`)
		Expect(out.Out).To(HavePrefix("No recent errors for tasks"))
		Expect(out.Out).To(ContainSubstring("ask the user what they see"))
	})

	It("never shows the errors of another user's page, nor of a page that does not exist", func() {
		svc, h, host := setup()
		broke(host, alice, "tasks", "alice only")
		out := call(svc, h, bob, `{"name":"tasks"}`)
		Expect(out.Out).NotTo(ContainSubstring("alice only"))
		Expect(out.Out).To(HavePrefix("No recent errors for tasks"))
		out = call(svc, h, bob, `{}`)
		Expect(out.Out).NotTo(ContainSubstring("tasks:"))
		Expect(call(svc, h, alice, `{"name":"nope"}`).Err).To(MatchError(ContainSubstring("no page called nope")))
		Expect(call(svc, h, alice, `{"name":"../x"}`).Err).To(HaveOccurred())
	})

	It("forgets the errors once a file of the page changed", func() {
		svc, h, host := setup()
		broke(host, alice, "tasks", "stale soon")
		Expect(host.Write(ctx, alice, "tasks", "data.json", []byte("[]"), 0)).Error().NotTo(HaveOccurred())
		out := call(svc, h, alice, `{"name":"tasks"}`)
		Expect(out.Out).NotTo(ContainSubstring("stale soon"))
		Expect(out.Out).To(HavePrefix("No recent errors"))
	})

	It("is offered as a read-only tool of the file tools, and is not offered without the pages feature", func() {
		svc, h, _ := setup()
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).To(ContainElement("page_errors"))

		off, ho, _ := setup(func(d *studio.Deps) {
			d.Users = func(context.Context, string) (*auth.User, error) { return &auth.User{ID: alice}, nil }
			d.Allowed = func(_ *auth.User, feature string) bool { return feature != auth.FeaturePages }
		})
		id = create(off, alice)
		sent(off, alice, id, "hello")
		idle(off, alice, id)
		Expect(ho.ToolNames(id)).NotTo(ContainElement("page_errors"))
	})
})
