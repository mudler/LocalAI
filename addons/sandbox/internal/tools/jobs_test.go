//go:build linux || darwin

// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recEgress records the job operations the proxy was opened for.
type recEgress struct {
	mu    sync.Mutex
	begun []string
	ended []string
}

func (r *recEgress) BeginJob(user, conv, op string) func() {
	r.mu.Lock()
	r.begun = append(r.begun, user+"/"+conv+"/"+op)
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			r.ended = append(r.ended, op)
			r.mu.Unlock()
		})
	}
}

func (r *recEgress) endedOps() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ended...)
}

// jobsRig is the runtime over a real runner on the fake backend: the commands
// run on this machine, as in the host backend.
type jobsRig struct {
	st     workspace.Store
	m      *runner.Manager
	rt     contract.ActRuntime
	egress *recEgress
	n      int
}

var admin = workspace.Actor{Principal: "admin"}

func newJobsRig(lifetime, grace time.Duration) *jobsRig {
	ctx := context.Background()
	st := sbtest.OpenStore()
	for _, u := range []string{"alice", "bob"} {
		_, err := st.Put(ctx, admin, "/users/"+u+"/keep.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Grant(ctx, admin, u, "/users/"+u, workspace.ModeWrite)).To(Succeed())
	}
	backend := sbtest.NewFakeBackend(GinkgoT().TempDir())
	m, err := runner.New(runner.Options{
		Backend: func() (sandbox.Backend, error) { return backend, nil },
		Store:   st,
		Actor: func(_ context.Context, user, conv string) (workspace.Actor, error) {
			return workspace.Actor{Principal: user, ChatID: conv}, nil
		},
		View: func(ctx context.Context, a workspace.Actor) (workspace.View, error) {
			return st.View(ctx, a, []workspace.MountRequest{{Path: "/users/" + a.Principal, At: "home", Mode: workspace.ModeWrite}})
		},
		Tools: tools.Runs,
	})
	Expect(err).NotTo(HaveOccurred())
	eg := &recEgress{}
	rt := tools.New(m, nil, nil, tools.WithJobEgress(eg), tools.WithJobClock(time.Now, lifetime, grace))
	DeferCleanup(func() { _ = rt.(interface{ Close(context.Context) error }).Close(context.Background()) })
	return &jobsRig{st: st, m: m, rt: rt.(contract.ActRuntime), egress: eg}
}

// job calls the job tool as user in conv with a fresh operation.
func (r *jobsRig) job(user, conv string, a contract.JobArgs) contract.JobReply {
	GinkgoHelper()
	r.n++
	raw, _ := json.Marshal(a)
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	e := env(tools.ToolJob, user, conv, args)
	e.Request.OperationID = "jobop" + strconv.Itoa(r.n)
	res, err := r.rt.Act(context.Background(), e)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(res.Payload)).To(BeNumerically("<=", contract.MaxJobReplyBytes))
	var out contract.JobReply
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
	Expect(out.Validate(a)).To(Succeed(), "the add-on's answer passes the host's check: %+v", out)
	return out
}

func (r *jobsRig) start(user, conv, cmd string) string {
	GinkgoHelper()
	out := r.job(user, conv, contract.JobArgs{Verb: contract.JobStart, Command: cmd, Label: "test"})
	Expect(out.OK).To(BeTrue(), "start: %+v", out)
	return out.Job.ID
}

// finished waits until the job has ended and returns it.
func (r *jobsRig) finished(user, conv, id string) contract.JobInfo {
	GinkgoHelper()
	var in contract.JobInfo
	Eventually(func() bool {
		out := r.job(user, conv, contract.JobArgs{Verb: contract.JobRead, ID: id, Observe: true})
		in = *out.Job
		return in.Finished()
	}).WithTimeout(20 * time.Second).WithPolling(50 * time.Millisecond).Should(BeTrue())
	return in
}

var _ = Describe("background jobs in the add-on", func() {
	It("runs a job after the call returns, keeps its output and exit status, and commits what it wrote", func() {
		r := newJobsRig(time.Hour, time.Second)
		id := r.start("alice", "c1", "echo one; sleep 0.3; echo two > out.txt; echo three; exit 3")
		Expect(contract.ValidJobID(id)).To(BeTrue())
		in := r.finished("alice", "c1", id)
		Expect(in.State).To(Equal("exited"))
		Expect(in.End).To(Equal("exit"))
		Expect(in.Exit).To(Equal(3))
		read := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: id})
		Expect(read.Output).To(Equal("one\nthree\n"))
		Expect(read.From).To(BeZero())
		Expect(read.Next).To(Equal(int64(len("one\nthree\n"))))
		// The model's cursor moved: the next read has nothing new.
		again := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: id})
		Expect(again.Output).To(BeEmpty())
		Expect(again.From).To(Equal(read.Next))
		// An absolute offset reads again.
		since := int64(4)
		Expect(r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: id, Since: &since}).Output).To(Equal("three\n"))
		// The final checkpoint committed the file of the job.
		rc, _, err := r.st.Open(context.Background(), admin, "/users/alice/out.txt")
		Expect(err).NotTo(HaveOccurred())
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		Expect(string(b)).To(Equal("two\n"))
		// The job's network was opened under the start's operation and closed at its end.
		Expect(r.egress.begun).To(HaveLen(1))
		Expect(r.egress.begun[0]).To(HavePrefix("alice/c1/jobop"))
		Expect(r.egress.endedOps()).To(HaveLen(1))
		Expect(r.m.LiveJobs("alice", "c1")).To(BeZero(), "the sandbox is let go")
	})

	It("keeps a job running when the start call's context ends", func() {
		r := newJobsRig(time.Hour, time.Second)
		raw, _ := json.Marshal(map[string]any{"verb": "start", "command": "sleep 0.5; echo late"})
		var args map[string]any
		_ = json.Unmarshal(raw, &args)
		ctx, cancel := context.WithCancel(context.Background())
		res, err := r.rt.Act(ctx, env(tools.ToolJob, "alice", "c1", args))
		cancel()
		Expect(err).NotTo(HaveOccurred())
		var out contract.JobReply
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		Expect(out.OK).To(BeTrue())
		in := r.finished("alice", "c1", out.Job.ID)
		Expect(in.Exit).To(BeZero())
		Expect(r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: out.Job.ID}).Output).To(Equal("late\n"))
	})

	It("writes input to a job, closes it, and refuses input to an ended job", func() {
		r := newJobsRig(time.Hour, time.Second)
		id := r.start("alice", "c1", "while read l; do echo got:$l; done; echo bye")
		w := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobInput, ID: id, Input: "hello\n"})
		Expect(w.OK).To(BeTrue())
		Expect(w.Written).To(Equal(6))
		w = r.job("alice", "c1", contract.JobArgs{Verb: contract.JobInput, ID: id, CloseInput: true})
		Expect(w.OK).To(BeTrue())
		in := r.finished("alice", "c1", id)
		Expect(in.Exit).To(BeZero())
		Expect(r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: id}).Output).To(Equal("got:hello\nbye\n"))
		late := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobInput, ID: id, Input: "x"})
		Expect(late.OK).To(BeFalse())
		Expect(late.Code).To(Equal(contract.JobCodeEnded))
		Expect(late.Error).To(ContainSubstring("has ended (exited with status 0)"))
	})

	It("does not hold the call when the job does not read its input", func() {
		r := newJobsRig(time.Hour, time.Second)
		id := r.start("alice", "c1", "sleep 30")
		start := time.Now()
		// More than a pipe takes at once would block a writer that waits.
		w := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobInput, ID: id, Input: strings.Repeat("y", contract.MaxJobInputBytes)})
		Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second))
		Expect(w.OK).To(BeTrue())
		r.job("alice", "c1", contract.JobArgs{Verb: contract.JobKill, ID: id, Signal: "kill"})
		r.finished("alice", "c1", id)
	})

	It("kills with term, then with kill after the grace time, and reports the signal", func() {
		r := newJobsRig(time.Hour, 300*time.Millisecond)
		id := r.start("alice", "c1", "sleep 30")
		k := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobKill, ID: id})
		Expect(k.OK).To(BeTrue())
		in := r.finished("alice", "c1", id)
		Expect(in.State).To(Equal("killed"))
		Expect(in.End).To(Equal("killed"))
		Expect(in.Exit).To(Equal(128 + 15))

		// A job that ignores term gets kill after the grace time.
		id = r.start("alice", "c1", "trap '' TERM; echo ready; while true; do sleep 0.05; done")
		Eventually(func() string {
			return r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: id, Observe: true}).Tail
		}).WithTimeout(10 * time.Second).Should(ContainSubstring("ready"))
		r.job("alice", "c1", contract.JobArgs{Verb: contract.JobKill, ID: id, Signal: "term"})
		in = r.finished("alice", "c1", id)
		Expect(in.State).To(Equal("killed"))
		Expect(in.Exit).To(Equal(128 + 9))
		again := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobKill, ID: id})
		Expect(again.Code).To(Equal(contract.JobCodeEnded))
	})

	It("records the end reason the host gives with a kill", func() {
		r := newJobsRig(time.Hour, 300*time.Millisecond)
		id := r.start("alice", "c1", "sleep 30")
		Expect(r.job("alice", "c1", contract.JobArgs{Verb: contract.JobKill, ID: id, Reason: "group_off"}).OK).To(BeTrue())
		in := r.finished("alice", "c1", id)
		Expect(in.End).To(Equal("group_off"))
		Expect(in.State).To(Equal("exited"))

		id = r.start("alice", "c1", "sleep 30")
		Expect(r.job("alice", "c1", contract.JobArgs{Verb: contract.JobKill, ID: id, Reason: "stopped"}).OK).To(BeTrue())
		in = r.finished("alice", "c1", id)
		Expect(in.End).To(Equal("stopped"))
		Expect(in.State).To(Equal("killed"))
	})

	It("ends a job at its lifetime", func() {
		r := newJobsRig(300*time.Millisecond, 200*time.Millisecond)
		id := r.start("alice", "c1", "sleep 30")
		in := r.finished("alice", "c1", id)
		Expect(in.End).To(Equal("lifetime"))
		Expect(in.State).To(Equal("exited"))
	})

	It("ends the jobs when the add-on closes", func() {
		r := newJobsRig(time.Hour, 200*time.Millisecond)
		id := r.start("alice", "c1", "sleep 30")
		Expect(r.m.Close(context.Background())).To(Succeed())
		in := r.finished("alice", "c1", id)
		Expect(in.End).To(Equal("shutdown"))
	})

	It("never shows a job to another person or another chat", func() {
		r := newJobsRig(time.Hour, time.Second)
		id := r.start("alice", "c1", "sleep 30")
		for _, who := range [][2]string{{"bob", "c1"}, {"alice", "c2"}} {
			for _, a := range []contract.JobArgs{
				{Verb: contract.JobRead, ID: id},
				{Verb: contract.JobInput, ID: id, Input: "x"},
				{Verb: contract.JobKill, ID: id},
			} {
				out := r.job(who[0], who[1], a)
				Expect(out.OK).To(BeFalse(), "%v reached %s", who, a.Verb)
				Expect(out.Code).To(Equal(contract.JobCodeUnknown))
			}
			Expect(r.job(who[0], who[1], contract.JobArgs{Verb: contract.JobList}).Jobs).To(BeEmpty())
		}
		list := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobList})
		Expect(list.Jobs).To(HaveLen(1))
		Expect(list.Jobs[0].State).To(Equal("running"))
		Expect(r.job("alice", "c1", contract.JobArgs{Verb: contract.JobKill, ID: id, Signal: "kill"}).OK).To(BeTrue())
	})

	It("refuses a traversal in a job id, a long input and an unknown argument", func() {
		r := newJobsRig(time.Hour, time.Second)
		for _, args := range []map[string]any{
			{"verb": "read", "id": "../../etc/passwd"},
			{"verb": "read", "id": "job-abcdef/.."},
			{"verb": "input", "id": "job-abcdef", "input": strings.Repeat("a", contract.MaxJobInputBytes+1)},
			{"verb": "start", "command": "true", "label": "a\nb"},
		} {
			res, err := r.rt.Act(context.Background(), env(tools.ToolJob, "alice", "c1", args))
			Expect(err).NotTo(HaveOccurred())
			var out contract.JobReply
			Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
			Expect(out.Code).To(Equal(contract.JobCodeInvalid), "%v", args)
		}
		_, err := r.rt.Act(context.Background(), env(tools.ToolJob, "alice", "c1", map[string]any{"verb": "list", "user": "bob"}))
		Expect(err).To(HaveOccurred(), "an argument the tool does not know is refused")
	})

	It("starts a job in the work folder the host named, with $WORK set", func() {
		r := newJobsRig(time.Hour, time.Second)
		const work = "/workspace/home/Chats/2026-10-18 Café · Q1/work"
		_, err := r.st.Put(context.Background(), admin, "/users/alice/Chats/2026-10-18 Café · Q1/work/keep.txt", strings.NewReader("k"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		args := map[string]any{"verb": "start", "command": `basename "$PWD"; cd "$WORK" && ls`, sandbox.CallArgKey: map[string]any{"workDir": work}}
		e := env(tools.ToolJob, "alice", "c1", args)
		e.Request.OperationID = "jobopwork"
		res, err := r.rt.Act(context.Background(), e)
		Expect(err).NotTo(HaveOccurred())
		var out contract.JobReply
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		Expect(out.OK).To(BeTrue(), "%+v", out)
		r.finished("alice", "c1", out.Job.ID)
		Expect(r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: out.Job.ID}).Output).To(Equal("work\nkeep.txt\n"))
	})

	It("does not start a job for a check that must change no file", func() {
		r := newJobsRig(time.Hour, time.Second)
		args := map[string]any{"verb": "start", "command": "true", sandbox.CallArgKey: map[string]any{"readOnly": true}}
		res, err := r.rt.Act(context.Background(), env(tools.ToolJob, "alice", "c1", args))
		Expect(err).NotTo(HaveOccurred())
		var out contract.JobReply
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		Expect(out.OK).To(BeFalse())
	})

	It("keeps the last 256 KiB, says where the kept bytes start, and bounds a read of noisy output", func() {
		r := newJobsRig(time.Hour, time.Second)
		// 400 KiB of control characters: each is six bytes once escaped in JSON.
		id := r.start("alice", "c1", `head -c 409600 /dev/zero | tr '\0' '\001'`)
		r.finished("alice", "c1", id)
		since := int64(0)
		out := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobRead, ID: id, Since: &since, MaxBytes: contract.MaxJobReadBytes})
		Expect(out.Job.Total).To(Equal(int64(409600)))
		Expect(out.From).To(Equal(int64(409600 - contract.JobRingBytes)))
		Expect(out.Next).To(BeNumerically(">", out.From))
		Expect(out.Next-out.From).To(BeNumerically("<", contract.MaxJobReadBytes), "the read shrank to fit the answer")
	})

	It("refuses a job beyond the most a process runs", func() {
		r := newJobsRig(time.Hour, 200*time.Millisecond)
		for range contract.MaxJobsPerProcess {
			r.start("alice", "c1", "sleep 30")
		}
		out := r.job("alice", "c1", contract.JobArgs{Verb: contract.JobStart, Command: "true"})
		Expect(out.OK).To(BeFalse())
		Expect(out.Code).To(Equal(contract.JobCodeLimit))
	})

	It("says it cannot run jobs when the runner has none", func() {
		raw := map[string]any{"verb": "list"}
		out, err := act(&fakeRunner{}, env(tools.ToolJob, "alice", "c1", raw))
		Expect(err).NotTo(HaveOccurred())
		Expect(out["ok"]).To(Equal(false))
		Expect(out["error"]).To(ContainSubstring("cannot run background jobs"))
	})
})
