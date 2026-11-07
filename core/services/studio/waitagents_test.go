package studio_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("wait_agents", func() {
	ctx := context.Background()
	var (
		release chan struct{}
		undo    func()
	)
	BeforeEach(func() {
		release = make(chan struct{})
		undo = studio.FastWaitsForTest(20*time.Millisecond, 50*time.Millisecond, 30*time.Second, 2*time.Minute)
		DeferCleanup(undo)
	})

	// parent spawns "slow" (blocks until released), "bad" (fails) and "quick"
	// (done at once) in the background, then calls wait_agents with the
	// arguments of the message "wait <args>".
	setup := func() (*studio.Service, *studiotest.ToolHarness, string) {
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case text == "go":
				return []studiotest.ToolStep{
					{Tool: "spawn_agent", Args: `{"task":"slow job","wait":false}`},
					{Tool: "spawn_agent", Args: `{"task":"bad job","wait":false}`},
					{Tool: "spawn_agent", Args: `{"task":"quick job","wait":false}`}, {Say: "spawned"}}
			case text == "slow job":
				return []studiotest.ToolStep{{Block: release}, {Say: "slow done"}}
			case text == "bad job":
				return []studiotest.ToolStep{{Err: errFail}}
			case text == "quick job":
				return []studiotest.ToolStep{{Say: "quick done"}}
			case strings.HasPrefix(text, "wait "):
				return []studiotest.ToolStep{{Tool: "wait_agents", Args: strings.TrimPrefix(text, "wait ")}, {Say: "waited"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		until("the quick and the bad one to end", func() bool {
			subs, _ := svc.SubAgents(ctx, alice, id)
			n := 0
			for _, s := range subs {
				if !s.Turn.Running && s.Turn.Last != "" {
					n++
				}
			}
			return n == 2
		})
		return svc, h, id
	}
	call := func(svc *studio.Service, h *studiotest.ToolHarness, id, args string) string {
		n := len(h.ResultsFor("wait_agents"))
		sent(svc, alice, id, "wait "+args)
		until("wait_agents to return", func() bool { return len(h.ResultsFor("wait_agents")) > n })
		idle(svc, alice, id)
		res := h.ResultsFor("wait_agents")[n]
		if res.Err != nil {
			return "ERR " + res.Err.Error()
		}
		return res.Out
	}
	idOf := func(svc *studio.Service, id, task string) string {
		subs, _ := svc.SubAgents(ctx, alice, id)
		for _, s := range subs {
			if strings.Contains(s.Chat.Title, task) {
				return s.Chat.ID
			}
		}
		Fail("no sub-agent " + task)
		return ""
	}

	It("returns the current statuses at the timeout and leaves the running one running", func() {
		svc, h, id := setup()
		out := call(svc, h, id, `{"ids":["`+idOf(svc, id, "slow")+`"],"timeout":"1s"}`)
		Expect(out).To(ContainSubstring("Timed out"))
		Expect(out).To(ContainSubstring("slow job"))
		Expect(out).To(ContainSubstring(": running"))
		subs, _ := svc.SubAgents(ctx, alice, id)
		running := 0
		for _, s := range subs {
			if s.Turn.Running {
				running++
			}
		}
		Expect(running).To(Equal(1))
		close(release)
	})

	It("in mode all waits for the last one", func() {
		svc, h, id := setup()
		go func() { time.Sleep(300 * time.Millisecond); close(release) }()
		out := call(svc, h, id, `{"timeout":"10m"}`)
		Expect(out).To(ContainSubstring("All the sub-agents you waited for are done"))
		Expect(out).To(ContainSubstring("slow job"))
		Expect(out).NotTo(ContainSubstring("Timed out"))
	})

	It("in mode any_failed_or_all returns at once when one failed", func() {
		svc, h, id := setup()
		out := call(svc, h, id, `{"mode":"any_failed_or_all","ids":["`+idOf(svc, id, "slow")+`","`+idOf(svc, id, "bad")+`"],"timeout":"10m"}`)
		Expect(out).To(ContainSubstring("failed or was stopped"))
		Expect(out).To(ContainSubstring(": failed"))
		Expect(out).To(ContainSubstring(": running"))
		close(release)
	})

	It("reports an unknown id at once", func() {
		svc, h, id := setup()
		start := time.Now()
		out := call(svc, h, id, `{"ids":["no-such-id"],"timeout":"10m"}`)
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		Expect(out).To(HavePrefix("ERR unknown sub-agent id: no-such-id"))
		Expect(out).To(ContainSubstring("slow job"))
		close(release)
	})

	It("with no ids waits for the running children only", func() {
		svc, h, id := setup()
		go func() { time.Sleep(200 * time.Millisecond); close(release) }()
		out := call(svc, h, id, `{}`)
		Expect(out).To(ContainSubstring("All the sub-agents you waited for are done"))
		Expect(out).NotTo(ContainSubstring("quick job"), "a finished child was waited for")
		out = call(svc, h, id, `{}`)
		Expect(out).To(HavePrefix("No sub-agent runs now."))
	})

	It("stops when the turn is cancelled", func() {
		svc, _, id := setup()
		sent(svc, alice, id, `wait {"timeout":"10m"}`)
		until("the wait to start", func() bool { return turn(svc, alice, id).Running })
		start := time.Now()
		Expect(svc.Stop(ctx, alice, id)).To(Succeed())
		Eventually(func() bool { return turn(svc, alice, id).Running }, 10*time.Second, 20*time.Millisecond).Should(BeFalse())
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		close(release)
	})

	It("keeps the timeout between its bounds", func() {
		svc, h, id := setup()
		start := time.Now()
		out := call(svc, h, id, `{"ids":["`+idOf(svc, id, "slow")+`"],"timeout":"1ms"}`)
		Expect(out).To(ContainSubstring("Timed out after 50ms"), "the shortest timeout is the minimum")
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		close(release)
	})
})
