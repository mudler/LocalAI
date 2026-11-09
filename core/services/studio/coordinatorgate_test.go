package studio_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// gateRig is a coordinator rig whose turns end at once, except a message with
// BLOCK, which runs until the channel closes.
type gateRig struct {
	*coordRig
	block chan struct{}
	mu    sync.Mutex
}

// release lets the blocked turns go. Rigs of one cluster share the channel, so
// it is closed once whoever calls first.
func (r *gateRig) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.block:
	default:
		close(r.block)
	}
}

func newGateRig(runs, maxRunning int) *gateRig {
	st, err := coordinator.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	return newGateRigOn(newShared(), st, make(chan struct{}), func() studio.CoordinatorLimits {
		return studio.CoordinatorLimits{RunsPerDay: runs, MaxRunning: maxRunning}
	})
}

// newGateRigOn is a frontend over the stores of sh and the coordinator store
// st, so that several of them make a cluster.
func newGateRigOn(sh *shared, st coordinator.Store, block chan struct{}, lim func() studio.CoordinatorLimits) *gateRig {
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case strings.Contains(text, "BLOCK"):
			return []studiotest.ToolStep{{Block: block}}
		case strings.Contains(text, "HELPER"):
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"SLOW","wait":false}`}, {Block: block}}
		case strings.Contains(text, "SLOW"):
			return []studiotest.ToolStep{{Block: block}, {Say: "late"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	r := sh.rig(script, studio.TriggerOptions{})
	host, err := studio.NewCoordinators(r.svc, studio.CoordinatorOptions{Store: st, Limits: lim})
	Expect(err).NotTo(HaveOccurred())
	r.svc.SetCoordinators(host)
	g := &gateRig{coordRig: &coordRig{trigRig: r, host: host, store: st}, block: block}
	DeferCleanup(g.release)
	return g
}

// desk makes a coordinator named name and returns its agent and chat.
func (r *gateRig) desk(name string) (agentID, chat string) {
	in := coordInput()
	in.Agent.Name = name
	v, err := r.host.Create(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	return v.Agent.ID, v.Config.ChatID
}

func (r *gateRig) pause(agentID string) {
	_, _, err := r.store.SetPaused(context.Background(), agentID, true, alice, time.Now())
	Expect(err).NotTo(HaveOccurred())
}

func (r *gateRig) send(ctx context.Context, chat, text string) studio.SendResult {
	res, err := r.svc.Send(ctx, alice, chat, text)
	Expect(err).NotTo(HaveOccurred())
	return res
}

var _ = Describe("coordinator gate", func() {
	bg := context.Background()
	reply := studio.WithWake(bg, studio.WakeReply)

	It("holds a message of a paused coordinator as a waiting message and starts no turn", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		r.pause(id)
		res := r.send(reply, chat, "from a contact")
		Expect(res.Held).To(BeTrue())
		Expect(res.Started).To(BeFalse())
		Expect(res.PendingID).NotTo(BeEmpty())
		Expect(pendingTexts(r.svc, alice, chat)).To(Equal([]string{"from a contact"}))
		Expect(r.h.Sent(chat)).To(BeEmpty())
		Expect(turn(r.svc, alice, chat).Running).To(BeFalse())
	})

	It("holds the owner's message too while paused", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		r.pause(id)
		res := r.send(bg, chat, "from the owner")
		Expect(res.Held).To(BeTrue())
		Expect(pendingTexts(r.svc, alice, chat)).To(Equal([]string{"from the owner"}))
		Expect(r.h.Sent(chat)).To(BeEmpty())
	})

	It("a chat that is not a coordinator is never held", func() {
		r := newGateRig(1, 1)
		plain := create(r.svc, alice)
		res := r.send(reply, plain, "hello")
		Expect(res.Started).To(BeTrue())
		Expect(res.Held).To(BeFalse())
	})

	It("refuses a third wake over a budget of 2 and counts only wake sources", func() {
		r := newGateRig(2, 0)
		_, chat := r.desk("Desk")
		for i := range 2 {
			Expect(r.send(reply, chat, "r").Started).To(BeTrue(), "wake %d", i+1)
			idle(r.svc, alice, chat)
		}
		res := r.send(reply, chat, "third")
		Expect(res.Held).To(BeTrue())
		Expect(res.Started).To(BeFalse())
		rec, err := r.svc.Get(bg, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.Check(bg, rec, studio.WakeReply)).To(Equal(studio.Verdict{Reason: coordinator.ReasonBudget}))
		Expect(r.host.Check(bg, rec, studio.WakeHelper).Reason).To(Equal(coordinator.ReasonBudget))
		Expect(r.host.Check(bg, rec, studio.WakeOwner).Allow).To(BeTrue(), "the owner has no budget")
		// The owner still gets a turn, and it takes the held message with it.
		Expect(r.send(bg, chat, "owner").Started).To(BeTrue())
		idle(r.svc, alice, chat)
		Expect(r.h.Sent(chat)).To(HaveLen(3))
		Expect(pendingTexts(r.svc, alice, chat)).To(BeEmpty())
	})

	It("holds a wake while the cluster is at the max running count", func() {
		r := newGateRig(0, 1)
		_, one := r.desk("One")
		_, two := r.desk("Two")
		Expect(r.send(bg, one, "BLOCK").Started).To(BeTrue())
		until("the first turn to run", func() bool { return turn(r.svc, alice, one).Running })
		rec, err := r.svc.Get(bg, alice, two)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.Check(bg, rec, studio.WakeSchedule).Reason).To(Equal(coordinator.ReasonConcurrency))
		res := r.send(studio.WithWake(bg, studio.WakeSchedule), two, "wake")
		Expect(res.Held).To(BeTrue())
		Expect(r.h.Sent(two)).To(BeEmpty())
		// The owner's own message and an ordinary chat are not counted out.
		Expect(r.send(bg, two, "owner").Started).To(BeTrue())
		idle(r.svc, alice, two)
		plain := create(r.svc, alice)
		Expect(r.send(reply, plain, "hello").Started).To(BeTrue())
	})

	It("loses nothing when senders race for the last run", func() {
		r := newGateRig(1, 0)
		_, chat := r.desk("Desk")
		rec, err := r.svc.Get(bg, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		var allowed atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				defer GinkgoRecover()
				if r.host.Charge(bg, rec, studio.WakeReply).Allow {
					allowed.Add(1)
				}
			})
		}
		wg.Wait()
		Expect(allowed.Load()).To(BeEquivalentTo(1), "the last slot goes to one sender")

		// Through Send, with the slot gone: every message is waiting, none lost.
		var held atomic.Int32
		for range 3 {
			wg.Go(func() {
				defer GinkgoRecover()
				if r.send(reply, chat, "m").Held {
					held.Add(1)
				}
			})
		}
		wg.Wait()
		Expect(held.Load()).To(BeEquivalentTo(3))
		Expect(pendingTexts(r.svc, alice, chat)).To(HaveLen(3))
		Expect(r.h.Sent(chat)).To(BeEmpty())
	})

	It("a loop run skips a paused coordinator chat", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		r.pause(id)
		started, err := r.svc.StartIdleForTest(bg, alice, chat, "loop")
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeFalse())
		Expect(r.h.Sent(chat)).To(BeEmpty())
		_, _, err = r.store.SetPaused(bg, id, false, "", time.Now())
		Expect(err).NotTo(HaveOccurred())
		started, err = r.svc.StartIdleForTest(bg, alice, chat, "loop")
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeTrue())
	})

	It("an interrupted turn does not resume while paused", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		st := r.sh.chats
		Expect(st.ClaimTurn(bg, alice, chat, "dead")).To(Succeed())
		Expect(st.MarkInterrupted(bg, alice, chat, "dead", 0)).To(Succeed())
		r.pause(id)
		res, err := r.svc.ResumeInterrupted(bg, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Held).To(BeTrue())
		Expect(res.Started).To(BeFalse())
		Expect(turn(r.svc, alice, chat).ResumeAttempts).To(BeZero())
		Expect(turn(r.svc, alice, chat).Last).To(Equal(store.TurnInterrupted))
	})

	It("a continuation from the waiting messages of a running turn is not charged", func() {
		r := newGateRig(1, 0)
		_, chat := r.desk("Desk")
		Expect(r.send(reply, chat, "BLOCK").Started).To(BeTrue())
		until("the turn to run", func() bool { return turn(r.svc, alice, chat).Running })
		// A message that waits is not a run: the budget of 1 is spent already.
		Expect(r.send(reply, chat, "more").Held).To(BeFalse())
		Expect(pendingTexts(r.svc, alice, chat)).To(Equal([]string{"more"}))
		rec, _ := r.svc.Get(bg, alice, chat)
		used, err := r.store.Used(bg, rec.AgentID, coordinator.DayBucket("runs", time.Now()))
		Expect(err).NotTo(HaveOccurred())
		Expect(used).To(Equal(1))
		// The turn ends and the message goes on from the waiting ones, with no
		// second run charged and nothing held.
		r.release()
		until("the waiting message to be taken", func() bool {
			return len(pendingTexts(r.svc, alice, chat)) == 0 && !turn(r.svc, alice, chat).Running
		})
		used, err = r.store.Used(bg, rec.AgentID, coordinator.DayBucket("runs", time.Now()))
		Expect(err).NotTo(HaveOccurred())
		Expect(used).To(Equal(1))
	})

	It("a paused coordinator's turn that still runs does not take the held messages", func() {
		r := newGateRig(0, 0)
		id, chat := r.desk("Desk")
		Expect(r.send(bg, chat, "BLOCK").Started).To(BeTrue())
		until("the turn to run", func() bool { return turn(r.svc, alice, chat).Running })
		r.pause(id) // the stop is lost or late: only the flag is set
		Expect(r.send(bg, chat, "held").Held).To(BeTrue())
		r.release()
		until("the turn to end", func() bool { return !turn(r.svc, alice, chat).Running })
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		Expect(pendingTexts(r.svc, alice, chat)).To(Equal([]string{"held"}))
	})
})
