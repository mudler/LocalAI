package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func searchTool() session.Tool {
	return session.Tool{
		Name: "search", Description: "search the workspace", Schema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "found", nil },
	}
}

func newToolService(script func(workspace.Actor, string) []studiotest.ToolStep, opts ...func(*studio.Deps)) (*studio.Service, *studiotest.ToolHarness) {
	h := &studiotest.ToolHarness{Script: script}
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	d := studio.Deps{
		Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h,
		HostTools: func(context.Context, workspace.Actor) ([]session.Tool, error) {
			return []session.Tool{searchTool()}, nil
		},
	}
	for _, o := range opts {
		o(&d)
	}
	svc, err := studio.New(d)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc, h
}

// childrenOf lists the sub-agent conversations of a parent.
func childrenOf(svc *studio.Service, user, parent string) []session.ChatRecord {
	all, err := svc.List(context.Background(), user, 100)
	Expect(err).NotTo(HaveOccurred())
	var out []session.ChatRecord
	for _, r := range all {
		if r.ParentID == parent {
			out = append(out, r)
		}
	}
	return out
}

func idle(svc *studio.Service, user, id string) {
	until("the turn to end", func() bool { return !turn(svc, user, id).Running && turn(svc, user, id).Last != "" })
}

var _ = Describe("subagent", func() {
	It("a foreground sub agent answers inside the parents turn", func() {
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "go":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"find the answer","wait":true}`}, {Say: "parent done"}}
			case "find the answer":
				return []studiotest.ToolStep{{Say: "42"}}
			}
			return nil
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)

		res := h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1), "spawn_agent result = %+v, want the child's answer 42", res)
		Expect(res[0].Err).To(BeNil(), "spawn_agent result = %+v, want the child's answer 42", res)
		Expect(res[0].Out).To(ContainSubstring("42"), "spawn_agent result = %+v, want the child's answer 42", res)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1), "children = %+v, want one sub-agent of the same user and model", kids)
		Expect(kids[0].Kind).To(Equal(session.KindSubAgent), "children = %+v, want one sub-agent of the same user and model", kids)
		Expect(kids[0].Principal).To(Equal(alice), "children = %+v, want one sub-agent of the same user and model", kids)
		Expect(kids[0].Model).To(Equal("m"), "children = %+v, want one sub-agent of the same user and model", kids)
	})

	It("a background sub agent reports back as a message and an event", func() {
		release := make(chan struct{})
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case text == "go":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"slow job","wait":false}`}, {Say: "parent moved on"}}
			case text == "slow job":
				return []studiotest.ToolStep{{Block: release}, {Say: "child done: 7"}}
			case strings.Contains(text, "child done: 7"):
				return []studiotest.ToolStep{{Say: "thanks"}}
			}
			return nil
		})
		id := create(svc, alice)
		events, cancel, err := svc.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()

		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1), "background spawn returned %+v, want an immediate 'Started'", res)
		Expect(res[0].Err).To(BeNil(), "background spawn returned %+v, want an immediate 'Started'", res)
		Expect(res[0].Out).To(ContainSubstring("Started"), "background spawn returned %+v, want an immediate 'Started'", res)
		Expect(h.Sent(id)).To(HaveLen(1), "the parent has taken another turn too early: %v", h.Sent(id))

		close(release)
		until("the result to reach the parent as a new message", func() bool { return len(h.Sent(id)) == 2 })
		Expect(h.Sent(id)[1]).To(ContainSubstring("child done: 7"))
		timeout := time.After(20 * time.Second)
		for {
			select {
			case e := <-events:
				if e.Kind == session.KindSubAgentDone {
					Expect(e.IDs).To(HaveLen(1), "sub_agent_done = %+v", e)
					Expect(e.IDs[0]).To(Equal(childrenOf(svc, alice, id)[0].ID), "sub_agent_done = %+v", e)
					return
				}
			case <-timeout:
				Fail("no sub_agent_done event")
			}
		}
	})

	It("a sub-agent gets the parent's built-in file tools, or the ones the parent names", func() {
		f := newFiles()
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "inherit":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"a","wait":true}`}}
			case "narrow":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"b","tools":["read_file","edit_file"],"wait":true}`}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = f })
		id := create(svc, alice)

		sent(svc, alice, id, "inherit")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1))
		Expect(h.ToolNames(kids[0].ID)).To(ContainElements("list_files", "read_file", "write_file", "edit_file", "move_file", "delete_file", "search_files"))

		sent(svc, alice, id, "narrow")
		idle(svc, alice, id)
		kids = childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(2))
		var lists [][]string
		for _, k := range kids {
			lists = append(lists, h.ToolNames(k.ID))
		}
		Expect(lists).To(ContainElement(ConsistOf("read_file", "edit_file")), "tool lists: %v", lists)
	})

	It("a sub agent cannot spawn and gets the parents tools unless narrowed", func() {
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "inherit":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"a","wait":true}`}}
			case "none":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"b","tools":[],"wait":true}`}}
			case "bad":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"c","tools":["nope"],"wait":true}`}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		id := create(svc, alice)

		sent(svc, alice, id, "inherit")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1), "children = %d, want 1", len(kids))
		got := h.ToolNames(kids[0].ID)
		Expect(got).To(Equal([]string{"search"}), "a sub-agent that was not narrowed has tools %v, want only search (never spawn_agent)", got)
		got = h.ToolNames(id)
		Expect(got).To(Equal([]string{"search", "spawn_agent"}), "the parent has tools %v, want search and spawn_agent", got)

		sent(svc, alice, id, "none")
		idle(svc, alice, id)
		kids = childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(2), "children = %d, want 2", len(kids))
		var narrowed session.ChatRecord
		for _, k := range kids {
			if len(h.ToolNames(k.ID)) == 0 {
				narrowed = k
			}
		}
		Expect(narrowed.ID).NotTo(Equal(""), "no sub-agent was started with an empty tool list; tool lists: %v / %v", h.ToolNames(kids[0].ID), h.ToolNames(kids[1].ID))

		sent(svc, alice, id, "bad")
		idle(svc, alice, id)
		bad := h.ResultsFor("spawn_agent")
		last := bad[len(bad)-1]
		Expect(last.Err).NotTo(BeNil(), "a tool the parent lacks must be refused by name; got %+v", last)
		Expect(last.Err.Error()).To(ContainSubstring("nope"), "a tool the parent lacks must be refused by name; got %+v", last)
		Expect(childrenOf(svc, alice, id)).To(HaveLen(2))
	})

	It("at most four sub agents run in the background", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "many" {
				var steps []studiotest.ToolStep
				for _, task := range []string{"t1", "t2", "t3", "t4", "t5"} {
					steps = append(steps, studiotest.ToolStep{Tool: "spawn_agent", Args: `{"task":"` + task + `","wait":false}`})
				}
				return steps
			}
			return []studiotest.ToolStep{{Block: never}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "many")
		idle(svc, alice, id)
		res := h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(5), "spawn calls = %d", len(res))
		for i := range 4 {
			Expect(res[i].Err).To(BeNil(), "spawn %d failed: %v", i+1, res[i].Err)
		}
		Expect(res[4].Err).NotTo(BeNil(), "the fifth background sub-agent must hit the limit, got %+v", res[4])
		Expect(strings.ToLower(res[4].Err.Error())).To(ContainSubstring("limit"), "the fifth background sub-agent must hit the limit, got %+v", res[4])
		Expect(childrenOf(svc, alice, id)).To(HaveLen(4))
	})

	It("stopping the parent stops its running sub agents", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"blocked child","wait":false}`}, {Block: never}}
			}
			return []studiotest.ToolStep{{Block: never}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		until("the child to run", func() bool {
			k := childrenOf(svc, alice, id)
			return len(k) == 1 && turn(svc, alice, k[0].ID).Running && len(h.Sent(k[0].ID)) == 1
		})
		Expect(svc.Stop(context.Background(), alice, id)).To(Succeed())
		child := childrenOf(svc, alice, id)[0].ID
		until("the child to stop", func() bool { return turn(svc, alice, child).Last == store.TurnStopped })
	})

	// The parent's turn can end while a background child still runs. Stopping the
	// parent then still stops the child.
	It("stopping a parent whose turn ended stops its background sub agent", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"blocked child","wait":false}`}, {Say: "started it"}}
			}
			return []studiotest.ToolStep{{Block: never}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		until("the child to run", func() bool {
			k := childrenOf(svc, alice, id)
			return len(k) == 1 && turn(svc, alice, k[0].ID).Running && len(h.Sent(k[0].ID)) == 1
		})
		until("the parent's turn to end", func() bool {
			i := turn(svc, alice, id)
			return !i.Running && i.Last == store.TurnDone
		})
		Expect(svc.Stop(context.Background(), alice, id)).To(Succeed())
		child := childrenOf(svc, alice, id)[0].ID
		until("the child to stop", func() bool { return turn(svc, alice, child).Last == store.TurnStopped })
	})

	// A sub-agent conversation runs with the tools its parent chose. A message
	// from the user would start it with none, so it is read-only.
	It("a user cannot send to a sub agent conversation", func() {
		svc, _ := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"job","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1), "children = %d, want 1", len(kids))
		_, err := svc.Send(context.Background(), alice, kids[0].ID, "more")
		Expect(err).To(MatchError(studio.ErrSubAgentReadOnly), "Send to a sub-agent conversation = %v, want ErrSubAgentReadOnly", err)
	})

	// A turn that the server's shutdown cuts short is interrupted, not done: its
	// text is partial, and a run of a trigger or a connector must not report it as
	// finished.
	It("a turn cut short by shutdown is interrupted", func() {
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "work")
		until("the turn to run", func() bool { return turn(svc, alice, id).Running })
		Expect(svc.Close(context.Background())).To(Succeed())
		until("the turn to be settled", func() bool { return !turn(svc, alice, id).Running })
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnInterrupted))
	})
})
