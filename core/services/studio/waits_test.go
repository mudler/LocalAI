package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// source is a read-only tool whose output a test controls.
type source struct {
	mu    sync.Mutex
	out   string
	err   error
	calls int
}

func (s *source) set(out string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out, s.err = out, err
}

func (s *source) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *source) tools(names ...string) func(*studio.Deps) {
	return func(d *studio.Deps) {
		d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) {
			var ts []session.Tool
			for _, n := range names {
				ts = append(ts, session.Tool{Name: n, Description: n, Schema: json.RawMessage(`{"type":"object"}`),
					Run: func(context.Context, json.RawMessage) (string, error) {
						s.mu.Lock()
						defer s.mu.Unlock()
						s.calls++
						return s.out, s.err
					}})
			}
			return ts, nil
		}
	}
}

func armScript(args string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "arm" {
			return []studiotest.ToolStep{{Tool: "wait_for", Args: args}, {Say: "armed"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

const waitThen = `"then":"Move the file to the archive."`

var _ = Describe("wait_for", func() {
	ctx := context.Background()

	// armed starts a wait through the model and returns its chat and loop.
	armed := func(args string, src *source, extra ...func(*studio.Deps)) (*loopRig, string, loops.Loop) {
		r := newLoopRig(armScript(args), studio.LoopOptions{}, append([]func(*studio.Deps){src.tools("list_downloads", "add_downloads")}, extra...)...)
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)
		res := r.h.ResultsFor("wait_for")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(BeNil(), "%v", res[0].Err)
		list, err := r.host.List(ctx, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		return r, chat, list[0]
	}
	tick := func(r *loopRig, d time.Duration) {
		r.sh.clock.Advance(d)
		r.pass()
	}
	const poll = `{"tool":"list_downloads","arguments":{},"every":"1m","timeout":"10m",` + waitThen + `,"reason":"the download"}`

	It("is offered next to start_loop and appears in the loops list with its reason", func() {
		src := &source{out: "10%"}
		r, chat, l := armed(poll, src)
		Expect(r.h.ToolNames(chat)).To(ContainElements("start_loop", "wait_for"))
		Expect(l.Watch).NotTo(BeNil())
		Expect(l.State).To(Equal(loops.StateActive))
		Expect(l.Wait).To(Equal("the download"))
		Expect(l.SelfPaced()).To(BeFalse())
		Expect(r.h.ResultsFor("wait_for")[0].Out).To(ContainSubstring("woken once"))
	})

	It("stores a baseline at the first check and does not wake", func() {
		src := &source{out: "10%"}
		r, chat, l := armed(poll, src)
		r.pass()
		got := r.get(l.ID)
		Expect(src.count()).To(Equal(1))
		Expect(got.Watch.Checks).To(Equal(1))
		Expect(got.Watch.BaseOut).To(Equal("10%"))
		Expect(got.Reason).To(ContainSubstring("baseline"))
		Expect(r.runs(chat)).To(Equal(0))
		Expect(r.h.Sent(chat)).To(HaveLen(1), "the model was called by a check")
	})

	It("does not wake while the output is unchanged, and wakes once it changes", func() {
		src := &source{out: "10%"}
		r, chat, l := armed(poll, src)
		r.pass()
		for range 3 {
			tick(r, time.Minute)
		}
		Expect(src.count()).To(Equal(4))
		Expect(r.h.Sent(chat)).To(HaveLen(1), "an unchanged output woke the model")
		before := r.get(l.ID)
		Expect(before.Runs).To(Equal(0))

		src.set("100%", nil)
		tick(r, time.Minute) // the check finds the change
		tick(r, time.Second) // the next pass runs the model
		r.settled(l.ID)
		sentTexts := r.h.Sent(chat)
		Expect(sentTexts).To(HaveLen(2))
		Expect(sentTexts[1]).To(ContainSubstring("the condition was met (changed)"))
		Expect(sentTexts[1]).To(ContainSubstring("-10%"))
		Expect(sentTexts[1]).To(ContainSubstring("+100%"))
		Expect(sentTexts[1]).To(ContainSubstring("Move the file to the archive."))
		done := r.get(l.ID)
		Expect(done.State).To(Equal(loops.StateEnded))
		Expect(done.Watch.BaseOut).To(Equal("100%"), "the new hash is stored before the wake")
		n := src.count()
		tick(r, time.Hour)
		Expect(src.count()).To(Equal(n), "an ended wait kept polling")
	})

	It("wakes when the output matches the pattern", func() {
		src := &source{out: "downloading 40%"}
		r, chat, l := armed(`{"tool":"list_downloads","until":"match","pattern":"complete|finished","every":"1m",`+waitThen+`}`, src)
		r.pass()
		tick(r, time.Minute)
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		src.set("a.iso finished", nil)
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring("(matched)"))
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring("finished"))
	})

	It("wakes when a JSON field equals the value", func() {
		src := &source{out: `{"items":[{"status":"running"}]}`}
		r, chat, l := armed(`{"tool":"list_downloads","until":"json_equals","field":"items.0.status","equals":"done","every":"1m",`+waitThen+`}`, src)
		r.pass()
		tick(r, time.Minute)
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		src.set(`{"items":[{"status":"done"}]}`, nil)
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring(`equals "done"`))
	})

	It("does not count an error as a change", func() {
		src := &source{out: "10%"}
		r, chat, l := armed(poll, src)
		r.pass()
		src.set("", errors.New("the add-on is restarting"))
		tick(r, time.Minute)
		tick(r, time.Minute)
		got := r.get(l.ID)
		Expect(got.Watch.Errors).To(Equal(2))
		Expect(got.Watch.BaseOut).To(Equal("10%"), "an error replaced the baseline")
		src.set("10%", nil)
		tick(r, time.Minute)
		tick(r, time.Second)
		got = r.get(l.ID)
		Expect(got.Watch.Errors).To(Equal(0))
		Expect(got.Watch.Wake).To(BeEmpty(), "an error followed by the same output woke the model")
		Expect(r.h.Sent(chat)).To(HaveLen(1))
	})

	It("wakes the model with a failure reason after repeated errors", func() {
		src := &source{out: "10%"}
		r, chat, l := armed(`{"tool":"list_downloads","every":"1m","timeout":"10m","max_errors":2,`+waitThen+`}`, src)
		r.pass()
		src.set("", errors.New("connection refused"))
		tick(r, time.Minute)
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		text := r.h.Sent(chat)[1]
		Expect(text).To(ContainSubstring("the source failed. The condition was NOT met"))
		Expect(text).To(ContainSubstring("2 checks in a row failed"))
		Expect(text).To(ContainSubstring("connection refused"))
		Expect(text).To(ContainSubstring("Do not do the planned step"))
	})

	It("wakes the model at the timeout and says the condition was not met", func() {
		src := &source{out: "10%"}
		r, chat, l := armed(`{"tool":"list_downloads","every":"1m","timeout":"3m",`+waitThen+`}`, src)
		r.pass()
		for range 3 {
			tick(r, time.Minute)
		}
		tick(r, time.Second)
		r.settled(l.ID)
		text := r.h.Sent(chat)[1]
		Expect(text).To(ContainSubstring("The condition was NOT met"))
		Expect(text).To(ContainSubstring("timed out"))
		Expect(text).To(ContainSubstring("last output: 10%"))
		Expect(r.get(l.ID).State).To(Equal(loops.StateEnded))
	})

	It("keeps the wake until the busy chat is free", func() {
		block := make(chan struct{})
		src := &source{out: "10%"}
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "arm":
				return []studiotest.ToolStep{{Tool: "wait_for", Args: poll}, {Say: "armed"}}
			case "long task":
				return []studiotest.ToolStep{{Block: block}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{}, src.tools("list_downloads"))
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)
		l, _ := r.host.List(ctx, alice, chat)
		r.pass()
		src.set("100%", nil)
		sent(r.svc, alice, chat, "long task")
		until("the turn", func() bool { return turn(r.svc, alice, chat).Running })
		tick(r, time.Minute)
		tick(r, time.Minute)
		Expect(r.get(l[0].ID).Watch.Wake).NotTo(BeEmpty())
		Expect(r.get(l[0].ID).Runs).To(Equal(0))
		close(block)
		idle(r.svc, alice, chat)
		tick(r, time.Second)
		r.settled(l[0].ID)
		Expect(r.get(l[0].ID).State).To(Equal(loops.StateEnded))
	})

	Describe("sources", func() {
		It("refuses a tool the chat does not have", func() {
			src := &source{}
			r := newLoopRig(armScript(`{"tool":"list_secrets",`+waitThen+`}`), studio.LoopOptions{}, src.tools("list_downloads"))
			chat := create(r.svc, alice)
			sent(r.svc, alice, chat, "arm")
			idle(r.svc, alice, chat)
			res := r.h.ResultsFor("wait_for")[0]
			Expect(res.Err).To(MatchError(ContainSubstring("no tool \"list_secrets\" that it may call")))
			list, _ := r.host.List(ctx, alice, chat)
			Expect(list).To(BeEmpty())
		})

		It("refuses a tool that changes things, and the loop tools", func() {
			src := &source{}
			for _, name := range []string{"add_downloads", "start_loop", "spawn_agent"} {
				r := newLoopRig(armScript(`{"tool":"`+name+`",`+waitThen+`}`), studio.LoopOptions{}, src.tools("list_downloads", "add_downloads"))
				chat := create(r.svc, alice)
				sent(r.svc, alice, chat, "arm")
				idle(r.svc, alice, chat)
				Expect(r.h.ResultsFor("wait_for")[0].Err).To(MatchError(ContainSubstring("cannot be polled")), name)
			}
			Expect(src.count()).To(BeZero())
		})

		It("stops polling a tool the chat lost, with a failure wake", func() {
			src := &source{out: "10%"}
			var gone bool
			var mu sync.Mutex
			r, chat, l := armed(`{"tool":"list_downloads","every":"1m","max_errors":1,`+waitThen+`}`, src, func(d *studio.Deps) {
				inner := d.HostTools
				d.HostTools = func(ctx context.Context, a workspace.Actor) ([]session.Tool, error) {
					mu.Lock()
					defer mu.Unlock()
					if gone {
						return nil, nil
					}
					return inner(ctx, a)
				}
			})
			r.pass()
			mu.Lock()
			gone = true
			mu.Unlock()
			calls := src.count()
			tick(r, time.Minute)
			tick(r, time.Second)
			r.settled(l.ID)
			Expect(src.count()).To(Equal(calls), "a tool the chat may not call was run")
			Expect(r.h.Sent(chat)[1]).To(ContainSubstring("NOT met"))
		})

		It("reads a URL through web_fetch, so the egress checks apply", func() {
			w := &fakeWeb{fetch: true}
			r, chat, l := armed(`{"url":"https://status.example.com/job/7","every":"1m",`+waitThen+`}`, &source{}, withWeb(w))
			r.pass()
			w.mu.Lock()
			Expect(w.fetches).To(HaveLen(1))
			Expect(w.fetches[0]).To(ContainSubstring("https://status.example.com/job/7"))
			w.mu.Unlock()
			Expect(r.get(l.ID).Watch.Checks).To(Equal(1))
			Expect(r.h.Sent(chat)).To(HaveLen(1))
		})

		It("refuses a URL when web_fetch is off, and a non-http address", func() {
			w := &fakeWeb{}
			r := newLoopRig(armScript(`{"url":"https://status.example.com/",`+waitThen+`}`), studio.LoopOptions{}, withWeb(w))
			chat := create(r.svc, alice)
			sent(r.svc, alice, chat, "arm")
			idle(r.svc, alice, chat)
			Expect(r.h.ResultsFor("wait_for")[0].Err).To(MatchError(ContainSubstring("web_fetch is switched off")))

			w2 := &fakeWeb{fetch: true}
			r = newLoopRig(armScript(`{"url":"file:///etc/passwd",`+waitThen+`}`), studio.LoopOptions{}, withWeb(w2))
			chat = create(r.svc, alice)
			sent(r.svc, alice, chat, "arm")
			idle(r.svc, alice, chat)
			Expect(r.h.ResultsFor("wait_for")[0].Err).To(MatchError(ContainSubstring("http or https")))
			Expect(w2.fetches).To(BeEmpty())
		})
	})

	It("keeps the interval at the minimum the server allows", func() {
		src := &source{out: "x"}
		r, _, l := armed(`{"tool":"list_downloads","every":"5s",`+waitThen+`}`, src)
		Expect(l.Interval).To(Equal(time.Minute))
		_ = r
	})
})
