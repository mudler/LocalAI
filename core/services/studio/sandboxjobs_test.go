package studio_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeJobCaller answers the jobs tool with a function and records the calls.
type fakeJobCaller struct {
	mu     sync.Mutex
	calls  []contract.JobArgs
	scopes [][3]string
	answer func(contract.JobArgs) (contract.JobReply, error)
}

func (f *fakeJobCaller) CallJobs(_ context.Context, user, conv, addon string, a contract.JobArgs) (contract.JobReply, error) {
	f.mu.Lock()
	f.calls = append(f.calls, a)
	f.scopes = append(f.scopes, [3]string{user, conv, addon})
	f.mu.Unlock()
	return f.answer(a)
}

var _ = Describe("the jobs of a Sandbox chat, host side", func() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	running := contract.JobInfo{ID: "job-abcdef", Label: "build", State: "running", Exit: -1, Started: now, LastOutput: now, Total: 4000, IdleMS: 1500}
	scope := studio.SandboxJobScope{User: "alice", Chat: "c1", AddonID: "sandbox"}
	var (
		caller *fakeJobCaller
		events []studio.SandboxJobEvent
		jobs   *studio.SandboxJobs
	)
	BeforeEach(func() {
		events = nil
		caller = &fakeJobCaller{answer: func(a contract.JobArgs) (contract.JobReply, error) {
			switch a.Verb {
			case contract.JobList:
				return contract.JobReply{OK: true, Jobs: []contract.JobInfo{running}}, nil
			case contract.JobRead:
				return contract.JobReply{OK: true, Job: &running, Output: "hello", From: 100, Next: 105, Tail: strings.Repeat("t", 3000)}, nil
			case contract.JobInput:
				return contract.JobReply{OK: true, Job: &running, Written: len(a.Input)}, nil
			}
			return contract.JobReply{OK: true, Job: &running}, nil
		}}
		jobs = studio.NewSandboxJobs(caller, func(_ context.Context, ev studio.SandboxJobEvent) { events = append(events, ev) })
	})

	It("starts a job in the chat's add-on, audits the command, and names where it runs", func() {
		in, err := jobs.Start(ctx, scope, "make all", "build")
		Expect(err).NotTo(HaveOccurred())
		Expect(in).To(SatisfyAll(HaveField("ID", "job-abcdef"), HaveField("Owner", "alice"), HaveField("Chat", "c1"),
			HaveField("Provider", "sandbox"), HaveField("Instance", "addon:sandbox"), HaveField("State", bgjob.StateRunning)))
		Expect(caller.scopes).To(Equal([][3]string{{"alice", "c1", "sandbox"}}))
		Expect(events).To(ConsistOf(SatisfyAll(HaveField("Kind", "job_start"), HaveField("Job", "job-abcdef"), HaveField("Text", "make all"), HaveField("Chat", "c1"))))
	})

	It("audits no start the add-on refused", func() {
		caller.answer = func(contract.JobArgs) (contract.JobReply, error) {
			return contract.JobReply{Code: contract.JobCodeLimit, Error: "Not started: this server runs 32 jobs, the most it may."}, nil
		}
		_, err := jobs.Start(ctx, scope, "make all", "build")
		Expect(err).To(HaveOccurred())
		Expect(events).To(BeEmpty())
	})

	It("ends a job for a reason of the host, which the add-on records, and audits the kill and the end", func() {
		_, err := jobs.End(ctx, scope, "job-abcdef", bgjob.EndGroupOff)
		Expect(err).NotTo(HaveOccurred())
		last := caller.calls[len(caller.calls)-1]
		Expect(last).To(SatisfyAll(HaveField("Verb", contract.JobKill), HaveField("Signal", "term"), HaveField("Reason", "group_off")))
		_, err = jobs.Kill(ctx, scope, "job-abcdef", "int")
		Expect(err).NotTo(HaveOccurred())
		Expect(caller.calls[len(caller.calls)-1].Reason).To(BeEmpty(), "a kill of the model gives no reason: the end is killed")
		jobs.Ended(ctx, scope, bgjob.Info{ID: "job-abcdef", End: bgjob.EndGroupOff, Exit: 143})
		Expect(events).To(HaveLen(3))
		Expect(events[0]).To(SatisfyAll(HaveField("Kind", "job_kill"), HaveField("Text", "group_off")))
		Expect(events[2]).To(SatisfyAll(HaveField("Kind", "job_end"), HaveField("Job", "job-abcdef"), HaveField("Text", "group_off, exit 143")))
	})

	It("caps the audit of an input at 4 KiB and refuses an input over 2000 bytes before sending it", func() {
		_, _, err := jobs.Input(ctx, scope, "job-abcdef", strings.Repeat("s", contract.MaxJobInputBytes+1), false)
		text, ok := studio.SandboxJobText(err)
		Expect(ok).To(BeTrue())
		Expect(text).To(Equal("Not written: input is at most 2000 bytes per call."))
		Expect(caller.calls).To(BeEmpty())
		Expect(events).To(BeEmpty())
		n, pending, err := jobs.Input(ctx, scope, "job-abcdef", "secret\n", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(7))
		Expect(pending).To(BeFalse())
		Expect(events).To(ConsistOf(SatisfyAll(HaveField("Kind", "job_input"), HaveField("Job", "job-abcdef"), HaveField("Text", "secret\n"))))
		long, _ := jobs.Start(ctx, scope, strings.Repeat("c", 10<<10), "")
		_ = long
		Expect(len(events[1].Text)).To(BeNumerically("<=", studio.JobAuditMax+len(" [cut]")))
	})

	It("never sends an ID that is not a job ID, and reports it unknown", func() {
		for _, id := range []string{"../../etc/passwd", "job-ABCDEF", "job-abcdef/../x", ""} {
			_, err := jobs.Read(ctx, scope, id, nil, 0)
			Expect(err).To(HaveOccurred(), id)
			if id != "" {
				Expect(errors.Is(err, bgjob.ErrUnknown)).To(BeTrue(), id)
			}
		}
		Expect(caller.calls).To(BeEmpty())
	})

	It("passes the add-on's refusal to the model, and an unknown job is bgjob.ErrUnknown so the stub becomes lost", func() {
		caller.answer = func(contract.JobArgs) (contract.JobReply, error) {
			return contract.JobReply{Code: contract.JobCodeUnknown, Error: `No job "job-abcdef" in this chat.`}, nil
		}
		_, err := jobs.Kill(ctx, scope, "job-abcdef", "")
		Expect(errors.Is(err, bgjob.ErrUnknown)).To(BeTrue())
		text, ok := studio.SandboxJobText(err)
		Expect(ok).To(BeTrue())
		Expect(text).To(ContainSubstring("No job"))
		caller.answer = func(contract.JobArgs) (contract.JobReply, error) {
			return contract.JobReply{Code: contract.JobCodeEnded, Error: "job-abcdef has ended (exited with status 0)."}, nil
		}
		_, err = jobs.Kill(ctx, scope, "job-abcdef", "term")
		Expect(errors.Is(err, bgjob.ErrUnknown)).To(BeFalse())
	})

	It("says an old add-on cannot run jobs, and never falls back", func() {
		caller.answer = func(contract.JobArgs) (contract.JobReply, error) { return contract.JobReply{}, studio.ErrAddonNoJobs }
		_, err := jobs.Start(ctx, scope, "make", "")
		text, ok := studio.SandboxJobText(err)
		Expect(ok).To(BeTrue())
		Expect(text).To(Equal(studio.SandboxJobsTooOld))
		_, err = studio.NewSandboxJobs(nil, nil).List(ctx, scope)
		Expect(err).To(MatchError(studio.ErrAddonNoJobs))
	})

	It("observes a job for a wait check without moving the model's cursor, and only for its own owner and chat", func() {
		obs := jobs.Observer("sandbox")
		o, err := obs.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: "job-abcdef", Since: 100})
		Expect(err).NotTo(HaveOccurred())
		Expect(o.Output).To(Equal([]byte("hello")))
		Expect(o.From).To(Equal(int64(100)))
		Expect(o.Next).To(Equal(int64(105)))
		Expect(o.Idle).To(Equal(1500 * time.Millisecond))
		Expect(len(o.Tail)).To(Equal(bgjob.DefaultTailBytes))
		last := caller.calls[len(caller.calls)-1]
		Expect(last.Observe).To(BeTrue())
		Expect(*last.Since).To(Equal(int64(100)))
		Expect(last.MaxBytes).To(Equal(bgjob.DefaultObserveBytes))
		list, err := obs.List(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		_, err = jobs.Observe(ctx, scope, bgjob.ObserveRequest{Owner: "bob", Chat: "c1", ID: "job-abcdef"})
		Expect(err).To(MatchError(bgjob.ErrUnknown))
	})

	It("reads for the model from its own cursor by default", func() {
		o, err := jobs.Read(ctx, scope, "job-abcdef", nil, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(o.Output)).To(Equal("hello"))
		last := caller.calls[len(caller.calls)-1]
		Expect(last.Since).To(BeNil())
		Expect(last.Observe).To(BeFalse())
	})
})
