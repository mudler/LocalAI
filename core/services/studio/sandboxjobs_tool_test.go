package studio_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// addonJobTable is the Sandbox add-on's jobs tool with an in-memory table: a
// job runs until the test ends it, and a restart forgets every job.
type addonJobTable struct {
	mu    sync.Mutex
	n     int
	jobs  map[string]*contract.JobInfo
	out   map[string]string
	scope map[string][2]string
	verbs []string
}

func newAddonJobTable() *addonJobTable {
	return &addonJobTable{jobs: map[string]*contract.JobInfo{}, out: map[string]string{}, scope: map[string][2]string{}}
}

func (t *addonJobTable) CallJobs(_ context.Context, user, conv, _ string, a contract.JobArgs) (contract.JobReply, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.verbs = append(t.verbs, a.Verb)
	if a.Verb == contract.JobStart {
		t.n++
		id := fmt.Sprintf("job-aaaaa%c", 'a'+rune(t.n))
		now := time.Now().UTC()
		t.jobs[id] = &contract.JobInfo{ID: id, Label: a.Label, State: "running", Exit: -1, Started: now, LastOutput: now}
		t.scope[id] = [2]string{user, conv}
		in := *t.jobs[id]
		return contract.JobReply{OK: true, Job: &in}, nil
	}
	if a.Verb == contract.JobList {
		var out []contract.JobInfo
		for id, in := range t.jobs {
			if t.scope[id] == [2]string{user, conv} {
				out = append(out, *in)
			}
		}
		return contract.JobReply{OK: true, Jobs: out}, nil
	}
	in := t.jobs[a.ID]
	if in == nil || t.scope[a.ID] != [2]string{user, conv} {
		return contract.JobReply{OK: false, Code: contract.JobCodeUnknown, Error: fmt.Sprintf("No job %q in this chat.", a.ID)}, nil
	}
	cp := *in
	switch a.Verb {
	case contract.JobRead:
		out := t.out[a.ID]
		return contract.JobReply{OK: true, Job: &cp, Output: out, From: 0, Next: int64(len(out)), Tail: out}, nil
	case contract.JobInput:
		if in.State != "running" {
			return contract.JobReply{OK: false, Code: contract.JobCodeEnded, Error: a.ID + " has ended."}, nil
		}
		return contract.JobReply{OK: true, Job: &cp, Written: len(a.Input)}, nil
	case contract.JobKill:
		in.State, in.End, in.Exit, in.Ended = "killed", "killed", 143, time.Now().UTC()
		cp = *in
		return contract.JobReply{OK: true, Job: &cp}, nil
	}
	return contract.JobReply{OK: false, Code: contract.JobCodeInvalid, Error: "bad verb"}, nil
}

func (t *addonJobTable) exit(id string, code int, out string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	in := t.jobs[id]
	in.State, in.End, in.Exit, in.Ended = "exited", "exit", code, time.Now().UTC()
	in.Total = int64(len(out))
	t.out[id] = out
}

func (t *addonJobTable) restart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.jobs, t.out, t.scope = map[string]*contract.JobInfo{}, map[string]string{}, map[string][2]string{}
}

var _ = Describe("the job tool of a Sandbox chat", func() {
	newSandboxJobChat := func() (jobChat, *addonJobTable) {
		table := newAddonJobTable()
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		c := newJobChat("on", fa, func(d *studio.Deps) { d.SandboxJobs = studio.NewSandboxJobs(table, nil) })
		return c, table
	}

	It("starts the job in the add-on, records its stub and the end the add-on reports, and never runs it here", func() {
		c, table := newSandboxJobChat()
		id := create(c.svc, alice)
		out := c.say(alice, id, "start build make all")
		Expect(out).To(MatchRegexp(`^Started job job-aaaaab \("build"\) in the sandbox\.`))
		Expect(out).To(HaveSuffix("```\nmake all\n```"))
		_, _, node := c.host.reg.Counts(alice, id)
		Expect(node).To(Equal(0), "nothing started on this machine")
		stubs := c.stubs(alice, id)
		Expect(stubs).To(HaveLen(1))
		Expect(stubs[0]).To(SatisfyAll(HaveField("Provider", "sandbox"), HaveField("Instance", "addon:sandbox"), HaveField("State", bgjob.StateRunning)))

		Expect(c.say(alice, id, "input job-aaaaab yes")).To(Equal("Wrote 4 bytes to job-aaaaab."))
		Expect(c.say(alice, id, "list")).To(ContainSubstring(`job-aaaaab "build": running`))

		table.exit("job-aaaaab", 1, "FAILED\n")
		out = c.say(alice, id, "read job-aaaaab")
		Expect(out).To(ContainSubstring(`job-aaaaab "build": exited with status 1`))
		Expect(out).To(ContainSubstring("FAILED"))
		Expect(out).NotTo(ContainSubstring("no longer kept"))
		Expect(c.stubs(alice, id)[0]).To(SatisfyAll(HaveField("State", bgjob.StateExited), HaveField("Exit", 1)))
		Expect(c.say(alice, id, "input job-aaaaab more")).To(Equal("job-aaaaab has ended."))
	})

	It("marks a job lost when the add-on restarted, and keeps the chat's limit of 4 live jobs", func() {
		c, table := newSandboxJobChat()
		id := create(c.svc, alice)
		for range bgjob.PerChat {
			Expect(c.say(alice, id, "start loop sleep 99")).To(HavePrefix("Started job"))
		}
		Expect(c.say(alice, id, "start one-more true")).To(Equal("Not started: this chat runs 4 jobs, the most it may."))
		table.restart()
		Expect(c.say(alice, id, "read job-aaaaab")).To(ContainSubstring("job-aaaaab is gone"))
		Expect(c.say(alice, id, "list")).To(ContainSubstring("gone with its node"))
		for _, st := range c.stubs(alice, id) {
			Expect(st.State).To(Equal(bgjob.StateLost))
		}
		Expect(c.say(alice, id, "start again true")).To(HavePrefix("Started job"), "lost jobs free their places")
	})

	It("keeps the limit of 8 live jobs per user across chats, counted from the stubs", func() {
		c, table := newSandboxJobChat()
		a, b, third := create(c.svc, alice), create(c.svc, alice), create(c.svc, alice)
		for range bgjob.PerChat {
			Expect(c.say(alice, a, "start loop sleep 99")).To(HavePrefix("Started job"))
			Expect(c.say(alice, b, "start loop sleep 99")).To(HavePrefix("Started job"))
		}
		Expect(c.say(alice, third, "start one-more true")).To(Equal("Not started: you run 8 jobs, the most you may."))
		// A finished job frees a place.
		table.exit("job-aaaaab", 0, "")
		Expect(c.say(alice, a, "read job-aaaaab")).To(ContainSubstring("exited"))
		Expect(c.say(alice, third, "start again true")).To(HavePrefix("Started job"))
	})

	It("counts only the Sandbox jobs against its limits; the host registry bounds the built-in ones", func() {
		c, _ := newSandboxJobChat()
		host, id := create(c.svc, alice), create(c.svc, alice)
		for i := range bgjob.PerUser {
			c.svc.RecordJob(context.Background(), bgjob.Info{ID: fmt.Sprintf("job-host0%d", i), Owner: alice, Chat: host,
				Instance: "node-a", Provider: studio.BuiltinProvider, Label: "build", State: bgjob.StateRunning, Started: time.Now()})
		}
		Expect(c.stubs(alice, host)).To(HaveLen(bgjob.PerUser))
		Expect(c.say(alice, id, "start build make")).To(HavePrefix("Started job"))
	})

	It("wakes a wait on a Sandbox job through the routing observer when the add-on reports its end", func() {
		table := newAddonJobTable()
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		var job string
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case text == "start":
				return []studiotest.ToolStep{{Tool: "job", Args: `{"action":"start","label":"build","command":"make"}`}, {Say: "started"}}
			case text == "arm":
				return []studiotest.ToolStep{{Tool: "wait_for", Args: `{"job":"` + job + `","until":"exit","every":"10m","then":"Report the build."}`}, {Say: "armed"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{}, func(d *studio.Deps) {
			d.AddonTools = fa
			d.HostExecMode = func() string { return "on" }
			d.SandboxJobs = studio.NewSandboxJobs(table, nil)
		})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "start")
		idle(r.svc, alice, chat)
		job = "job-aaaaab"
		Expect(r.h.ResultsFor("job")[0].Out).To(HavePrefix("Started job " + job))
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)
		list, err := r.host.List(context.Background(), alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1), "%v", r.h.ResultsFor("wait_for"))
		Expect(list[0].Watch.Node).To(Equal("addon:sandbox"))

		table.exit(job, 0, "PASS\n")
		r.sh.clock.Advance(10 * time.Minute)
		until("the wake-up run", func() bool {
			r.pass()
			return len(r.h.Sent(chat)) >= 3
		})
		r.settled(list[0].ID)
		wake := r.h.Sent(chat)[2]
		Expect(wake).To(ContainSubstring("[Wait ended: job " + job + ` ("build") exited with status 0`))
		Expect(strings.Contains(wake, "PASS")).To(BeTrue())
		rec, err := r.svc.Get(context.Background(), alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Jobs[0].State).To(Equal(bgjob.StateExited), "the observer kept the stub in step")
	})
	It("makes a wait on a Sandbox job due soon after the add-on ends it, with no read of the model", func() {
		defer studio.FastSandboxWatchForTest(20 * time.Millisecond)()
		table := newAddonJobTable()
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "start":
				return []studiotest.ToolStep{{Tool: "job", Args: `{"action":"start","label":"build","command":"make"}`}, {Say: "started"}}
			case "arm":
				return []studiotest.ToolStep{{Tool: "wait_for", Args: `{"job":"job-aaaaab","until":"exit","every":"10m","then":"Report."}`}, {Say: "armed"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{}, func(d *studio.Deps) {
			d.AddonTools = fa
			d.HostExecMode = func() string { return "on" }
			d.SandboxJobs = studio.NewSandboxJobs(table, nil)
		})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "start")
		idle(r.svc, alice, chat)
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)
		list, err := r.host.List(context.Background(), alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		until("the first check to push the wait to its interval", func() bool {
			r.pass()
			return r.get(list[0].ID).Next.After(r.sh.clock.Now().UTC())
		})

		table.exit("job-aaaaab", 0, "PASS\n")
		until("the wait to be due", func() bool { return !r.get(list[0].ID).Next.After(r.sh.clock.Now().UTC()) })
	})
})
