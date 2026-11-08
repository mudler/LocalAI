package studio_test

import (
	"context"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// spawnBG is the script step that starts a background helper with a task.
func spawnBG(task string) studiotest.ToolStep {
	return studiotest.ToolStep{Tool: "spawn_agent", Args: `{"task":"` + task + `","wait":false}`}
}

// helperRig is a coordinator whose helpers wait until the spec ends. A root
// message FAN<n> starts n helpers in the background; a helper whose task is
// NEST starts one helper (task LEAF) and NEST2 starts one NEST; a LEAF waits.
type helperRig struct {
	*coordRig
	ceiling atomic.Int64 // the operator ceiling of helpers; 0 means the default
	agentID string
	chat    string
}

func newHelperRig(cfg coordinator.Config) *helperRig { return newHelperRigOff(cfg, nil) }

// newHelperRigOff is newHelperRig for an agent that switches the groups off.
func newHelperRigOff(cfg coordinator.Config, off []string) *helperRig {
	never := make(chan struct{})
	DeferCleanup(func() { close(never) })
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case strings.HasPrefix(text, "FAN"):
			var steps []studiotest.ToolStep
			for i := range text[3] - '0' {
				steps = append(steps, spawnBG("LEAF "+string(rune('a'+i))+" "+text))
			}
			return append(steps, studiotest.ToolStep{Say: "started"})
		case text == "TREE":
			return []studiotest.ToolStep{spawnBG("NEST a"), spawnBG("NEST b"), {Say: "started"}}
		case text == "DEEP":
			return []studiotest.ToolStep{spawnBG("MID"), {Say: "started"}}
		case text == "SAVED":
			return []studiotest.ToolStep{spawnBG("PROBE"), {Say: "started"}}
		case strings.HasPrefix(text, "NEST"):
			return []studiotest.ToolStep{spawnBG("LEAF under " + text), {Block: never}}
		case strings.HasPrefix(text, "MID"):
			return []studiotest.ToolStep{spawnBG("NEST under " + text), {Block: never}}
		case text == "PROBE":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"x","agent":"Other","wait":false}`}, {Block: never}}
		}
		return []studiotest.ToolStep{{Block: never}}
	}
	sh := newShared()
	rig := sh.rig(script, studio.TriggerOptions{})
	r := &helperRig{}
	host, st := newCoordHost(rig.svc, func() studio.CoordinatorLimits {
		return studio.CoordinatorLimits{HelpersCeiling: int(r.ceiling.Load())}
	})
	r.coordRig = &coordRig{trigRig: rig, host: host, store: st}
	in := coordInput()
	in.Config = cfg
	in.Agent.BuiltinsOff = off
	v, err := host.Create(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	r.agentID, r.chat = v.Agent.ID, v.Config.ChatID
	return r
}

func (r *helperRig) say(text string) {
	_, err := r.svc.Send(context.Background(), alice, r.chat, text)
	Expect(err).NotTo(HaveOccurred())
}

// spawnResults returns the results of every spawn_agent call so far.
func (r *helperRig) spawnResults() (ok, refused []studiotest.ToolResult) {
	for _, res := range r.h.ResultsFor("spawn_agent") {
		if res.Err != nil {
			refused = append(refused, res)
		} else {
			ok = append(ok, res)
		}
	}
	return ok, refused
}

func (r *helperRig) allHelpers() []session.ChatRecord {
	all, err := r.svc.List(context.Background(), alice, 200)
	Expect(err).NotTo(HaveOccurred())
	var out []session.ChatRecord
	for _, c := range all {
		if c.Kind == session.KindSubAgent {
			out = append(out, c)
		}
	}
	return out
}

var _ = Describe("helper limits", func() {
	It("an ordinary chat keeps 4 background helpers and depth 1", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "many" {
				var steps []studiotest.ToolStep
				for range 5 {
					steps = append(steps, spawnBG("t"+strings.Repeat("x", len(steps))))
				}
				return steps
			}
			return []studiotest.ToolStep{{Block: never}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "many")
		idle(svc, alice, id)
		res := h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(5))
		Expect(res[4].Err).To(MatchError(ContainSubstring("limit")))
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(4))
		until("a helper to run", func() bool { return len(h.ToolNames(kids[0].ID)) > 0 || h.Sent(kids[0].ID) != nil })
		Expect(h.ToolNames(kids[0].ID)).NotTo(ContainElement("spawn_agent"), "a helper of an ordinary chat starts none")
	})

	It("a coordinator starts MaxHelpers helpers and the next is refused with ErrSubAgentLimit", func() {
		r := newHelperRig(coordinator.Config{MaxHelpers: 3})
		r.say("FAN4")
		idle(r.svc, alice, r.chat)
		ok, refused := r.spawnResults()
		Expect(ok).To(HaveLen(3))
		Expect(refused).To(HaveLen(1))
		Expect(refused[0].Err).To(MatchError(ContainSubstring("limit")))
		rows, err := r.host.Audit(context.Background(), studio.CoordinatorActor{User: alice}, coordinator.AuditQuery{Kind: coordinator.KindHelper})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Reason).To(Equal(coordinator.ReasonHelperLimit))
	})

	It("uses 8 helpers by default", func() {
		r := newHelperRig(coordinator.Config{})
		r.say("FAN9")
		idle(r.svc, alice, r.chat)
		ok, refused := r.spawnResults()
		Expect(ok).To(HaveLen(8))
		Expect(refused).To(HaveLen(1))
	})

	It("a helper of a coordinator may start a helper at depth 2 and that one may not", func() {
		r := newHelperRig(coordinator.Config{})
		r.say("DEEP")
		until("the second level to try a third", func() bool { return len(r.h.ResultsFor("spawn_agent")) == 3 })
		helpers := r.allHelpers()
		Expect(helpers).To(HaveLen(2), "a third level was not made")
		byTask := map[string]session.ChatRecord{}
		for _, c := range helpers {
			byTask[strings.TrimPrefix(c.Title, "Sub-agent: ")] = c
		}
		mid, low := byTask["MID"], byTask["NEST under MID"]
		Expect(low.ParentID).To(Equal(mid.ID))
		Expect(r.h.ToolNames(mid.ID)).To(ContainElements("spawn_agent", "wait_agents"))
		Expect(r.h.ToolNames(low.ID)).NotTo(ContainElement("spawn_agent"))
		Expect(r.h.ToolNames(low.ID)).NotTo(ContainElement("wait_agents"))
		Expect(r.h.ToolNames(mid.ID)).NotTo(ContainElement(studio.ContactName))
		Expect(r.h.ToolNames(low.ID)).NotTo(ContainElement(studio.ContactName))
		last := r.h.ResultsFor("spawn_agent")[2]
		Expect(last.Chat).To(Equal(low.ID))
		Expect(last.Err).To(HaveOccurred())
		// The session of the helper that may delegate says to wait.
		Expect(r.h.LastToolDescription("spawn_agent")).To(ContainSubstring("Wait for your own sub-agents with wait_agents before you answer."))
	})

	It("the cap counts helpers of helpers across the tree", func() {
		r := newHelperRig(coordinator.Config{MaxHelpers: 3})
		r.say("TREE")
		idle(r.svc, alice, r.chat)
		until("every spawn call to answer", func() bool { return len(r.h.ResultsFor("spawn_agent")) == 4 })
		ok, refused := r.spawnResults()
		Expect(ok).To(HaveLen(3), "two helpers and one of their helpers fill the cap of 3")
		Expect(refused).To(HaveLen(1))
		Expect(refused[0].Err).To(MatchError(ContainSubstring("limit")))
		Expect(r.allHelpers()).To(HaveLen(3))
	})

	It("the operator ceiling lowers the effective limits and leaves the stored config alone", func() {
		r := newHelperRig(coordinator.Config{MaxHelpers: 6})
		r.ceiling.Store(2)
		r.say("FAN4")
		idle(r.svc, alice, r.chat)
		ok, _ := r.spawnResults()
		Expect(ok).To(HaveLen(2))
		v, err := r.host.Get(context.Background(), alice, r.agentID)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Config.MaxHelpers).To(Equal(6))
	})

	It("lowering the limit keeps running helpers and blocks new ones", func() {
		r := newHelperRig(coordinator.Config{MaxHelpers: 6})
		r.say("FAN3")
		idle(r.svc, alice, r.chat)
		until("three helpers to run", func() bool {
			n := 0
			for _, c := range r.allHelpers() {
				if turn(r.svc, alice, c.ID).Running {
					n++
				}
			}
			return n == 3
		})
		r.ceiling.Store(1)
		r.say("FAN1")
		until("the new helper to be refused", func() bool { _, refused := r.spawnResults(); return len(refused) == 1 })
		for _, c := range r.allHelpers() {
			Expect(turn(r.svc, alice, c.ID).Running).To(BeTrue(), "no running helper was stopped")
		}
		Expect(r.allHelpers()).To(HaveLen(3))
	})

	It("a helper at depth 3 keeps the switches of the coordinator's agent", func() {
		r := newHelperRigOff(coordinator.Config{MaxDepth: 3}, []string{"files"})
		r.say("DEEP")
		until("the third level to exist", func() bool { return len(r.allHelpers()) == 3 })
		var low session.ChatRecord
		for _, c := range r.allHelpers() {
			if strings.HasPrefix(c.Title, "Sub-agent: LEAF") {
				low = c
			}
		}
		until("the third level to start", func() bool { return len(r.h.ToolNames(low.ID)) > 0 || turn(r.svc, alice, low.ID).Running })
		Expect(r.h.ToolNames(low.ID)).NotTo(ContainElement("write_file"), "files are off for the agent, at every depth")
		Expect(r.h.ToolNames(low.ID)).NotTo(ContainElement("spawn_agent"))
	})

	It("saved agents are still refused as helpers of a coordinator", func() {
		r := newHelperRig(coordinator.Config{})
		_, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Other", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		r.say("SAVED")
		until("the saved agent to be asked for", func() bool { return len(r.h.ResultsFor("spawn_agent")) == 2 })
		_, refused := r.spawnResults()
		Expect(refused).To(HaveLen(1))
		Expect(refused[0].Err.Error()).To(Or(ContainSubstring("saved agents"), ContainSubstring("unattended"), ContainSubstring("started by itself")))
		Expect(r.allHelpers()).To(HaveLen(1), "only the probe exists: the saved agent did not start")
	})
})
