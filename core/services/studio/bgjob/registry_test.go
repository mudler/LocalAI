package bgjob_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

// fakeProc is a process that a test ends by hand. A signal ends it when
// dies is set for that signal.
type fakeProc struct {
	mu      sync.Mutex
	signals []syscall.Signal
	done    chan struct{}
	once    sync.Once
	code    int
	err     error
	dies    map[syscall.Signal]bool
}

func newProc(dies ...syscall.Signal) *fakeProc {
	p := &fakeProc{done: make(chan struct{}), dies: map[syscall.Signal]bool{}}
	for _, s := range dies {
		p.dies[s] = true
	}
	return p
}

func (p *fakeProc) Wait() (int, error) { <-p.done; return p.code, p.err }

func (p *fakeProc) Signal(s syscall.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, s)
	dies := p.dies[s]
	p.mu.Unlock()
	if dies {
		p.finish(128+int(s), nil)
	}
	return nil
}

func (p *fakeProc) finish(code int, err error) {
	p.once.Do(func() { p.code, p.err = code, err; close(p.done) })
}

func (p *fakeProc) sent() []syscall.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]syscall.Signal(nil), p.signals...)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var _ = Describe("Registry", func() {
	var (
		clk     *clock
		reg     *bgjob.Registry
		changes chan bgjob.Info
		grace   chan time.Time
		ctx     = context.Background()
	)
	BeforeEach(func() {
		clk = &clock{t: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
		// The closures hold their own channels: a goroutine of an earlier spec
		// may still run when the next one starts.
		ch, gr := make(chan bgjob.Info, 64), make(chan time.Time)
		changes, grace = ch, gr
		reg = bgjob.New(bgjob.Options{Instance: "n1", Now: clk.Now, After: func(time.Duration) <-chan time.Time { return gr },
			Changed: func(i bgjob.Info) {
				select {
				case ch <- i:
				default:
				}
			}})
	})
	start := func(owner, chat string, p *fakeProc) *bgjob.Job {
		j, err := reg.Reserve(bgjob.Spec{Owner: owner, Chat: chat, Label: "build", Command: "make"})
		Expect(err).NotTo(HaveOccurred())
		j.Run(p, func() {}, nil)
		return j
	}
	finished := func(j *bgjob.Job) bgjob.Info {
		Eventually(func() bool { return j.Info().State.Finished() }).Should(BeTrue())
		return j.Info()
	}

	It("gives an id, runs, records output with absolute offsets and exits", func() {
		p := newProc()
		j := start("alice", "c1", p)
		Expect(j.ID()).To(MatchRegexp(`^job-[a-z2-7]{6}$`))
		Expect(j.Info().State).To(Equal(bgjob.StateRunning))
		_, _ = j.Output().Write([]byte("line one\n"))
		clk.Add(5 * time.Second)
		o, err := reg.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: j.ID()})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(o.Output)).To(Equal("line one\n"))
		Expect(o.Idle).To(Equal(5 * time.Second))
		Expect(o.Next).To(Equal(int64(9)))
		p.finish(3, nil)
		in := finished(j)
		Expect(in.State).To(Equal(bgjob.StateExited))
		Expect(in.End).To(Equal(bgjob.EndExit))
		Expect(in.Exit).To(Equal(3))
		Expect(in.Instance).To(Equal("n1"))
		o, _ = reg.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: j.ID()})
		Expect(o.Idle).To(BeZero())
	})

	It("moves the read place of the chat and lets a parent read without moving it", func() {
		j, _ := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "sub", Parent: "c1", Label: "x"})
		j.Run(newProc(), func() {}, nil)
		_, _ = j.Output().Write([]byte("0123456789"))
		o, _ := reg.Read(ctx, bgjob.ReadRequest{Owner: "alice", Chat: "sub", ID: j.ID(), MaxBytes: 4})
		Expect(string(o.Output)).To(Equal("0123"))
		o, _ = reg.Read(ctx, bgjob.ReadRequest{Owner: "alice", Chat: "c1", ID: j.ID()})
		Expect(string(o.Output)).To(Equal("456789"), "the parent starts where the sub-agent's read ended, and leaves it")
		o, _ = reg.Read(ctx, bgjob.ReadRequest{Owner: "alice", Chat: "sub", ID: j.ID()})
		Expect(string(o.Output)).To(Equal("456789"))
		since := int64(2)
		o, _ = reg.Read(ctx, bgjob.ReadRequest{Owner: "alice", Chat: "sub", ID: j.ID(), Since: &since})
		Expect(o.From).To(Equal(int64(2)))
	})

	It("knows a job only to its owner and its chat, or the parent chat", func() {
		j := start("alice", "c1", newProc())
		for _, q := range []bgjob.ObserveRequest{{Owner: "bob", Chat: "c1", ID: j.ID()}, {Owner: "alice", Chat: "c2", ID: j.ID()}, {Owner: "alice", Chat: "c1", ID: "job-zzzzzz"}} {
			_, err := reg.Observe(ctx, q)
			Expect(err).To(MatchError(bgjob.ErrUnknown))
		}
		l, _ := reg.List(ctx, "bob", "c1")
		Expect(l).To(BeEmpty())
	})

	Describe("limits", func() {
		It("refuses the fifth job of a chat, the ninth of a user and the thirty-third of a node", func() {
			for range 4 {
				start("alice", "c1", newProc())
			}
			_, err := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
			var le *bgjob.LimitError
			Expect(errors.As(err, &le)).To(BeTrue())
			Expect(le.Scope).To(Equal(bgjob.ScopeChat))
			for range 4 {
				start("alice", "c2", newProc())
			}
			_, err = reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "c3"})
			Expect(errors.As(err, &le)).To(BeTrue())
			Expect(le.Scope).To(Equal(bgjob.ScopeUser))
			for u := range 6 {
				for range 4 {
					start(string(rune('b'+u)), "c", newProc())
				}
			}
			_, err = reg.Reserve(bgjob.Spec{Owner: "zed", Chat: "c"})
			Expect(errors.As(err, &le)).To(BeTrue())
			Expect(le.Scope).To(Equal(bgjob.ScopeNode))
			Expect(le.Error()).To(Equal("this server runs 32 jobs, the most it may"))
		})

		It("does not count a finished job or one that failed to start", func() {
			for range 3 {
				start("alice", "c1", newProc())
			}
			p := newProc()
			j := start("alice", "c1", p)
			p.finish(0, nil)
			finished(j)
			f, _ := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
			f.Fail()
			Expect(f.Info().State).To(Equal(bgjob.StateFailed))
			Expect(f.Info().End).To(Equal(bgjob.EndStartFailed))
			_, err := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ending", func() {
		It("kills with term first and with kill after the grace", func() {
			p := newProc(syscall.SIGKILL)
			j := start("alice", "c1", p)
			Expect(reg.Kill("alice", "c1", j.ID(), bgjob.EndKilled, syscall.SIGTERM)).Error().NotTo(HaveOccurred())
			Expect(j.Info().State).To(Equal(bgjob.StateEnding))
			Consistently(func() []syscall.Signal { return p.sent() }, "100ms").Should(Equal([]syscall.Signal{syscall.SIGTERM}))
			grace <- time.Now()
			in := finished(j)
			Expect(p.sent()).To(Equal([]syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}))
			Expect(in.State).To(Equal(bgjob.StateKilled))
			Expect(in.End).To(Equal(bgjob.EndKilled))
			Expect(in.Exit).To(Equal(128 + 9))
		})

		It("does not send the kill when the process ended after term", func() {
			p := newProc(syscall.SIGTERM)
			j := start("alice", "c1", p)
			_, err := reg.Kill("alice", "c1", j.ID(), bgjob.EndStopped, syscall.SIGTERM)
			Expect(err).NotTo(HaveOccurred())
			in := finished(j)
			Expect(in.End).To(Equal(bgjob.EndStopped))
			Expect(p.sent()).To(Equal([]syscall.Signal{syscall.SIGTERM}))
			_, err = reg.Kill("alice", "c1", j.ID(), bgjob.EndKilled, syscall.SIGTERM)
			var ended *bgjob.EndedError
			Expect(errors.As(err, &ended)).To(BeTrue())
		})

		It("ends an end condition as exited with its reason, and kills at once on shutdown", func() {
			p := newProc(syscall.SIGTERM)
			j := start("alice", "c1", p)
			Expect(reg.EndChat("alice", "c1", bgjob.EndChatDeleted)).To(Equal(1))
			in := finished(j)
			Expect(in.State).To(Equal(bgjob.StateExited))
			Expect(in.End).To(Equal(bgjob.EndChatDeleted))

			q := newProc(syscall.SIGKILL)
			k := start("alice", "c1", q)
			k.End(bgjob.EndShutdown)
			finished(k)
			Expect(q.sent()).To(Equal([]syscall.Signal{syscall.SIGKILL}))
		})

		It("ends the jobs of a sub-agent with the chat that started them", func() {
			j, _ := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "sub", Parent: "c1"})
			p := newProc(syscall.SIGTERM)
			j.Run(p, func() {}, nil)
			Expect(reg.EndChat("alice", "c1", bgjob.EndAgentEnded)).To(Equal(1))
			Expect(finished(j).End).To(Equal(bgjob.EndAgentEnded))
		})

		It("runs onEnd before the job reads as finished, with the final state", func() {
			p := newProc()
			j, _ := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
			seen := make(chan bgjob.Info, 1)
			release := make(chan struct{})
			j.Run(p, func() {}, func(in bgjob.Info) {
				seen <- in
				<-release
			})
			p.finish(0, nil)
			in := <-seen
			Expect(in.State).To(Equal(bgjob.StateExited))
			Expect(j.Info().State.Finished()).To(BeFalse(), "the files are saved first")
			close(release)
			finished(j)
		})

		It("reads a lifetime deadline as the end reason lifetime", func() {
			p := newProc()
			j := start("alice", "c1", p)
			p.finish(0, context.DeadlineExceeded)
			in := finished(j)
			Expect(in.End).To(Equal(bgjob.EndLifetime))
			Expect(in.Exit).To(Equal(bgjob.ExitUnknown))
		})
	})

	Describe("input", func() {
		It("writes to the standard input, refuses a parent, and closes it", func() {
			j, _ := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "sub", Parent: "c1"})
			j.Run(newProc(), func() {}, nil)
			got := make(chan string, 1)
			go func() {
				buf := make([]byte, 64)
				n, _ := j.Stdin().Read(buf)
				got <- string(buf[:n])
			}()
			n, err := reg.Input("alice", "sub", j.ID(), "yes\n", false)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(4))
			Eventually(got).Should(Receive(Equal("yes\n")))
			_, err = reg.Input("alice", "c1", j.ID(), "no\n", false)
			Expect(err).To(MatchError(bgjob.ErrForbidden))
		})

		It("gives up on a child that does not read", func() {
			reg2 := bgjob.New(bgjob.Options{InputTimeout: 100 * time.Millisecond})
			k, _ := reg2.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
			k.Run(newProc(), func() {}, nil)
			// More than a pipe holds, and nobody reads it.
			_, err := k.Input(strings.Repeat("x", 1<<20), false)
			Expect(err).To(MatchError(bgjob.ErrInputTimeout))
		})

		It("says the input is closed after close_input, also over the carrier", func() {
			j := start("alice", "c1", newProc())
			_, err := reg.Input("alice", "c1", j.ID(), "last\n", true)
			Expect(err).NotTo(HaveOccurred())
			_, err = reg.Input("alice", "c1", j.ID(), "more\n", false)
			Expect(err).To(MatchError(bgjob.ErrInputClosed))
			rep := reg.Handle(ctx, bgjob.Request{Action: bgjob.ActionInput, Owner: "alice", Chat: "c1", ID: j.ID(), Input: "x"})
			Expect(rep.Err()).To(MatchError(bgjob.ErrInputClosed))
		})

		It("says a finished job has ended", func() {
			p := newProc()
			j := start("alice", "c1", p)
			p.finish(0, nil)
			finished(j)
			_, err := reg.Input("alice", "c1", j.ID(), "x", false)
			var ended *bgjob.EndedError
			Expect(errors.As(err, &ended)).To(BeTrue())
			Expect(ended.Info.Exit).To(Equal(0))
		})
	})

	Describe("a request without an owner or a chat", func() {
		It("matches no top-level job and ends none", func() {
			j := start("alice", "c1", newProc())
			sub, _ := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "sub", Parent: "c1"})
			sub.Run(newProc(), func() {}, nil)
			for _, who := range [][2]string{{"alice", ""}, {"", "c1"}, {"", ""}} {
				_, err := reg.Observe(ctx, bgjob.ObserveRequest{Owner: who[0], Chat: who[1], ID: j.ID()})
				Expect(err).To(MatchError(bgjob.ErrUnknown), "%q", who)
				l, _ := reg.List(ctx, who[0], who[1])
				Expect(l).To(BeEmpty(), "%q", who)
				Expect(reg.EndChat(who[0], who[1], bgjob.EndChatDeleted)).To(BeZero(), "%q", who)
				_, err = reg.Kill(who[0], who[1], j.ID(), bgjob.EndKilled, syscall.SIGTERM)
				Expect(err).To(MatchError(bgjob.ErrUnknown), "%q", who)
				rep := reg.Handle(ctx, bgjob.Request{Action: bgjob.ActionList, Owner: who[0], Chat: who[1]})
				Expect(rep.List).To(BeEmpty(), "%q", who)
			}
			Expect(j.Info().State).To(Equal(bgjob.StateRunning))
			// A top-level job has no parent: the parent match needs one.
			l, _ := reg.List(ctx, "alice", "c1")
			Expect(l).To(HaveLen(2))
		})
	})

	Describe("the global cap of finished jobs", func() {
		It("drops the oldest finished jobs beyond the total, whatever their chats", func() {
			var all []*bgjob.Job
			for i := range bgjob.KeepFinishedTotal + 10 {
				p := newProc()
				j := start("alice", "chat"+string(rune('A'+i%26))+string(rune('a'+i/26)), p)
				p.finish(0, nil)
				finished(j)
				clk.Add(time.Second)
				all = append(all, j)
			}
			reg.Sweep(ctx)
			var left int
			for _, j := range all {
				if _, err := reg.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: j.Info().Chat, ID: j.ID()}); err == nil {
					left++
				}
			}
			Expect(left).To(Equal(bgjob.KeepFinishedTotal))
			_, err := reg.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: all[0].Info().Chat, ID: all[0].ID()})
			Expect(err).To(MatchError(bgjob.ErrUnknown), "the oldest went first")
		})
	})

	Describe("a start that never began", func() {
		It("is dropped without a state change", func() {
			j, err := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
			Expect(err).NotTo(HaveOccurred())
			j.Abandon()
			Expect(changes).To(BeEmpty())
			l, _ := reg.List(ctx, "alice", "c1")
			Expect(l).To(BeEmpty())
			chatN, _, _ := reg.Counts("alice", "c1")
			Expect(chatN).To(BeZero())
		})
	})

	Describe("sweep", func() {
		It("ends a job at its lifetime and drops finished jobs after the retention", func() {
			p := newProc(syscall.SIGTERM)
			j := start("alice", "c1", p)
			clk.Add(time.Hour)
			reg.Sweep(ctx)
			Expect(j.Info().State).To(Equal(bgjob.StateRunning))
			clk.Add(time.Hour)
			reg.Sweep(ctx)
			Expect(finished(j).End).To(Equal(bgjob.EndLifetime))
			clk.Add(29 * time.Minute)
			reg.Sweep(ctx)
			l, _ := reg.List(ctx, "alice", "c1")
			Expect(l).To(HaveLen(1))
			clk.Add(2 * time.Minute)
			reg.Sweep(ctx)
			l, _ = reg.List(ctx, "alice", "c1")
			Expect(l).To(BeEmpty())
		})

		It("keeps at most 16 finished jobs per chat", func() {
			for range 20 {
				p := newProc()
				j := start("alice", "c1", p)
				p.finish(0, nil)
				finished(j)
				clk.Add(time.Second)
			}
			reg.Sweep(ctx)
			l, _ := reg.List(ctx, "alice", "c1")
			Expect(l).To(HaveLen(16))
		})

		It("asks the check and ends a job for its answer", func() {
			p := newProc(syscall.SIGTERM)
			reg3 := bgjob.New(bgjob.Options{Check: func(_ context.Context, in bgjob.Info) bgjob.EndReason { return bgjob.EndGroupOff }})
			j, _ := reg3.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
			j.Run(p, func() {}, nil)
			reg3.Sweep(ctx)
			Expect(finished(j).End).To(Equal(bgjob.EndGroupOff))
		})

		It("lists running jobs first, then the newest finished", func() {
			p := newProc()
			a := start("alice", "c1", p)
			p.finish(0, nil)
			finished(a)
			clk.Add(time.Minute)
			b := start("alice", "c1", newProc())
			l, _ := reg.List(ctx, "alice", "c1")
			Expect([]string{l[0].ID, l[1].ID}).To(Equal([]string{b.ID(), a.ID()}))
		})
	})

	It("reports the start and the end through Changed, and never the command", func() {
		p := newProc()
		j := start("alice", "c1", p)
		Eventually(changes).Should(Receive(HaveField("State", bgjob.StateRunning)))
		p.finish(0, nil)
		Eventually(changes).Should(Receive(HaveField("State", bgjob.StateExited)))
		Expect(j.Command()).To(Equal("make"))
	})

	It("ends every job and waits on Close, and refuses a new one", func() {
		p := newProc(syscall.SIGKILL)
		j := start("alice", "c1", p)
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		reg.Close(cctx)
		Expect(j.Info().End).To(Equal(bgjob.EndShutdown))
		_, err := reg.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
		Expect(err).To(HaveOccurred())
	})
})
