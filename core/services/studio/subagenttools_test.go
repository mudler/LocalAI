package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// hostSet gives a frontend the host tools named, each a read tool that
// answers with its own name. The set can change while the test runs, as a
// grant does.
type hostSet struct{ names []string }

func (h *hostSet) deps(d *studio.Deps) {
	d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) {
		var ts []session.Tool
		for _, n := range h.names {
			ts = append(ts, session.Tool{Name: n, Description: n, Schema: json.RawMessage(`{"type":"object"}`),
				Run: func(context.Context, json.RawMessage) (string, error) { return n + " ok", nil }})
		}
		return ts, nil
	}
}

var _ = Describe("the tools of a generic sub-agent", func() {
	ctx := context.Background()

	// armChild spawns a background sub-agent with the two tools list_jobs and
	// get_status out of three, and lets it arm a self-paced loop. It returns
	// the child and its loop.
	armChild := func(sh *loopShared, hosts *hostSet) (*loopRig, string, string) {
		a := sh.rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case text == "go":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"watch it","tools":["list_jobs","get_status"],"wait":false}`}, {Say: "started"}}
			case strings.HasPrefix(text, "watch"):
				return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check it.","first_run_after":"1m"}`}, {Say: "armed"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{}, hosts.deps)
		parent := create(a.svc, alice)
		sent(a.svc, alice, parent, "go")
		idle(a.svc, alice, parent)
		var child session.ChatRecord
		until("the child to exist", func() bool {
			kids := childrenOf(a.svc, alice, parent)
			if len(kids) == 1 {
				child = kids[0]
			}
			return len(kids) == 1
		})
		until("the child to arm its loop", func() bool {
			l, _ := a.host.List(ctx, alice, child.ID)
			return len(l) == 1
		})
		l, _ := a.host.List(ctx, alice, child.ID)
		Expect(child.AllowedTools).To(Equal([]string{"list_jobs", "get_status"}), "the grant is stored with the chat")
		return a, child.ID, l[0].ID
	}

	// otherFrontend is a fresh service on the same stores with no memory of
	// the child: a restart, or another frontend. Its loop host runs the loop.
	otherFrontend := func(sh *loopShared, hosts *hostSet, script func(workspace.Actor, string) []studiotest.ToolStep) *loopRig {
		return sh.rig(script, studio.LoopOptions{}, hosts.deps)
	}
	runLoop := func(b *loopRig, loopID string) {
		b.sh.clock.Advance(2 * time.Minute)
		b.pass()
		b.settled(loopID)
	}
	pollScript := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if strings.Contains(text, "Loop run") {
			return []studiotest.ToolStep{
				{Tool: "wait_for", Args: `{"tool":"search_all","every":"1m","then":"x"}`},
				{Tool: "wait_for", Args: `{"tool":"get_status","every":"1m","then":"Report."}`},
				{Say: "polled"},
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}

	It("keeps exactly the granted tools in a loop run after a restart", func() {
		sh := newLoopShared()
		hosts := &hostSet{names: []string{"list_jobs", "get_status", "search_all"}}
		a, child, loopID := armChild(sh, hosts)
		Expect(a.h.ToolNames(child)).To(ContainElements("list_jobs", "get_status", "start_loop"))
		Expect(a.h.ToolNames(child)).NotTo(ContainElement("search_all"))

		b := otherFrontend(sh, hosts, pollScript)
		runLoop(b, loopID)
		names := b.h.ToolNames(child)
		Expect(names).To(ContainElements("list_jobs", "get_status", "loop_done", "wait_for"))
		Expect(names).NotTo(ContainElement("search_all"), "a tool the parent did not grant must not appear")
		Expect(names).NotTo(ContainElement("spawn_agent"))
		Expect(b.h.TurnPrompt(child)).NotTo(ContainSubstring("no longer available"))

		// wait_for polls a stored tool, and refuses one that was not granted.
		res := b.h.ResultsFor("wait_for")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Err).To(MatchError(ContainSubstring(`no tool "search_all"`)))
		Expect(res[1].Err).NotTo(HaveOccurred())
	})

	It("drops a tool the parent may no longer give, and says so", func() {
		sh := newLoopShared()
		a, child, loopID := armChild(sh, &hostSet{names: []string{"list_jobs", "get_status", "search_all"}})
		_ = a
		// The grants of the parent shrank: get_status is gone, and a tool
		// that was never granted appeared.
		now := &hostSet{names: []string{"list_jobs", "search_all", "delete_all"}}
		b := otherFrontend(sh, now, pollScript)
		runLoop(b, loopID)
		names := b.h.ToolNames(child)
		Expect(names).To(ContainElement("list_jobs"))
		Expect(names).NotTo(ContainElement("get_status"))
		Expect(names).NotTo(ContainElement("search_all"))
		Expect(names).NotTo(ContainElement("delete_all"))
		Expect(b.h.TurnPrompt(child)).To(ContainSubstring("no longer available to you and were removed: get_status"))
		// The poll of the dropped tool fails like a tool the chat does not have.
		res := b.h.ResultsFor("wait_for")
		Expect(res).To(HaveLen(2))
		Expect(res[1].Err).To(MatchError(ContainSubstring(`no tool "get_status"`)))
	})

	It("a saved-agent child has no stored tool list", func() {
		sh := newLoopShared()
		r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{}, (&hostSet{names: []string{"list_jobs"}}).deps)
		rec := session.ChatRecord{ID: "kid", Principal: alice, Kind: session.KindSubAgent, ParentID: "p", AgentID: "ag"}
		Expect(sh.chats.CreateChat(ctx, rec)).To(Succeed())
		got, err := sh.chats.GetChat(ctx, alice, "kid")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AllowedTools).To(BeEmpty())
		_ = r
	})
})
