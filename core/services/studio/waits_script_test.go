package studio_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// scriptHostExec is the built-in bash without a machine, with the read-only
// call that a wait on a script uses. It answers a wrapped script the way bash
// would: the text, then the status line that the wrapper prints.
type scriptHostExec struct {
	fakeHostExec
	mu2 sync.Mutex
	// behave returns what the script printed and its exit status; text, when
	// it is set, is returned as it is (a shell that gave no status line).
	behave   func(script string) (out string, code int)
	text     string
	scripts  []string
	readOnly int
}

var (
	wrappedTag    = regexp.MustCompile(`'(LOCALAI_WAIT_STATUS_[0-9a-f]+=)'`)
	wrappedScript = regexp.MustCompile(`(?s)^\(\n(.*)\n\) 2>&1 \|`)
)

func (f *scriptHostExec) RunReadOnly(_ context.Context, _ workspace.Actor, raw json.RawMessage) (string, error) {
	defer GinkgoRecover() // a check runs in its own goroutine
	var in struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	Expect(json.Unmarshal(raw, &in)).To(Succeed())
	Expect(in.TimeoutSeconds).To(Equal(120), "a check has the 120 s timeout of bash")
	tag := wrappedTag.FindStringSubmatch(in.Command)
	script := wrappedScript.FindStringSubmatch(in.Command)
	Expect(tag).NotTo(BeNil(), "the command is not wrapped: %s", in.Command)
	Expect(script).NotTo(BeNil(), "the command is not wrapped: %s", in.Command)
	f.mu2.Lock()
	defer f.mu2.Unlock()
	f.readOnly++
	f.scripts = append(f.scripts, script[1])
	if f.text != "" {
		return f.text, nil
	}
	out, code := f.behave(script[1])
	return out + "\n" + tag[1] + itoa(code) + "\n", nil
}

func itoa(n int) string { raw, _ := json.Marshal(n); return string(raw) }

func (f *scriptHostExec) set(behave func(string) (string, int)) {
	f.mu2.Lock()
	defer f.mu2.Unlock()
	f.behave, f.text = behave, ""
}

func (f *scriptHostExec) calls() (int, []string) {
	f.mu2.Lock()
	defer f.mu2.Unlock()
	return f.readOnly, append([]string(nil), f.scripts...)
}

// exits is a script that prints out and exits with the status code.
func exits(out string, code *int) func(string) (string, int) {
	return func(string) (string, int) { return out, *code }
}

// sandboxAddons is the Sandbox add-on that serves bash: it records the options
// the host put on each call and answers a wrapped script like bash.
type sandboxAddons struct {
	*fakeAddons
	mu2      sync.Mutex
	readOnly []bool
	exit     int
}

func (s *sandboxAddons) Call(ctx context.Context, user, conv, name string, raw json.RawMessage) (string, error) {
	defer GinkgoRecover()
	_, _ = s.fakeAddons.Call(ctx, user, conv, name, raw)
	var in struct{ Command string }
	Expect(json.Unmarshal(raw, &in)).To(Succeed())
	call, _ := contract.ExecutionCallFrom(ctx)
	s.mu2.Lock()
	defer s.mu2.Unlock()
	s.readOnly = append(s.readOnly, call.ReadOnly)
	return "from the sandbox\n" + wrappedTag.FindStringSubmatch(in.Command)[1] + itoa(s.exit) + "\n", nil
}

const secretCommand = "curl -fsS https://internal.example/health | grep -q ok"

var _ = Describe("wait_for on a script", func() {
	ctx := context.Background()
	const wait = `{"script":"` + secretCommand + `","until":"exit_zero","every":"1m","timeout":"10m",` + waitThen + `,"reason":"the service"}`

	type rigOpts struct {
		mode   string
		limits func() studio.LoopLimits
		admin  func(string) bool
	}
	build := func(args string, o rigOpts, fx *scriptHostExec) (*loopRig, string) {
		if o.mode == "" {
			o.mode = "on"
		}
		mode := o.mode
		var opts studio.LoopOptions
		opts.Limits = o.limits
		r := newLoopRig(armScript(args), opts, func(d *studio.Deps) {
			d.HostExec = fx
			d.HostExecMode = func() string { return mode }
			d.IsAdmin = func(_ context.Context, user string) bool { return user == alice }
		})
		return r, create(r.svc, alice)
	}
	arm := func(r *loopRig, chat string) loops.Loop {
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)
		list, err := r.host.List(ctx, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1), "%v", r.h.ResultsFor("wait_for"))
		return list[0]
	}
	tick := func(r *loopRig, d time.Duration) {
		r.sh.clock.Advance(d)
		r.pass()
	}

	It("runs the command read-only for each check, never as a call of bash, and wakes when it exits 0", func() {
		status := 1
		fx := &scriptHostExec{}
		fx.set(exits("starting", &status))
		r, chat := build(wait, rigOpts{}, fx)
		l := arm(r, chat)
		Expect(r.h.ResultsFor("wait_for")[0].Err).To(BeNil())
		Expect(l.Watch.Script).To(Equal(secretCommand))
		Expect(l.Watch.Mode).To(Equal(loops.UntilExitZero))

		r.pass()
		for range 2 {
			tick(r, time.Minute)
		}
		got := r.get(l.ID)
		Expect(got.Watch.Checks).To(Equal(3))
		Expect(got.Watch.Errors).To(BeZero(), "a non-zero exit is a value, not a failed check")
		Expect(got.Watch.LastExit).To(Equal(1))
		Expect(r.h.Sent(chat)).To(HaveLen(1), "a check called the model")
		n, scripts := fx.calls()
		Expect(n).To(Equal(3))
		Expect(scripts).To(HaveEach(secretCommand))
		Expect(fx.commands()).To(BeEmpty(), "a check ran as a normal call of bash, which commits its files")

		status = 0
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		woke := r.h.Sent(chat)
		Expect(woke).To(HaveLen(2))
		Expect(woke[1]).To(ContainSubstring("the condition was met (exited-zero)"))
		Expect(woke[1]).To(ContainSubstring(waitThen[len(`"then":"`) : len(waitThen)-1]))
		done := r.get(l.ID)
		Expect(done.Watch.End).To(Equal(loops.EndMet))
		Expect(done.State).To(Equal(loops.StateEnded))
	})

	It("says the command in its answer to the model, and nowhere a list reads: not in the label, the line or the reason", func() {
		status := 1
		fx := &scriptHostExec{}
		fx.set(exits("", &status))
		r, chat := build(wait, rigOpts{}, fx)
		l := arm(r, chat)
		Expect(r.h.ResultsFor("wait_for")[0].Out).To(ContainSubstring(secretCommand), "the person must be able to read the command")
		Expect(r.h.ResultsFor("wait_for")[0].Out).To(ContainSubstring("Show the person this command"))
		for _, shown := range []string{l.Prompt, l.Wait, l.Reason, l.Watch.Describe(), l.Watch.Note} {
			Expect(shown).NotTo(ContainSubstring("internal.example"))
			Expect(shown).NotTo(ContainSubstring("curl"))
		}
		Expect(l.Watch.Describe()).To(Equal("the script to exit with status 0"))
		r.pass()
		Expect(r.get(l.ID).Reason).NotTo(ContainSubstring("curl"))
	})

	It("feeds the exit status into a change: the same output with another status is a change", func() {
		status := 1
		fx := &scriptHostExec{}
		fx.set(exits("same", &status))
		r, chat := build(`{"script":"check","every":"1m",`+waitThen+`}`, rigOpts{}, fx)
		l := arm(r, chat)
		r.pass()
		tick(r, time.Minute)
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		status = 2
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring("(changed)"))
	})

	It("reads JSON from the output of a command that failed as not there yet, and judges it once it prints", func() {
		status := 3
		out := ""
		fx := &scriptHostExec{}
		fx.set(func(string) (string, int) { return out, status })
		r, chat := build(`{"script":"job-status","until":"json_equals","field":"state","equals":"done","every":"1m",`+waitThen+`}`, rigOpts{}, fx)
		l := arm(r, chat)
		r.pass()
		tick(r, time.Minute)
		Expect(r.get(l.ID).Watch.Errors).To(BeZero(), "output that is not JSON from a failed command counted as a failed check")
		status, out = 0, `{"state":"done"}`
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		Expect(r.get(l.ID).Watch.End).To(Equal(loops.EndMet))
	})

	It("counts only a command that does not finish, or cannot run, as a failed check, and ends with the source failing", func() {
		fx := &scriptHostExec{text: "partial\n[stopped after 120 seconds]"}
		r, chat := build(`{"script":"sleep 999","every":"1m","max_errors":2,`+waitThen+`}`, rigOpts{}, fx)
		l := arm(r, chat)
		r.pass()
		Expect(r.get(l.ID).Watch.Errors).To(Equal(1))
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		done := r.get(l.ID)
		Expect(done.Watch.End).To(Equal(loops.EndSourceFailing))
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring("the source failed"))
		Expect(done.Watch.LastErr).NotTo(ContainSubstring("sleep 999"))
	})

	Describe("the daily budget of script checks", func() {
		It("charges each check before it runs, ends the wait with a plain text when it is used up, and leaves the model runs alone", func() {
			status := 1
			fx := &scriptHostExec{}
			fx.set(exits("", &status))
			r, chat := build(wait, rigOpts{limits: func() studio.LoopLimits { return studio.LoopLimits{DailyScriptChecks: 2} }}, fx)
			l := arm(r, chat)
			r.pass()
			tick(r, time.Minute)
			Expect(r.get(l.ID).Watch.Checks).To(Equal(2))
			tick(r, time.Minute) // the third check is not paid for
			n, _ := fx.calls()
			Expect(n).To(Equal(2), "a check ran past the budget")
			tick(r, time.Second)
			r.settled(l.ID)
			woke := r.h.Sent(chat)
			Expect(woke).To(HaveLen(2))
			Expect(woke[1]).To(ContainSubstring("today's limit of 2 script checks"))
			Expect(woke[1]).To(ContainSubstring("NOT met"))
			// The wake-up is one model run; the three checks took none.
			ok, err := r.sh.loops.TakeRun(ctx, alice, loops.Day(r.sh.clock.Now()), 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue(), "the checks used model runs")
		})

		It("has a default that is bounded and documented in the loops package", func() {
			Expect(loops.DefaultDailyScriptChecks).To(BeNumerically(">=", 1440), "one wait that checks every minute for a day must fit")
			Expect(loops.DefaultDailyScriptChecks).To(BeNumerically("<=", loops.MaxDailyScriptChecks))
		})
	})

	Describe("when this node cannot run the script", func() {
		It("ends the wait and wakes the model with text that says so, without pinning", func() {
			status := 1
			fx := &scriptHostExec{}
			fx.set(exits("", &status))
			r, chat := build(wait, rigOpts{}, fx)
			l := arm(r, chat)
			r.pass()
			fx.down = "the scratch folder cannot be made"
			tick(r, time.Minute)
			tick(r, time.Second)
			r.settled(l.ID)
			woke := r.h.Sent(chat)
			Expect(woke).To(HaveLen(2))
			Expect(woke[1]).To(ContainSubstring("the script cannot run on this node"))
			Expect(woke[1]).To(ContainSubstring("NOT met"))
			Expect(r.get(l.ID).Watch.Errors).To(BeZero(), "this is not a failed check")
		})

		It("ends the wait when the shell says it did not run the command", func() {
			fx := &scriptHostExec{text: "This server cannot run commands: no scratch."}
			r, chat := build(wait, rigOpts{}, fx)
			l := arm(r, chat)
			r.pass()
			tick(r, time.Second)
			r.settled(l.ID)
			Expect(r.h.Sent(chat)[1]).To(ContainSubstring("the script cannot run on this node"))
		})
	})

	Describe("refusing to arm", func() {
		refused := func(args string, o rigOpts, fx *scriptHostExec, want string) {
			r, chat := build(args, o, fx)
			sent(r.svc, alice, chat, "arm")
			idle(r.svc, alice, chat)
			res := r.h.ResultsFor("wait_for")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(MatchError(ContainSubstring(want)))
			list, err := r.host.List(ctx, alice, chat)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty())
		}

		It("refuses when an administrator switched the built-in bash off and no Sandbox serves", func() {
			refused(wait, rigOpts{mode: "off"}, &scriptHostExec{}, "cannot run a script")
		})
		It("refuses when this machine cannot run commands", func() {
			refused(wait, rigOpts{}, &scriptHostExec{fakeHostExec: fakeHostExec{down: "LocalAI runs as root"}}, "LocalAI runs as root")
		})
		It("refuses when the built-in bash cannot run a command read-only", func() {
			r := newLoopRig(armScript(wait), studio.LoopOptions{}, func(d *studio.Deps) {
				d.HostExec = &fakeHostExec{}
				d.HostExecMode = func() string { return "on" }
			})
			chat := create(r.svc, alice)
			sent(r.svc, alice, chat, "arm")
			idle(r.svc, alice, chat)
			Expect(r.h.ResultsFor("wait_for")[0].Err).To(MatchError(ContainSubstring("leaves the files alone")))
		})
		It("refuses an empty script, two sources, and exit_zero without a script", func() {
			refused(`{"script":"  ",`+waitThen+`}`, rigOpts{}, &scriptHostExec{}, "name exactly one source")
			refused(`{"script":"true","url":"https://example.org/",`+waitThen+`}`, rigOpts{}, &scriptHostExec{}, "name exactly one source")
			refused(`{"tool":"list_downloads","until":"exit_zero",`+waitThen+`}`, rigOpts{}, &scriptHostExec{}, "")
		})
		It("refuses a script that is too long", func() {
			refused(`{"script":"`+strings.Repeat("a", loops.MaxScriptBytes+1)+`",`+waitThen+`}`, rigOpts{}, &scriptHostExec{}, "longer than")
		})
	})

	Describe("in the mode ask", func() {
		// answerCard answers the card that shows the command of the wait.
		answerCard := func(r *loopRig, chat, decision string, shown *[]string) {
			events, cancel, err := r.svc.Events(ctx, alice, chat)
			Expect(err).NotTo(HaveOccurred())
			go func() {
				defer GinkgoRecover()
				defer cancel()
				deadline := time.After(20 * time.Second)
				for {
					select {
					case ev, ok := <-events:
						if !ok {
							return
						}
						if ev.Kind == session.KindHostExecConsent {
							*shown = append(*shown, ev.Text)
							Expect(r.svc.HostExecDecide(ctx, alice, chat, ev.IDs[0], decision)).To(Succeed())
							return
						}
					case <-deadline:
						return
					}
				}
			}()
		}

		It("shows the whole command in one card when the wait is armed, then checks with no card", func() {
			status := 1
			fx := &scriptHostExec{}
			fx.set(exits("", &status))
			r, chat := build(wait, rigOpts{mode: "ask"}, fx)
			var shown []string
			answerCard(r, chat, studio.HostExecOnce, &shown)
			l := arm(r, chat)
			Expect(shown).To(Equal([]string{secretCommand}))
			r.pass()
			for range 3 {
				tick(r, time.Minute)
			}
			n, _ := fx.calls()
			Expect(n).To(Equal(4))
			rec, err := r.svc.Get(ctx, alice, chat)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HostConsent).To(BeNil(), "a check asked again")
			Expect(r.get(l.ID).Watch.Errors).To(BeZero())
		})

		It("arms nothing when the administrator refuses", func() {
			fx := &scriptHostExec{}
			r, chat := build(wait, rigOpts{mode: "ask"}, fx)
			var shown []string
			answerCard(r, chat, studio.HostExecDeny, &shown)
			sent(r.svc, alice, chat, "arm")
			idle(r.svc, alice, chat)
			Expect(r.h.ResultsFor("wait_for")[0].Err).To(MatchError(ContainSubstring("did not allow")))
			list, _ := r.host.List(ctx, alice, chat)
			Expect(list).To(BeEmpty())
		})

		It("refuses a command that is too long for the card", func() {
			fx := &scriptHostExec{}
			r, chat := build(`{"script":"echo `+strings.Repeat("a", 2000)+`","every":"1m",`+waitThen+`}`, rigOpts{mode: "ask"}, fx)
			sent(r.svc, alice, chat, "arm")
			idle(r.svc, alice, chat)
			Expect(r.h.ResultsFor("wait_for")[0].Err).To(MatchError(ContainSubstring("longer than 2000 bytes")))
			rec, err := r.svc.Get(ctx, alice, chat)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HostConsent).To(BeNil(), "no card was shown")
		})

		It("is for an administrator only", func() {
			fx := &scriptHostExec{}
			r := newLoopRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "wait_for", Args: wait}, {Say: "armed"}}
			}, studio.LoopOptions{}, func(d *studio.Deps) {
				d.HostExec = fx
				d.HostExecMode = func() string { return "ask" }
				d.IsAdmin = func(_ context.Context, user string) bool { return user == alice }
			})
			chat := create(r.svc, bob)
			sent(r.svc, bob, chat, "arm")
			idle(r.svc, bob, chat)
			Expect(r.h.ResultsFor("wait_for")[0].Err).To(MatchError(ContainSubstring("only an administrator")))
		})
	})

	Describe("when a Sandbox add-on serves bash", func() {
		It("runs the checks there, read-only, with no card even in the mode ask, and not in the built-in bash", func() {
			sb := &sandboxAddons{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}, exit: 1}
			fx := &scriptHostExec{}
			fx.set(exits("", new(int)))
			r := newLoopRig(armScript(wait), studio.LoopOptions{}, func(d *studio.Deps) {
				d.HostExec = fx
				d.HostExecMode = func() string { return "ask" }
				d.IsAdmin = func(_ context.Context, user string) bool { return user == alice }
				d.AddonTools = sb
			})
			chat := create(r.svc, alice)
			l := arm(r, chat)
			Expect(r.h.ResultsFor("wait_for")[0].Err).To(BeNil())
			rec, err := r.svc.Get(ctx, alice, chat)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HostConsent).To(BeNil(), "the Sandbox needs no approval of the host")
			r.pass()
			tick(r, time.Minute)
			sb.mu2.Lock()
			Expect(sb.readOnly).To(Equal([]bool{true, true}), "a check ran in the sandbox with write access")
			sb.mu2.Unlock()
			n, _ := fx.calls()
			Expect(n).To(BeZero(), "the built-in bash ran a check although the Sandbox serves bash")
			Expect(r.get(l.ID).Watch.LastExit).To(Equal(1))
			sb.mu2.Lock()
			sb.exit = 0
			sb.mu2.Unlock()
			tick(r, time.Minute)
			tick(r, time.Second)
			r.settled(l.ID)
			Expect(r.get(l.ID).Watch.End).To(Equal(loops.EndMet))
		})
	})

	It("is one wait for the same script and two for different ones", func() {
		status := 1
		fx := &scriptHostExec{}
		fx.set(exits("", &status))
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "same":
				return []studiotest.ToolStep{{Tool: "wait_for", Args: `{"script":"true","every":"1m",` + waitThen + `}`}, {Say: "ok"}}
			case "other":
				return []studiotest.ToolStep{{Tool: "wait_for", Args: `{"script":"false","every":"1m",` + waitThen + `}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{}, func(d *studio.Deps) {
			d.HostExec = fx
			d.HostExecMode = func() string { return "on" }
		})
		chat := create(r.svc, alice)
		for _, text := range []string{"same", "same", "other"} {
			sent(r.svc, alice, chat, text)
			idle(r.svc, alice, chat)
		}
		list, err := r.host.List(ctx, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(r.h.ResultsFor("wait_for")[1].Out).To(ContainSubstring("already exists"))
		Expect(list[0].Watch.Script).NotTo(Equal(list[1].Watch.Script))
	})
})
