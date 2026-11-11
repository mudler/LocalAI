//go:build linux || darwin

package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// jobProbe is a job that sleeps in the sandbox and records how it was ended.
type jobProbe struct {
	release func()
	mu      sync.Mutex
	reasons []bgjob.EndReason
}

func (p *jobProbe) ended() []bgjob.EndReason {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bgjob.EndReason(nil), p.reasons...)
}

// jobRig is a manager over the fake backend with a clock the test moves and a
// chat per name, all of alice.
type jobRig struct {
	ctx  context.Context
	st   workspace.Store
	m    *runner.Manager
	mu   sync.Mutex
	now  time.Time
	mode workspace.Mode
	n    int
}

func newJobRig(perUser int) *jobRig { return newJobRigTotal(perUser, 0) }

func newJobRigTotal(perUser, total int) *jobRig {
	r := &jobRig{ctx: context.Background(), st: sbtest.OpenStore(), now: time.Now(), mode: workspace.ModeWrite}
	_, err := r.st.Put(r.ctx, admin, "/users/alice/in.txt", strings.NewReader("inside\n"), workspace.PutOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(r.st.Grant(r.ctx, admin, "alice", "/users/alice", workspace.ModeWrite)).To(Succeed())
	backend := sbtest.NewFakeBackend(GinkgoT().TempDir())
	m, err := runner.New(runner.Options{
		Backend: func() (sandbox.Backend, error) { return backend, nil },
		Store:   r.st,
		Actor: func(_ context.Context, user, conv string) (workspace.Actor, error) {
			return workspace.Actor{Principal: user, ChatID: conv}, nil
		},
		View: func(ctx context.Context, a workspace.Actor) (workspace.View, error) {
			r.mu.Lock()
			mode := r.mode
			r.mu.Unlock()
			return r.st.View(ctx, a, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: mode}})
		},
		PerUser:   perUser,
		Total:     total,
		IdleAfter: time.Minute,
		MaxAge:    24 * time.Hour,
		Now:       r.clock,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = m.Close(context.Background()) })
	r.m = m
	return r
}

func (r *jobRig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *jobRig) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
}

func (r *jobRig) request(conv string) runner.Request {
	r.mu.Lock()
	r.n++
	n := r.n
	r.mu.Unlock()
	return runner.Request{User: "alice", Conversation: conv, Operation: fmt.Sprintf("job-op%d", n)}
}

func (r *jobRig) bash(conv, cmd string) (string, error) {
	req := r.request(conv)
	req.Tool = "bash"
	req.Args, _ = json.Marshal(map[string]any{"command": cmd})
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	return r.m.Run(ctx, req)
}

func (r *jobRig) mustBash(conv, cmd string) string {
	GinkgoHelper()
	out, err := r.bash(conv, cmd)
	Expect(err).NotTo(HaveOccurred(), cmd)
	return out
}

// job starts a process that sleeps until it is ended, in the sandbox of conv.
func (r *jobRig) job(conv string) *jobProbe {
	GinkgoHelper()
	p := &jobProbe{}
	release, err := r.m.StartJob(r.ctx, r.request(conv), func(sb sandbox.Sandbox) (bgjob.EndFunc, error) {
		jctx, cancel := context.WithCancel(context.Background())
		proc, err := sb.Exec(jctx, sandbox.Cmd{Args: []string{"sleep", "60"}})
		if err != nil {
			cancel()
			return nil, err
		}
		go func() { _, _ = proc.Wait() }()
		return func(reason bgjob.EndReason) {
			p.mu.Lock()
			p.reasons = append(p.reasons, reason)
			p.mu.Unlock()
			cancel()
		}, nil
	})
	Expect(err).NotTo(HaveOccurred())
	p.release = release
	return p
}

var _ = Describe("background jobs on a sandbox entry", func() {
	It("counts a job and stops counting it on release, also when released twice", func() {
		r := newJobRig(4)
		j := r.job("c1")
		Expect(r.m.LiveJobs("alice", "c1")).To(Equal(1))
		j.release()
		j.release()
		Expect(r.m.LiveJobs("alice", "c1")).To(BeZero())
		Expect(j.ended()).To(BeEmpty(), "a released job is not ended again")
	})

	It("refuses a job without a call identity or a way to end it", func() {
		r := newJobRig(4)
		_, err := r.m.StartJob(r.ctx, runner.Request{User: "alice"}, nil)
		Expect(err).To(MatchError(runner.ErrInvalid))
		_, err = r.m.StartJob(r.ctx, r.request("c1"), func(sandbox.Sandbox) (bgjob.EndFunc, error) { return nil, nil })
		Expect(err).To(HaveOccurred())
		Expect(r.m.LiveJobs("alice", "c1")).To(BeZero())
	})

	Describe("eviction", func() {
		It("gives way to the oldest idle sandbox of the user, not to the one with a job (fifth chat)", func() {
			r := newJobRig(4)
			j := r.job("c1") // the oldest entry
			for _, c := range []string{"c2", "c3", "c4"} {
				r.advance(time.Second)
				r.mustBash(c, "true")
			}
			r.advance(time.Second)
			r.mustBash("c5", "true")
			Expect(j.ended()).To(BeEmpty())
			Expect(r.m.LiveJobs("alice", "c1")).To(Equal(1))
			Expect(r.m.Count("alice")).To(Equal(4))
			Expect(r.mustBash("c1", "echo still")).To(ContainSubstring("still"))
		})

		It("refuses a new chat when every sandbox of the user has a job", func() {
			r := newJobRig(2)
			a, b := r.job("c1"), r.job("c2")
			_, err := r.bash("c3", "true")
			Expect(err).To(MatchError(runner.ErrBusy))
			Expect(a.ended()).To(BeEmpty())
			Expect(b.ended()).To(BeEmpty())
			Expect(r.m.Count("alice")).To(Equal(2))
		})
	})

	Describe("the server total", func() {
		It("refuses with its own error when every sandbox of the server has a job", func() {
			r := newJobRigTotal(4, 2)
			a, b := r.job("c1"), r.job("c2")
			_, err := r.bash("c3", "true")
			Expect(err).To(MatchError(runner.ErrBusyTotal))
			Expect(errors.Is(err, runner.ErrBusy)).To(BeTrue(), "a caller that ignores the difference still works")
			Expect(a.ended()).To(BeEmpty())
			Expect(b.ended()).To(BeEmpty())
		})
	})

	Describe("the sweep", func() {
		It("neither suspends nor expires an entry with a job, and does both once it ends", func() {
			r := newJobRig(4)
			j := r.job("c1")
			r.advance(25 * time.Hour) // idle and past MaxAge
			r.m.Sweep(r.ctx)
			Expect(r.m.Count("alice")).To(Equal(1))
			Expect(r.m.Running()).To(Equal(1), "the sandbox stays awake")
			Expect(j.ended()).To(BeEmpty())
			j.release()
			r.m.Sweep(r.ctx)
			Expect(r.m.Count("alice")).To(BeZero(), "the lifetime bound applies once the job is over")
		})

		It("suspends a sandbox without a job that is idle", func() {
			r := newJobRig(4)
			r.mustBash("c1", "true")
			r.advance(2 * time.Minute)
			r.m.Sweep(r.ctx)
			Expect(r.m.Running()).To(BeZero())
			Expect(r.m.Count("alice")).To(Equal(1))
		})

		It("starts the idle clock when the last job ends", func() {
			r := newJobRig(4)
			j := r.job("c1")
			r.advance(time.Hour)
			j.release()
			r.m.Sweep(r.ctx)
			Expect(r.m.Running()).To(Equal(1), "it was in use a moment ago")
			r.advance(2 * time.Minute)
			r.m.Sweep(r.ctx)
			Expect(r.m.Running()).To(BeZero())
		})
	})

	Describe("a call that finds the files changed elsewhere", func() {
		change := func(r *jobRig, body string) {
			GinkgoHelper()
			_, err := r.st.Put(r.ctx, admin, "/users/alice/in.txt", strings.NewReader(body), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
		}

		It("refreshes the sandbox when no job runs", func() {
			r := newJobRig(4)
			r.mustBash("c1", "true")
			change(r, "newer\n")
			out := r.mustBash("c1", "cat in.txt")
			Expect(out).To(ContainSubstring("newer"))
			Expect(out).NotTo(ContainSubstring(runner.StaleNote))
		})

		It("keeps the sandbox as it is and says so at the end of the output while a job runs", func() {
			r := newJobRig(4)
			j := r.job("c1")
			change(r, "newer\n")
			out := r.mustBash("c1", "cat in.txt")
			Expect(out).To(ContainSubstring("inside"))
			Expect(out).NotTo(ContainSubstring("newer"))
			Expect(strings.TrimSpace(out)).To(HaveSuffix(runner.StaleNote))
			Expect(j.ended()).To(BeEmpty())
			j.release()
			out = r.mustBash("c1", "cat in.txt")
			Expect(out).To(ContainSubstring("newer"), "the next call refreshes")
			Expect(out).NotTo(ContainSubstring(runner.StaleNote))
		})
	})

	It("ends the jobs when a changed access makes the sandbox again, and says so at the top of the output", func() {
		r := newJobRig(4)
		j := r.job("c1")
		r.mu.Lock()
		r.mode = workspace.ModeRead
		r.mu.Unlock()
		out := r.mustBash("c1", "echo after")
		Expect(strings.HasPrefix(out, runner.RemadeNote)).To(BeTrue(), out)
		Expect(out).To(ContainSubstring("after"))
		Expect(j.ended()).To(Equal([]bgjob.EndReason{bgjob.EndSandboxRemade}))
		Expect(r.m.LiveJobs("alice", "c1")).To(BeZero())
		j.release()
	})

	for name, stop := range map[string]func(*runner.Manager){
		"Close": func(m *runner.Manager) { Expect(m.Close(context.Background())).To(Succeed()) },
		"Reset": func(m *runner.Manager) { m.Reset(context.Background()) },
	} {
		It("kills the jobs with the reason shutdown on "+name, func() {
			r := newJobRig(4)
			a, b := r.job("c1"), r.job("c2")
			stop(r.m)
			Expect(a.ended()).To(Equal([]bgjob.EndReason{bgjob.EndShutdown}))
			Expect(b.ended()).To(Equal([]bgjob.EndReason{bgjob.EndShutdown}))
			Expect(r.m.LiveJobs("alice", "c1")).To(BeZero())
			Expect(r.m.Count("")).To(BeZero())
		})
	}

	It("does not count a job whose launch failed", func() {
		r := newJobRig(4)
		_, err := r.m.StartJob(r.ctx, r.request("c1"), func(sandbox.Sandbox) (bgjob.EndFunc, error) {
			return nil, errors.New("no such program")
		})
		Expect(err).To(MatchError(ContainSubstring("no such program")))
		Expect(r.m.LiveJobs("alice", "c1")).To(BeZero())
	})
})
