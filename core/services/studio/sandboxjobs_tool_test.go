package studio_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
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
	// kills are the kill calls, and gate, when set, holds each start until
	// it is closed. sameID makes every start answer with the first job's id.
	kills  []contract.JobArgs
	gate   chan struct{}
	sameID bool
}

func newAddonJobTable() *addonJobTable {
	return &addonJobTable{jobs: map[string]*contract.JobInfo{}, out: map[string]string{}, scope: map[string][2]string{}}
}

func (t *addonJobTable) CallJobs(_ context.Context, user, conv, _ string, a contract.JobArgs) (contract.JobReply, error) {
	t.mu.Lock()
	gate := t.gate
	t.mu.Unlock()
	if a.Verb == contract.JobStart && gate != nil {
		<-gate
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.verbs = append(t.verbs, a.Verb)
	if a.Verb == contract.JobStart {
		t.n++
		id := fmt.Sprintf("job-aaaaa%c", 'a'+rune(t.n))
		if t.sameID {
			id = "job-aaaaab"
		}
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
		t.kills = append(t.kills, a)
		if in.State != "running" {
			return contract.JobReply{OK: false, Code: contract.JobCodeEnded, Error: a.ID + " has ended."}, nil
		}
		in.State, in.End, in.Exit, in.Ended = "killed", "killed", 143, time.Now().UTC()
		if a.Reason != "" && a.Reason != "stopped" {
			in.State = "exited"
		}
		if a.Reason != "" {
			in.End = a.Reason
		}
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

func (t *addonJobTable) state(id string) contract.JobInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.jobs[id]
}

func (t *addonJobTable) killArgs() []contract.JobArgs {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.kills)
}

func (t *addonJobTable) wrote(id string, at time.Time, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.jobs[id].LastOutput, t.jobs[id].Total = at, n
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

	It("shows the person a Sandbox job's end and last output with no call of the model", func() {
		c, table := newSandboxJobChat()
		id := create(c.svc, alice)
		Expect(c.say(alice, id, "start build make")).To(HavePrefix("Started job"))
		at := time.Now().UTC().Add(-3 * time.Second).Truncate(time.Second)
		table.wrote("job-aaaaab", at, 512)
		list, err := c.svc.BgJobs(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].LastOutput.Equal(at)).To(BeTrue(), "the add-on's last output reaches the view")
		Expect(list[0].Total).To(BeEquivalentTo(512))

		table.exit("job-aaaaab", 2, "boom\n")
		list, err = c.svc.BgJobs(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(list[0]).To(SatisfyAll(HaveField("JobStub.State", bgjob.StateExited), HaveField("JobStub.Exit", 2)))
		Expect(c.stubs(alice, id)[0].State).To(Equal(bgjob.StateExited), "the view brought the stub in step")
	})

	It("stops a Sandbox job as stopped, and says an ended one has ended instead of leaving it ending", func() {
		c, table := newSandboxJobChat()
		id := create(c.svc, alice)
		c.say(alice, id, "start one sleep 99")
		c.say(alice, id, "start two sleep 99")
		v, err := c.svc.StopJob(context.Background(), alice, id, "job-aaaaab")
		Expect(err).NotTo(HaveOccurred())
		Expect(v.End).To(Equal(bgjob.EndStopped))
		Expect(v.State).To(Equal(bgjob.StateKilled))
		Expect(table.killArgs()[0].Reason).To(Equal("stopped"))
		Expect(c.stubs(alice, id)[0]).To(SatisfyAll(HaveField("State", bgjob.StateKilled), HaveField("End", bgjob.EndStopped)))

		table.exit("job-aaaaac", 0, "")
		_, err = c.svc.StopJob(context.Background(), alice, id, "job-aaaaac")
		Expect(err).To(MatchError(studio.ErrBgJobEnded))
		stubs := c.stubs(alice, id)
		Expect(stubs[slices.IndexFunc(stubs, func(st session.JobStub) bool { return st.ID == "job-aaaaac" })].State).To(Equal(bgjob.StateExited))
	})

	It("does not let the stale stubs of a restarted add-on block the person in other chats", func() {
		c, table := newSandboxJobChat()
		a, b, third := create(c.svc, alice), create(c.svc, alice), create(c.svc, alice)
		for range bgjob.PerChat {
			Expect(c.say(alice, a, "start loop sleep 99")).To(HavePrefix("Started job"))
			Expect(c.say(alice, b, "start loop sleep 99")).To(HavePrefix("Started job"))
		}
		table.restart()
		Expect(c.say(alice, third, "start again true")).To(HavePrefix("Started job"))
		for _, st := range c.stubs(alice, a) {
			Expect(st.State).To(Equal(bgjob.StateLost))
		}
	})

	It("lets two starts at once pass the person's limit only once", func() {
		c, table := newSandboxJobChat()
		a, b := create(c.svc, alice), create(c.svc, alice)
		for range bgjob.PerChat {
			Expect(c.say(alice, a, "start loop sleep 99")).To(HavePrefix("Started job"))
		}
		for range bgjob.PerChat - 1 {
			Expect(c.say(alice, b, "start loop sleep 99")).To(HavePrefix("Started job"))
		}
		third, fourth := create(c.svc, alice), create(c.svc, alice)
		gate := make(chan struct{})
		table.mu.Lock()
		table.gate = gate
		table.mu.Unlock()
		sent(c.svc, alice, third, "start x true")
		sent(c.svc, alice, fourth, "start y true")
		// One start holds the last place and waits in the add-on; the other
		// finds the person full.
		until("one start to be refused", func() bool {
			for _, r := range c.h.ResultsFor("job") {
				if r.Out == "Not started: you run 8 jobs, the most you may." {
					return true
				}
			}
			return false
		})
		close(gate)
		idle(c.svc, alice, third)
		idle(c.svc, alice, fourth)
		started := 0
		for _, chat := range []string{third, fourth} {
			for _, st := range c.stubs(alice, chat) {
				Expect(st.ID).To(HavePrefix("job-"), "no place is left held")
				started++
			}
		}
		Expect(started).To(Equal(1))
	})

	It("refuses a start whose job id the chat already has, and keeps the stub it has", func() {
		c, table := newSandboxJobChat()
		id := create(c.svc, alice)
		Expect(c.say(alice, id, "start build make")).To(HavePrefix("Started job job-aaaaab"))
		table.mu.Lock()
		table.sameID = true
		table.mu.Unlock()
		Expect(c.say(alice, id, "start other true")).To(ContainSubstring("not valid"))
		stubs := c.stubs(alice, id)
		Expect(stubs).To(HaveLen(1))
		Expect(stubs[0].Label).To(Equal("build"))
	})

	It("starts nothing on this machine when the add-on that serves bash cannot work, or the tools cannot be read", func() {
		table := newAddonJobTable()
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		gate := gatedAddons{fakeAddons: fa, reasons: map[string]string{}}
		var failPins bool
		c := newJobChat("on", gate, func(d *studio.Deps) {
			d.SandboxJobs = studio.NewSandboxJobs(table, nil)
			d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) {
				if failPins {
					return nil, errors.New("the tool list is not readable")
				}
				return nil, nil
			}
		})
		id := create(c.svc, alice)
		gate.reasons["sandbox"] = "no sandbox backend works on this host"
		out, err := c.svc.StartJobForTest(context.Background(), alice, id, "build", "make")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("Not started: the sandbox add-on cannot be used in this chat: no sandbox backend works on this host."))

		delete(gate.reasons, "sandbox")
		failPins = true
		out, err = c.svc.StartJobForTest(context.Background(), alice, id, "build", "make")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("Not started: the tools of this chat could not be read just now; try again."))
		_, _, node := c.host.reg.Counts(alice, id)
		Expect(node).To(BeZero(), "nothing ran on this machine")
		Expect(table.verbs).To(BeEmpty())
	})

	It("ends the Sandbox jobs of a chat whose commands were switched off, with that reason", func() {
		defer studio.FastSandboxSweepForTest(20 * time.Millisecond)()
		c, table := newSandboxJobChat()
		id := create(c.svc, alice)
		Expect(c.say(alice, id, "start server serve")).To(HavePrefix("Started job"))
		Consistently(func() string { return table.state("job-aaaaab").State }, "100ms", "20ms").Should(Equal("running"), "nothing ends a job that is allowed")
		Expect(c.svc.SetBuiltinsOff(context.Background(), alice, id, []string{"shell"})).To(Succeed())
		Eventually(func() string { return table.state("job-aaaaab").End }).Should(Equal("group_off"))
		Eventually(func() bgjob.EndReason { return c.stubs(alice, id)[0].End }).Should(Equal(bgjob.EndGroupOff))
	})

	It("ends the Sandbox jobs of a deleted chat as chat_deleted", func() {
		c, table := newSandboxJobChat()
		id := create(c.svc, alice)
		Expect(c.say(alice, id, "start server serve")).To(HavePrefix("Started job"))
		Expect(c.svc.Delete(context.Background(), alice, id)).To(Succeed())
		Eventually(func() string { return table.state("job-aaaaab").End }).Should(Equal("chat_deleted"))
	})
})
