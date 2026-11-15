package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the tool search and the MCP connectors", func() {
	ctx := context.Background()

	It("puts the connector tools behind the tool search when the setting is on", func() {
		fc := newFakeMCPConnectors()
		r := newMCPConnRigWith(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{
				{Tool: "tool_search", Args: `{"query":"doc search"}`},
				{Tool: "tool_call", Args: `{"name":"doc_search","arguments":{}}`},
				{Say: "done"},
			}
		}, fc, withSearch(config.ToolSearchOn, 0))
		id := create(r.svc, alice)
		r.turn(id, "hi")
		names := r.h.ToolNames(id)
		Expect(names).To(ContainElements(bridgeNames))
		Expect(names).NotTo(ContainElements("doc_search", "mail_send"), "the connector tools are sent in full: %v", names)

		found := r.h.ResultsFor("tool_search")
		Expect(found).To(HaveLen(1))
		Expect(found[0].Out).To(ContainSubstring("doc_search"))
		called := r.h.ResultsFor("tool_call")
		Expect(called).To(HaveLen(1))
		Expect(called[0].Err).NotTo(HaveOccurred())
		Expect(called[0].Out).To(Equal("from c1"))
		Expect(fc.calls).To(Equal([]string{"c1/doc_search"}))
	})

	It("cannot reach a connector the chat may not use through tool_call", func() {
		// c3 is switched off on the Connectors page, b1 is another user's.
		fc := newFakeMCPConnectors()
		r := newMCPConnRigWith(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{
				{Tool: "tool_call", Args: `{"name":"never"}`},
				{Tool: "tool_call", Args: `{"name":"bob_tool"}`},
				{Say: "done"},
			}
		}, fc, withSearch(config.ToolSearchOn, 0))
		id := create(r.svc, alice)
		Expect(r.svc.SetMcpOff(ctx, alice, id, []string{"connector:c2"})).To(Succeed())
		r.turn(id, "hi")
		for _, res := range r.h.ResultsFor("tool_call") {
			Expect(res.Err).To(MatchError(ContainSubstring("no such tool")), res.Out)
		}
		Expect(fc.calls).To(BeEmpty())
	})

	It("still sends the connector tools in full when the setting is not on", func() {
		cases := []struct {
			name string
			opt  func(*studio.Deps)
		}{
			// auto defers only the LocalAI groups: the connectors keep their
			// place, even in a window they fill.
			{"auto, small window", withSearch(config.ToolSearchAuto, 200)},
			{"off", withSearch(config.ToolSearchOff, 200)},
			{"no setting wired", nil},
		}
		for _, c := range cases {
			r := newMCPConnRigWith(sayOK, newFakeMCPConnectors(), c.opt)
			id := create(r.svc, alice)
			r.turn(id, "hi")
			names := r.h.ToolNames(id)
			Expect(names).To(ContainElements("doc_search", "mail_send"), c.name)
			Expect(names).NotTo(ContainElement("tool_search"), c.name)
		}
	})
})
