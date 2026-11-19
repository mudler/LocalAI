// SPDX-License-Identifier: MIT
package clientdemo

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeHost is a host that runs jobs: a started job stays running until the
// test ends it.
type fakeHost struct {
	mu     sync.Mutex
	status map[string]string
	n      int
	ops    []string
	opIDs  []string
	fail   error
	args   []string
}

func newFakeHost() *fakeHost { return &fakeHost{status: map[string]string{}} }

func (f *fakeHost) set(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = status
}

func (f *fakeHost) Do(_ context.Context, opID, op string, args, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op)
	f.opIDs = append(f.opIDs, opID)
	if f.fail != nil {
		return f.fail
	}
	b, _ := json.Marshal(args)
	if strings.HasPrefix(op, "client.workspace.") {
		f.args = append(f.args, string(b))
		return json.Unmarshal([]byte(`{"ok":true}`), out)
	}
	var a struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &a)
	var j Job
	switch op {
	case "client.jobs.start":
		f.n++
		j = Job{ID: "job-" + string(rune('a'+f.n)), Status: "pending"}
		f.status[j.ID] = "pending"
	case "client.jobs.get":
		s, ok := f.status[a.ID]
		if !ok {
			return &contract.HostError{Code: contract.HostNotFound}
		}
		j = Job{ID: a.ID, Status: s}
		if s == "completed" {
			j.Result = "42"
		}
	case "client.jobs.cancel":
		f.status[a.ID] = "cancelled"
		j = Job{ID: a.ID, Status: "cancelled"}
	}
	rb, _ := json.Marshal(j)
	return json.Unmarshal(rb, out)
}

// fakeClock is a clock that a spec moves; streams of an earlier spec may still
// read the clock they started with.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var _ = Describe("the client example add-on", func() {
	var (
		dir  string
		host *fakeHost
		rt   *Runtime
		clk  *fakeClock
	)
	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		host = newFakeHost()
		clk = &fakeClock{t: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
		var err error
		rt, err = New(host, Options{Dir: dir, Poll: 20 * time.Millisecond, Now: clk.Now})
		Expect(err).NotTo(HaveOccurred())
	})

	client := contract.ClientIdentity{ID: "key-1", Name: "box"}
	start := func(d contract.ClientIdentity, body string) contract.ClientResponse {
		out, err := rt.ClientCall(context.Background(), contract.ClientRequest{OperationID: "cl-op", RouteID: "start", Method: "POST", Path: "/jobs", Body: []byte(body), Client: d})
		Expect(err).NotTo(HaveOccurred())
		return out
	}

	It("declares the routes of its manifest", func() {
		b, err := os.ReadFile("manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.RequiredFeatureLevel()).To(Equal(uint64(8)), "the file routes use a wildcard tail")
		Expect(contract.ValidateManifest(m, contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: contract.HostSDKVersion, DataFormat: 1, FeatureLevel: contract.HostFeatureLevel, OS: "linux", Arch: "amd64"})).To(Succeed())
		ids := map[string]bool{}
		for _, r := range m.Capabilities.Client.Routes {
			ids[r.ID] = true
		}
		Expect(ids).To(Equal(map[string]bool{"ping": true, "start": true, "job": true, "cancel": true, "events": true, "files": true, "file": true, "save": true}))
	})

	Describe("the file routes", func() {
		call := func(route, method string, params map[string]string, query map[string]string, body string) contract.ClientResponse {
			out, err := rt.ClientCall(context.Background(), contract.ClientRequest{OperationID: "cl-op", RouteID: route, Method: method, Params: params, Query: query, Body: []byte(body), Client: client})
			Expect(err).NotTo(HaveOccurred())
			return out
		}
		It("hands the joined tail to the host operations, which check it against the grant", func() {
			Expect(call("file", "GET", map[string]string{"path": "Notes/todo.md"}, nil, "").Status).To(Equal(200))
			Expect(call("file", "GET", map[string]string{"path": "Notes"}, map[string]string{"list": "1"}, "").Status).To(Equal(200))
			Expect(call("save", "PUT", map[string]string{"path": "box/a.md"}, nil, `{"content":"hi"}`).Status).To(Equal(200))
			Expect(call("files", "GET", nil, nil, "").Status).To(Equal(200))
			Expect(host.ops).To(Equal([]string{"client.workspace.read", "client.workspace.list", "client.workspace.write", "client.workspace.list"}))
			Expect(host.args[0]).To(Equal(`{"path":"Notes/todo.md"}`))
			Expect(host.args[2]).To(ContainSubstring(`"content":"hi"`))
		})
		It("validates the tail itself before it asks the host", func() {
			for _, p := range []string{"", "../x.md", "a/../b.md", "a//b.md", `a\b.md`, ".hidden.md", "a/./b", "a/b/c/d/e/f/g/h/i/j.md"} {
				Expect(call("file", "GET", map[string]string{"path": p}, nil, "").Status).To(Equal(400), p)
				Expect(call("save", "PUT", map[string]string{"path": p}, nil, `{"content":"x"}`).Status).To(Equal(400), p)
			}
			Expect(host.ops).To(BeEmpty())
			Expect(call("save", "PUT", map[string]string{"path": "a.md"}, nil, `{"content":"x","extra":1}`).Status).To(Equal(400))
		})
		It("turns a refusal of the host into an answer a client can act on", func() {
			host.fail = &contract.HostError{Code: contract.HostNotFound}
			Expect(call("file", "GET", map[string]string{"path": "a.md"}, nil, "").Status).To(Equal(404))
			host.fail = &contract.HostError{Code: contract.HostBadRequest}
			Expect(call("save", "PUT", map[string]string{"path": "a.md"}, nil, `{"content":"x"}`).Status).To(Equal(400))
		})
	})

	Describe("the outbox", func() {
		It("gives ids that only grow, also after a restart", func() {
			o, err := OpenOutbox(filepath.Join(dir, "o.json"), time.Hour, clk.Now)
			Expect(err).NotTo(HaveOccurred())
			a, _ := o.Append(EventNotice, map[string]string{"n": "1"})
			b, _ := o.Append(EventNotice, map[string]string{"n": "2"})
			Expect(b.Seq).To(Equal(a.Seq + 1))
			again, err := OpenOutbox(filepath.Join(dir, "o.json"), time.Hour, clk.Now)
			Expect(err).NotTo(HaveOccurred())
			c, _ := again.Append(EventNotice, map[string]string{"n": "3"})
			Expect(c.Seq).To(Equal(b.Seq + 1))
			events, gap := again.After(a.Seq)
			Expect(gap).To(BeFalse())
			Expect(events).To(HaveLen(2))
		})

		It("drops events that expired and says that the client missed some", func() {
			o, _ := OpenOutbox(filepath.Join(dir, "o.json"), time.Minute, clk.Now)
			first, _ := o.Append(EventNotice, 1)
			clk.Add(2 * time.Minute)
			o.Append(EventNotice, 2) //nolint:errcheck
			events, gap := o.After(first.Seq)
			Expect(events).To(HaveLen(1))
			Expect(gap).To(BeFalse(), "the client saw event 1; nothing it needs is gone")
			// A client that last saw an event before the dropped one has a gap.
			_, gap = o.After(first.Seq - 1 + 0)
			Expect(gap).To(BeFalse(), "after 0 means nothing seen")
			o.Append(EventNotice, 3) //nolint:errcheck
			clk.Add(2 * time.Minute)
			o.Append(EventNotice, 4) //nolint:errcheck
			_, gap = o.After(2)
			Expect(gap).To(BeTrue())
		})

		It("keeps at most MaxEvents and watches at most MaxTracked jobs", func() {
			o, _ := OpenOutbox(filepath.Join(dir, "o.json"), time.Hour, clk.Now)
			for i := range MaxEvents + 20 {
				_, err := o.Append(EventNotice, i)
				Expect(err).NotTo(HaveOccurred())
			}
			events, gap := o.After(5)
			Expect(events).To(HaveLen(MaxEvents))
			Expect(gap).To(BeTrue())
			for i := range MaxTracked {
				Expect(o.Track("j"+string(rune('a'+i)), "pending")).To(Succeed())
			}
			Expect(o.Track("one-too-many", "pending")).To(HaveOccurred())
		})
	})

	It("removes the outbox of a client that has been silent for a week, as a revoked one is", func() {
		old := filepath.Join(dir, "outbox", "gone.json")
		fresh := filepath.Join(dir, "outbox", "here.json")
		Expect(os.WriteFile(old, []byte(`{}`), 0o600)).To(Succeed())
		Expect(os.WriteFile(fresh, []byte(`{}`), 0o600)).To(Succeed())
		week := clk.Now().Add(-8 * 24 * time.Hour)
		Expect(os.Chtimes(old, week, week)).To(Succeed())
		_, err := New(host, Options{Dir: dir, Now: clk.Now})
		Expect(err).NotTo(HaveOccurred())
		Expect(old).NotTo(BeAnExistingFile())
		Expect(fresh).To(BeAnExistingFile())
	})

	It("sweeps stale outbox files while it runs, and forgets their outboxes", func() {
		start(client, `{"task":"ask"}`)
		file := filepath.Join(dir, "outbox", client.ID+".json")
		Expect(file).To(BeAnExistingFile())
		week := clk.Now().Add(-8 * 24 * time.Hour)
		Expect(os.Chtimes(file, week, week)).To(Succeed())
		clk.Add(9 * 24 * time.Hour)
		_, err := rt.box("key-2") // any client asking triggers the sweep
		Expect(err).NotTo(HaveOccurred())
		Expect(file).NotTo(BeAnExistingFile())
		rt.mu.Lock()
		defer rt.mu.Unlock()
		Expect(rt.outbox).NotTo(HaveKey(client.ID))
		Expect(rt.outbox).To(HaveKey("key-2"))
	})

	Describe("the outbox under failure and parallel use", func() {
		It("changes nothing when the file cannot be written", func() {
			sub := filepath.Join(dir, "sub")
			Expect(os.Mkdir(sub, 0o700)).To(Succeed())
			o, err := OpenOutbox(filepath.Join(sub, "o.json"), time.Hour, clk.Now)
			Expect(err).NotTo(HaveOccurred())
			// The directory of the file turns into a plain file: every write fails.
			Expect(os.Remove(sub)).To(Succeed())
			Expect(os.WriteFile(sub, nil, 0o600)).To(Succeed())
			changed := o.Changed()
			_, err = o.Append(EventNotice, 1)
			Expect(err).To(HaveOccurred())
			Expect(o.Track("j", "pending")).NotTo(Succeed())
			events, _ := o.After(0)
			Expect(events).To(BeEmpty())
			Expect(o.Tracked()).To(BeEmpty())
			Expect(o.Oldest()).To(Equal(uint64(1)), "the number was not used")
			Expect(changed).NotTo(BeClosed())
		})

		It("holds MaxTracked places for parallel starts and gives back a failed one", func() {
			o, _ := OpenOutbox(filepath.Join(dir, "o.json"), time.Hour, clk.Now)
			var wg sync.WaitGroup
			var mu sync.Mutex
			won := 0
			for range MaxTracked + 5 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if o.Reserve() == nil {
						mu.Lock()
						won++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			Expect(won).To(Equal(MaxTracked))
			Expect(o.Reserve()).To(MatchError(ErrTooManyTracked))
			o.Release()
			Expect(o.Reserve()).To(Succeed())
		})

		It("sends the event of a job that is gone once, when two streams see it", func() {
			start(client, `{"task":"ask"}`)
			box, _ := rt.box(client.ID)
			ids := box.Tracked()
			Expect(ids).To(HaveLen(1))
			for id := range ids {
				host.mu.Lock()
				delete(host.status, id)
				host.mu.Unlock()
			}
			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					Expect(rt.refresh(context.Background(), "op", box)).To(Succeed())
				}()
			}
			wg.Wait()
			events, _ := box.After(0)
			failed := 0
			for _, e := range events {
				if e.Type == EventJobFailed {
					failed++
				}
			}
			Expect(failed).To(Equal(1))
			Expect(box.Tracked()).To(BeEmpty())
		})
	})

	Describe("the routes", func() {
		It("does not start more jobs than the outbox can watch, also in parallel", func() {
			var wg sync.WaitGroup
			var mu sync.Mutex
			codes := map[int]int{}
			for range MaxTracked + 6 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					out := start(client, `{"task":"ask"}`)
					mu.Lock()
					codes[out.Status]++
					mu.Unlock()
				}()
			}
			wg.Wait()
			Expect(codes).To(Equal(map[int]int{202: MaxTracked, 429: 6}))
			Expect(host.n).To(Equal(MaxTracked), "no job was started that could not be watched")
		})

		It("answers a cancel of an ended job with 409, and a start refused by the quota with 429", func() {
			host.fail = &contract.HostError{Code: contract.HostConflict}
			out, err := rt.ClientCall(context.Background(), contract.ClientRequest{RouteID: "cancel", Params: map[string]string{"id": "x"}, Client: client})
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Status).To(Equal(409))
			Expect(string(out.Body)).To(ContainSubstring("ended"))
			Expect(start(client, `{"task":"ask"}`).Status).To(Equal(429))
		})

		It("starts a job through the host, with the operation id of the call, and queues progress", func() {
			out := start(client, `{"task":"ask","params":{"q":"hi"}}`)
			Expect(out.Status).To(Equal(202))
			Expect(host.ops).To(Equal([]string{"client.jobs.start"}))
			Expect(host.opIDs).To(ConsistOf("cl-op"))
			box, _ := rt.box(client.ID)
			events, _ := box.After(0)
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(EventJobProgress))
		})

		It("refuses a body with unknown fields and answers the refusals of the host", func() {
			Expect(start(client, `{"task":"ask","user":"bob"}`).Status).To(Equal(400))
			Expect(start(client, `{}`).Status).To(Equal(400))
			host.fail = &contract.HostError{Code: contract.HostConflict}
			Expect(start(client, `{"task":"ask"}`).Status).To(Equal(429))
			host.fail = &contract.HostError{Code: contract.HostNotFound}
			out, err := rt.ClientCall(context.Background(), contract.ClientRequest{RouteID: "job", Params: map[string]string{"id": "x"}, Client: client})
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Status).To(Equal(404))
		})

		It("keeps the outbox of one client apart from another", func() {
			start(client, `{"task":"ask"}`)
			other, _ := rt.box("key-2")
			events, _ := other.After(0)
			Expect(events).To(BeEmpty())
		})

		It("refuses a client id that is not a safe file name", func() {
			for _, id := range []string{"../x", "a/b", "", "a b", strings.Repeat("a", 65)} {
				_, err := rt.ClientCall(context.Background(), contract.ClientRequest{RouteID: "start", Body: []byte(`{"task":"t"}`), Client: contract.ClientIdentity{ID: id}})
				Expect(err).To(HaveOccurred(), id)
			}
		})
	})

	Describe("the stream", func() {
		read := func(ctx context.Context, last string) *bufio.Scanner {
			body, err := rt.ClientStream(ctx, contract.ClientRequest{OperationID: "cl-stream", RouteID: "events", Method: "GET", LastEventID: last, Client: client})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(body.Close)
			return bufio.NewScanner(body)
		}
		next := func(sc *bufio.Scanner) (id, typ string) {
			for sc.Scan() {
				line := sc.Text()
				switch {
				case strings.HasPrefix(line, "id: "):
					id = strings.TrimPrefix(line, "id: ")
				case strings.HasPrefix(line, "event: "):
					typ = strings.TrimPrefix(line, "event: ")
				case line == "":
					return id, typ
				}
			}
			return "", "eof"
		}

		It("sends job.done after a drop, from the outbox, with ids a client can resume from", func() {
			out := start(client, `{"task":"ask"}`)
			var j Job
			Expect(json.Unmarshal(out.Body, &j)).To(Succeed())

			ctx, cancel := context.WithCancel(context.Background())
			sc := read(ctx, "")
			id1, typ := next(sc)
			Expect(typ).To(Equal(EventJobProgress))
			cancel() // the connection drops

			// The job ends while the client is away.
			host.set(j.ID, "completed")

			ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel2()
			sc2 := read(ctx2, id1)
			id2, typ := next(sc2)
			Expect(typ).To(Equal(EventJobDone))
			Expect(id2).NotTo(Equal(id1))

			// The client missed the line of id2 too: it resumes from id1 again
			// and gets the same event, not a lost one.
			sc3 := read(ctx2, id1)
			id3, typ := next(sc3)
			Expect(typ).To(Equal(EventJobDone))
			Expect(id3).To(Equal(id2))
			// From id2 there is nothing new but the quiet.
			Expect(host.opIDs).To(ContainElement("cl-stream"))
		})

		It("says once that events were lost when the client asks from an id that is gone", func() {
			box, _ := rt.box(client.ID)
			for i := range MaxEvents + 5 {
				box.Append(EventNotice, i) //nolint:errcheck
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			sc := read(ctx, "2")
			_, typ := next(sc)
			Expect(typ).To(Equal(EventNotice))
			id, typ := next(sc)
			Expect(typ).To(Equal(EventNotice))
			Expect(id).NotTo(BeEmpty())
		})

		It("says that events were lost when the client asks from an id above the last one", func() {
			box, _ := rt.box(client.ID)
			box.Append(EventNotice, 1) //nolint:errcheck
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			sc := read(ctx, "999")
			_, typ := next(sc)
			Expect(typ).To(Equal(EventNotice))
			// The notice has no id; the next event is the one in the outbox.
			id, typ := next(sc)
			Expect(typ).To(Equal(EventNotice))
			Expect(id).To(Equal("1"))
		})

		It("sends job.done once when two streams of one client are open", func() {
			out := start(client, `{"task":"ask"}`)
			var j Job
			Expect(json.Unmarshal(out.Body, &j)).To(Succeed())
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			a, b := read(ctx, ""), read(ctx, "")
			next(a)
			next(b) // the progress event on both
			host.set(j.ID, "completed")
			// Each stream shows the one event once; the outbox holds it once.
			counts := make([]int, 2)
			var wg sync.WaitGroup
			for i, sc := range []*bufio.Scanner{a, b} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					for {
						_, typ := next(sc)
						if typ == "eof" {
							return
						}
						if typ == EventJobDone {
							counts[i]++
						}
					}
				}()
			}
			time.Sleep(800 * time.Millisecond)
			cancel()
			wg.Wait()
			Expect(counts).To(Equal([]int{1, 1}))
			box, _ := rt.box(client.ID)
			events, _ := box.After(0)
			n := 0
			for _, e := range events {
				if e.Type == EventJobDone {
					n++
				}
			}
			Expect(n).To(Equal(1))
		})

		It("does not repeat the notice while the stream is quiet", func() {
			box, _ := rt.box(client.ID)
			for i := range 3 {
				box.Append(EventNotice, i) //nolint:errcheck
			}
			clk.Add(2 * DefaultTTL) // everything expires
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			sc := read(ctx, "1")
			_, typ := next(sc)
			Expect(typ).To(Equal(EventNotice))
			_, typ = next(sc) // the next thing is the end of the stream, not another notice
			Expect(typ).To(Equal("eof"))
		})

		It("ends when the host closes the stream and refuses a bad Last-Event-ID", func() {
			ctx, cancel := context.WithCancel(context.Background())
			sc := read(ctx, "")
			cancel()
			Eventually(func() string { _, typ := next(sc); return typ }, 2*time.Second).Should(Equal("eof"))
			_, err := rt.ClientStream(context.Background(), contract.ClientRequest{RouteID: "events", LastEventID: "abc", Client: client})
			Expect(err).To(HaveOccurred())
		})
	})
})
