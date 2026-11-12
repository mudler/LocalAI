package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
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
	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/webtools"
)

// pageFetcher serves one page to the real web_fetch, so a wait reads it
// through the same wrapper a model would see.
type pageFetcher struct {
	mu   sync.Mutex
	ct   string
	body string
	cut  bool
}

func (f *pageFetcher) set(ct, body string, cut bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ct, f.body, f.cut = ct, body, cut
}

func (f *pageFetcher) Get(_ context.Context, raw string, _ []string) (*safehttp.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, _ := url.Parse(raw)
	return &safehttp.Response{FinalURL: u, ContentType: f.ct, Body: []byte(f.body), Truncated: f.cut}, nil
}

// realWeb is web_fetch of the real webtools package over a fake fetcher.
type realWeb struct {
	*fakeWeb
	tools webtools.Tools
}

func (w *realWeb) Fetch(ctx context.Context, args json.RawMessage) (string, error) {
	_, _ = w.fakeWeb.Fetch(ctx, args) // record the call
	return w.tools.WebFetch(ctx, args)
}

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

	// bigDownloads is the output of a list_downloads-shaped tool: many items
	// with long URLs and error texts, so the JSON is well over the 8000
	// characters that a wait stores.
	bigDownloads := func(first string) string {
		items := make([]map[string]string, 0, 60)
		for i := range 60 {
			st := "running"
			if i == 0 {
				st = first
			}
			items = append(items, map[string]string{
				"id": "dl-" + strconv.Itoa(i), "status": st,
				"url":   "https://example.org/models/" + strings.Repeat("path-segment/", 12) + "file-" + strconv.Itoa(i) + ".gguf",
				"error": strings.Repeat("retrying after a temporary network error; ", 4),
			})
		}
		raw, err := json.Marshal(map[string]any{"items": items})
		Expect(err).NotTo(HaveOccurred())
		Expect(len(raw)).To(BeNumerically(">", loops.MaxWatchOutput))
		return string(raw)
	}

	It("judges a JSON field on the whole output, even when it is longer than the stored text", func() {
		src := &source{out: bigDownloads("running")}
		r, chat, l := armed(`{"tool":"list_downloads","until":"json_equals","field":"items.0.status","equals":"completed","every":"1m",`+waitThen+`}`, src)
		r.pass()
		tick(r, time.Minute)
		got := r.get(l.ID)
		Expect(got.Watch.Errors).To(Equal(0), "the long output was judged as an error: %s", got.Watch.LastErr)
		Expect(r.h.Sent(chat)).To(HaveLen(1))
		src.set(bigDownloads("completed"), nil)
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring(`equals "completed"`))
		Expect(r.get(l.ID).Watch.End).To(Equal(loops.EndMet))
	})

	It("stores at most the cap of a long output but still sees a change beyond it", func() {
		src := &source{out: bigDownloads("running")}
		r, chat, l := armed(poll, src)
		r.pass()
		Expect(len(r.get(l.ID).Watch.BaseOut)).To(BeNumerically("<=", loops.MaxWatchOutput))
		// Only the last item differs, far beyond the stored text.
		long := bigDownloads("running")
		i := strings.LastIndex(long, "running")
		src.set(long[:i]+"completed"+long[i+len("running"):], nil)
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring("(changed)"))
		Expect(len(r.get(l.ID).Watch.BaseOut)).To(BeNumerically("<=", loops.MaxWatchOutput))
	})

	It("matches a pattern that lies beyond the stored text", func() {
		src := &source{out: strings.Repeat("x", 2*loops.MaxWatchOutput)}
		r, chat, l := armed(`{"tool":"list_downloads","until":"match","pattern":"all done$","every":"1m",`+waitThen+`}`, src)
		r.pass()
		src.set(strings.Repeat("x", 2*loops.MaxWatchOutput)+"all done", nil)
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		Expect(r.h.Sent(chat)[1]).To(ContainSubstring("(matched)"))
	})

	It("ends a json_equals wait early when the output is never JSON", func() {
		src := &source{out: "not json at all"}
		r, chat, l := armed(`{"tool":"list_downloads","until":"json_equals","field":"items.0.status","equals":"done","every":"1m","timeout":"1h","max_errors":3,`+waitThen+`}`, src)
		r.pass()
		tick(r, time.Minute)
		Expect(r.get(l.ID).Watch.Errors).To(Equal(2))
		Expect(r.get(l.ID).State).To(Equal(loops.StateActive))
		tick(r, time.Minute)
		tick(r, time.Second)
		r.settled(l.ID)
		got := r.get(l.ID)
		Expect(got.Watch.End).To(Equal(loops.EndSourceFailing))
		text := r.h.Sent(chat)[1]
		Expect(text).To(ContainSubstring("the source failed. The condition was NOT met"))
		Expect(text).To(ContainSubstring("3 checks in a row failed"))
		Expect(text).To(ContainSubstring("not JSON"))
	})

	It("reports an output over the read limit as its own error", func() {
		src := &source{out: strings.Repeat("x", 4<<20+1)}
		r, _, l := armed(`{"tool":"list_downloads","until":"json_equals","field":"a","equals":"b","every":"1m",`+waitThen+`}`, src)
		r.pass()
		got := r.get(l.ID)
		Expect(got.Watch.Errors).To(Equal(1))
		Expect(got.Watch.LastErr).To(ContainSubstring("larger than 4 MiB"))
		Expect(got.Watch.LastErr).NotTo(ContainSubstring("unexpected EOF"))
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

	Describe("a URL source", func() {
		const jsonWait = `{"url":"https://status.example.com/job.json","until":"json_equals","field":"job.done","equals":"true","every":"1m","timeout":"1h","max_errors":3,` + waitThen + `}`
		bigJSON := func(done bool) string {
			return `{"log":"` + strings.Repeat("x", 3*loops.MaxWatchOutput) + `","job":{"done":` + strconv.FormatBool(done) + `}}`
		}
		rig := func(args string, f *pageFetcher) (*loopRig, string, loops.Loop, *realWeb) {
			w := &realWeb{fakeWeb: &fakeWeb{fetch: true}, tools: webtools.Tools{Fetch: f}}
			r, chat, l := armed(args, &source{}, withWeb(w))
			return r, chat, l, w
		}

		It("matches text that lies beyond the first 8000 characters", func() {
			f := &pageFetcher{}
			f.set("text/plain", strings.Repeat("x", 3*loops.MaxWatchOutput), false)
			r, chat, l, w := rig(`{"url":"https://status.example.com/p","until":"match","pattern":"all done$","every":"1m",`+waitThen+`}`, f)
			r.pass()
			Expect(w.fetches[0]).To(ContainSubstring(strconv.Itoa(webtools.HardMaxChars)))
			f.set("text/plain", strings.Repeat("x", 3*loops.MaxWatchOutput)+" all done", false)
			tick(r, time.Minute)
			tick(r, time.Second)
			r.settled(l.ID)
			Expect(r.h.Sent(chat)[1]).To(ContainSubstring("(matched)"))
		})

		It("sees a change beyond the first 8000 characters and stores only the cap", func() {
			f := &pageFetcher{}
			f.set("text/plain", strings.Repeat("x", 3*loops.MaxWatchOutput)+" a", false)
			r, chat, l, _ := rig(`{"url":"https://status.example.com/p","every":"1m",`+waitThen+`}`, f)
			r.pass()
			Expect(len(r.get(l.ID).Watch.BaseOut)).To(BeNumerically("<=", loops.MaxWatchOutput))
			f.set("text/plain", strings.Repeat("x", 3*loops.MaxWatchOutput)+" b", false)
			tick(r, time.Minute)
			tick(r, time.Second)
			r.settled(l.ID)
			Expect(r.h.Sent(chat)[1]).To(ContainSubstring("(changed)"))
		})

		It("judges a JSON body over 8000 characters, served as application/json", func() {
			f := &pageFetcher{}
			f.set("application/json", bigJSON(false), false)
			r, chat, l, _ := rig(jsonWait, f)
			r.pass()
			Expect(r.get(l.ID).Watch.Errors).To(Equal(0), "%s", r.get(l.ID).Watch.LastErr)
			f.set("application/json", bigJSON(true), false)
			tick(r, time.Minute)
			tick(r, time.Second)
			r.settled(l.ID)
			Expect(r.h.Sent(chat)[1]).To(ContainSubstring(`equals "true"`))
			Expect(r.get(l.ID).Watch.End).To(Equal(loops.EndMet))
		})

		It("reports a cut download as its own error in json_equals mode", func() {
			f := &pageFetcher{}
			f.set("application/json", bigJSON(true)[:20000], true)
			r, _, l, _ := rig(jsonWait, f)
			r.pass()
			got := r.get(l.ID)
			Expect(got.Watch.Errors).To(Equal(1))
			Expect(got.Watch.LastErr).To(ContainSubstring("cut"))
			Expect(got.Watch.LastErr).NotTo(ContainSubstring("unexpected EOF"))
		})

		It("reports a body cut at the character limit as its own error in json_equals mode", func() {
			f := &pageFetcher{}
			f.set("application/json", `{"log":"`+strings.Repeat("x", webtools.HardMaxChars+10)+`","job":{"done":true}}`, false)
			r, _, l, _ := rig(jsonWait, f)
			r.pass()
			got := r.get(l.ID)
			Expect(got.Watch.LastErr).To(ContainSubstring("cut"))
			Expect(got.Watch.LastErr).NotTo(ContainSubstring("unexpected EOF"))
		})

		It("accepts a cut page in match mode", func() {
			f := &pageFetcher{}
			f.set("text/plain", "start "+strings.Repeat("x", 100), true)
			r, _, l, _ := rig(`{"url":"https://status.example.com/p","until":"match","pattern":"start","every":"1m",`+waitThen+`}`, f)
			r.pass()
			Expect(r.get(l.ID).Watch.Errors).To(Equal(0))
			Expect(r.get(l.ID).Watch.Checks).To(Equal(1))
		})
	})

	It("answers a second wait on the same source and condition with the first one", func() {
		src := &source{out: "10%"}
		r, chat, _ := armed(poll, src)
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)
		res := r.h.ResultsFor("wait_for")
		Expect(res).To(HaveLen(2))
		Expect(res[1].Err).To(BeNil(), "%v", res[1].Err)
		Expect(res[1].Out).To(ContainSubstring("The wait is armed for"))
		Expect(res[1].Out).To(ContainSubstring("already exists"))
		Expect(res[1].Out).To(ContainSubstring("wakes you once"))
		list, err := r.host.List(ctx, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1), "a repeated wait_for made a second loop")
	})

	It("keeps the interval at the minimum the server allows", func() {
		src := &source{out: "x"}
		r, _, l := armed(`{"tool":"list_downloads","every":"5s",`+waitThen+`}`, src)
		Expect(l.Interval).To(Equal(time.Minute))
		_ = r
	})
})
