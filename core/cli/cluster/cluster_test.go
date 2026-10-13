// SPDX-License-Identifier: MIT
package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClusterCLI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Cluster CLI") }

// fakeServer records the requests of a command and answers from a table.
type fakeServer struct {
	mu       sync.Mutex
	requests []recorded
	handler  func(r *http.Request, body map[string]any) (int, any)
	srv      *httptest.Server
}

type recorded struct {
	Method, Path, Auth string
	Body               map[string]any
}

func newFakeServer(h func(r *http.Request, body map[string]any) (int, any)) *fakeServer {
	f := &fakeServer{handler: h}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), body})
		f.mu.Unlock()
		code, resp := f.handler(r, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return f
}

func (f *fakeServer) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakeServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func remote(f *fakeServer) Remote {
	return Remote{Endpoint: f.srv.URL, APIKey: "k", Timeout: 5 * time.Second}
}

var preflightOK = map[string]any{
	"active": "nats", "epoch": 3, "state": "stable", "ok": true,
	"replicas":  []map[string]any{{"id": "r1", "version": "v1", "ready_epoch": 3}, {"id": "r2", "version": "v1", "ready_epoch": 3}},
	"workers":   []map[string]any{{"id": "w1", "name": "gpu", "attached": []string{"nats"}, "can_follow": true}},
	"in_flight": map[string]any{"loads": 1},
}

var preflightBlocked = map[string]any{
	"active": "nats", "epoch": 3, "state": "stable", "ok": false,
	"blockers": []map[string]any{{"kind": "worker", "id": "w2", "reason": "no routable address", "forceable": true}},
	"replicas": []map[string]any{{"id": "r1", "version": "v1", "ready_epoch": 3}},
	"workers":  []map[string]any{{"id": "w2", "name": "edge", "attached": []string{"nats"}, "can_follow": false, "reason": "no routable address", "follow_error": "needs address"}},
}

var _ = Describe("local-ai cluster", func() {
	var out bytes.Buffer
	BeforeEach(func() { out.Reset() })

	Describe("parsing", func() {
		It("reads the subcommands and their flags", func() {
			var c struct {
				Cluster Command `cmd:""`
			}
			parser, err := kong.New(&c)
			Expect(err).NotTo(HaveOccurred())

			ctx, err := parser.Parse([]string{"cluster", "carrier", "switch", "--to", "tunnel", "--dry-run", "--force", "--yes", "--wait"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ctx.Command()).To(Equal("cluster carrier switch"))
			sw := c.Cluster.Carrier.Switch
			Expect(sw.To).To(Equal("tunnel"))
			Expect(sw.DryRun && sw.Force && sw.Yes && sw.Wait).To(BeTrue())
			Expect(sw.Endpoint).To(Equal("http://127.0.0.1:8080"))

			_, err = parser.Parse([]string{"cluster", "carrier", "switch", "--to", "kafka"})
			Expect(err).To(HaveOccurred())
			var bare struct {
				Cluster Command `cmd:""`
			}
			bareParser, err := kong.New(&bare)
			Expect(err).NotTo(HaveOccurred())
			_, err = bareParser.Parse([]string{"cluster", "carrier", "switch"})
			Expect(err).To(HaveOccurred())

			ctx, err = parser.Parse([]string{"cluster", "carrier", "abort"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ctx.Command()).To(Equal("cluster carrier abort"))

			ctx, err = parser.Parse([]string{"cluster", "carrier"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ctx.Command()).To(Equal("cluster carrier status"))
		})

		It("tells an absent setting from an empty one", func() {
			var c struct {
				Cluster Command `cmd:""`
			}
			parser, err := kong.New(&c)
			Expect(err).NotTo(HaveOccurred())
			_, err = parser.Parse([]string{"cluster", "settings", "set", "--nats-url=", "--max-drain", "20m"})
			Expect(err).NotTo(HaveOccurred())
			set := c.Cluster.Settings.Set
			Expect(set.NATSURL).NotTo(BeNil())
			Expect(*set.NATSURL).To(BeEmpty())
			Expect(set.NATSWorkerURL).To(BeNil())
			Expect(*set.MaxDrain).To(Equal("20m"))
		})
	})

	Describe("carrier status", func() {
		It("prints the carrier, the replicas and the workers, and sends the key", func() {
			f := newFakeServer(func(r *http.Request, _ map[string]any) (int, any) {
				return 200, map[string]any{
					"active": "tunnel", "epoch": 9, "state": "stable",
					"row":                map[string]any{"draining": "nats"},
					"drain_remaining_ns": int64(90 * time.Second),
					"replicas":           preflightOK["replicas"], "workers": preflightBlocked["workers"],
				}
			})
			defer f.srv.Close()
			c := &StatusCommand{Remote: remote(f)}
			Expect(c.run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Method).To(Equal("GET"))
			Expect(f.last().Path).To(Equal("/api/cluster/carrier"))
			Expect(f.last().Auth).To(Equal("Bearer k"))
			s := out.String()
			Expect(s).To(ContainSubstring("Active carrier: tunnel (epoch 9)"))
			Expect(s).To(ContainSubstring("Draining:       nats, 1m30s left"))
			Expect(s).To(ContainSubstring("Replicas (2)"))
			Expect(s).To(ContainSubstring("follow_error: needs address"))
		})

		It("writes the JSON of the server with --json", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) { return 200, preflightOK })
			defer f.srv.Close()
			r := remote(f)
			r.JSON = true
			Expect((&StatusCommand{Remote: r}).run(context.Background(), &out)).To(Succeed())
			var got map[string]any
			Expect(json.Unmarshal(out.Bytes(), &got)).To(Succeed())
			Expect(got["active"]).To(Equal("nats"))
		})

		It("explains a refused key and a frontend that is not distributed", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) { return 401, map[string]any{"error": "no"} })
			err := (&StatusCommand{Remote: remote(f)}).run(context.Background(), &out)
			f.srv.Close()
			Expect(err).To(MatchError(ContainSubstring("--api-key")))

			f = newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 503, map[string]any{"error": "distributed mode is not enabled on this frontend"}
			})
			defer f.srv.Close()
			err = (&StatusCommand{Remote: remote(f)}).run(context.Background(), &out)
			Expect(err).To(MatchError(ContainSubstring("not running in distributed mode")))
		})

		It("refuses an endpoint that is not a plain HTTP URL", func() {
			err := (&StatusCommand{Remote: Remote{Endpoint: "http://u:p@host:1"}}).run(context.Background(), &out)
			Expect(err).To(MatchError(ContainSubstring("without credentials")))
		})
	})

	Describe("carrier switch", func() {
		It("only runs the preflight with --dry-run", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) { return 200, preflightOK })
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", DryRun: true, Remote: remote(f)}
			Expect(c.run(context.Background(), &out, strings.NewReader(""), false)).To(Succeed())
			Expect(f.count()).To(Equal(1))
			Expect(f.last().Body).To(HaveKeyWithValue("dry_run", true))
			Expect(f.last().Body).To(HaveKeyWithValue("target", "tunnel"))
			Expect(out.String()).To(ContainSubstring("Nothing blocks the change."))
			Expect(out.String()).To(ContainSubstring("Check that this list is complete."))
		})

		It("fails a dry run that has a blocker and lists it", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) { return 200, preflightBlocked })
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", DryRun: true, Remote: remote(f)}
			err := c.run(context.Background(), &out, strings.NewReader(""), false)
			Expect(err).To(MatchError(ContainSubstring("blocked")))
			Expect(out.String()).To(ContainSubstring("BLOCKER worker w2: no routable address (can be forced)"))
		})

		It("refuses to start a change without a confirmation when there is no terminal", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) { return 200, preflightOK })
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", Remote: remote(f)}
			err := c.run(context.Background(), &out, strings.NewReader(""), false)
			Expect(err).To(MatchError(ContainSubstring("--yes")))
			Expect(f.count()).To(Equal(1), "only the dry run was sent")
		})

		It("does not start a change when the answer to the question is no", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) { return 200, preflightOK })
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", Remote: remote(f)}
			err := c.run(context.Background(), &out, strings.NewReader("n\n"), true)
			Expect(err).To(MatchError(ContainSubstring("nothing was changed")))
			Expect(f.count()).To(Equal(1))
			Expect(out.String()).To(ContainSubstring("Are these 2 replicas all the frontends"))
		})

		It("starts the change after a yes at the question", func() {
			f := newFakeServer(func(_ *http.Request, b map[string]any) (int, any) {
				if b["dry_run"] == true {
					return 200, preflightOK
				}
				return 202, map[string]any{"state": "prepare", "active": "nats", "target": "tunnel", "epoch": 4}
			})
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", Remote: remote(f)}
			Expect(c.run(context.Background(), &out, strings.NewReader("yes\n"), true)).To(Succeed())
			Expect(f.last().Body).To(HaveKeyWithValue("target", "tunnel"))
			Expect(f.last().Body).To(HaveKeyWithValue("force", false))
			Expect(f.last().Body).NotTo(HaveKey("dry_run"))
			Expect(out.String()).To(ContainSubstring("Change to tunnel started (epoch 4, state prepare)."))
		})

		It("stops at a blocker unless --force, and sends force when given", func() {
			var forced bool
			f := newFakeServer(func(_ *http.Request, b map[string]any) (int, any) {
				if b["dry_run"] == true {
					return 200, preflightBlocked
				}
				forced = b["force"] == true
				return 202, map[string]any{"state": "prepare", "active": "nats", "target": "tunnel", "epoch": 4}
			})
			defer f.srv.Close()
			err := (&SwitchCommand{To: "tunnel", Yes: true, Remote: remote(f)}).run(context.Background(), &out, strings.NewReader(""), false)
			Expect(err).To(MatchError(ContainSubstring("blocked")))
			Expect(f.count()).To(Equal(1))

			Expect((&SwitchCommand{To: "tunnel", Yes: true, Force: true, Remote: remote(f)}).run(context.Background(), &out, strings.NewReader(""), false)).To(Succeed())
			Expect(forced).To(BeTrue())
		})

		It("shows the blockers of a 422 and the busy state of a 409", func() {
			f := newFakeServer(func(_ *http.Request, b map[string]any) (int, any) {
				if b["dry_run"] == true {
					return 200, preflightOK
				}
				return 422, map[string]any{"error": "blocked", "blockers": []map[string]any{{"kind": "replica", "id": "r9", "reason": "not ready"}}}
			})
			err := (&SwitchCommand{To: "tunnel", Yes: true, Remote: remote(f)}).run(context.Background(), &out, strings.NewReader(""), false)
			f.srv.Close()
			Expect(err).To(MatchError(ContainSubstring("replica r9: not ready")))

			f = newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 409, map[string]any{"error": "a change is under way"}
			})
			defer f.srv.Close()
			err = (&SwitchCommand{To: "tunnel", DryRun: true, Remote: remote(f)}).run(context.Background(), &out, strings.NewReader(""), false)
			Expect(err).To(MatchError(ContainSubstring("a change is under way")))
		})

		It("waits for the commit with --wait", func() {
			old := pollInterval
			pollInterval = time.Millisecond
			DeferCleanup(func() { pollInterval = old })
			var polls int
			f := newFakeServer(func(r *http.Request, b map[string]any) (int, any) {
				switch {
				case r.Method == http.MethodGet:
					polls++
					if polls < 3 {
						return 200, map[string]any{"active": "nats", "state": "prepare", "epoch": 4}
					}
					return 200, map[string]any{"active": "tunnel", "state": "stable", "epoch": 6, "drain_remaining_ns": int64(15 * time.Minute)}
				case b["dry_run"] == true:
					return 200, preflightOK
				}
				return 202, map[string]any{"state": "prepare", "active": "nats", "target": "tunnel", "epoch": 4}
			})
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", Yes: true, Wait: true, WaitTimeout: 5 * time.Second, Remote: remote(f)}
			Expect(c.run(context.Background(), &out, strings.NewReader(""), false)).To(Succeed())
			Expect(out.String()).To(ContainSubstring("The cluster is on tunnel."))
			Expect(out.String()).To(ContainSubstring("drains for 15m0s more"))
		})

		It("reports an abort that --wait sees", func() {
			old := pollInterval
			pollInterval = time.Millisecond
			DeferCleanup(func() { pollInterval = old })
			f := newFakeServer(func(r *http.Request, b map[string]any) (int, any) {
				switch {
				case r.Method == http.MethodGet:
					return 200, map[string]any{"active": "nats", "state": "stable", "epoch": 5, "row": map[string]any{"note": "replica r2 not ready"}}
				case b["dry_run"] == true:
					return 200, preflightOK
				}
				return 202, map[string]any{"state": "prepare", "active": "nats", "target": "tunnel", "epoch": 4}
			})
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", Yes: true, Wait: true, WaitTimeout: 5 * time.Second, Remote: remote(f)}
			err := c.run(context.Background(), &out, strings.NewReader(""), false)
			Expect(err).To(MatchError(ContainSubstring("aborted")))
			Expect(err).To(MatchError(ContainSubstring("replica r2 not ready")))
		})

		It("gives up waiting when the time is out", func() {
			old := pollInterval
			pollInterval = time.Millisecond
			DeferCleanup(func() { pollInterval = old })
			f := newFakeServer(func(r *http.Request, b map[string]any) (int, any) {
				switch {
				case r.Method == http.MethodGet:
					return 200, map[string]any{"active": "nats", "state": "prepare", "epoch": 4}
				case b["dry_run"] == true:
					return 200, preflightOK
				}
				return 202, map[string]any{"state": "prepare", "active": "nats", "target": "tunnel", "epoch": 4}
			})
			defer f.srv.Close()
			c := &SwitchCommand{To: "tunnel", Yes: true, Wait: true, WaitTimeout: 30 * time.Millisecond, Remote: remote(f)}
			Expect(c.run(context.Background(), &out, strings.NewReader(""), false)).To(MatchError(ContainSubstring("gave up waiting")))
		})
	})

	Describe("carrier abort", func() {
		It("posts abort and says where the cluster stays", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"active": "nats", "epoch": 8, "state": "stable"}
			})
			defer f.srv.Close()
			Expect((&AbortCommand{Remote: remote(f)}).run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Body).To(HaveKeyWithValue("abort", true))
			Expect(out.String()).To(ContainSubstring("The cluster stays on nats (epoch 8)."))
		})

		It("passes on the 409 of a change that cannot be aborted", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 409, map[string]any{"error": "nothing to abort"}
			})
			defer f.srv.Close()
			Expect((&AbortCommand{Remote: remote(f)}).run(context.Background(), &out)).To(MatchError(ContainSubstring("nothing to abort")))
		})
	})

	Describe("settings", func() {
		It("shows the settings", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"nats_url": "nats://n:4222", "max_drain": "15m"}
			})
			defer f.srv.Close()
			Expect((&GetSettingsCommand{Remote: remote(f)}).run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Path).To(Equal("/api/cluster/settings"))
			Expect(out.String()).To(ContainSubstring("nats_url:          nats://n:4222"))
			Expect(out.String()).To(ContainSubstring("nats_worker_url:   (not set)"))
		})

		It("sends only the flags that are given, and says the carrier did not change", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"saved": true, "nats": map[string]any{"reachable": true}, "settings": map[string]any{"nats_url": "nats://n:4222"}}
			})
			defer f.srv.Close()
			url := "nats://n:4222"
			c := &SetSettingsCommand{NATSURL: &url, Remote: remote(f)}
			Expect(c.run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Method).To(Equal("PUT"))
			Expect(f.last().Body).To(Equal(map[string]any{"nats_url": url}))
			Expect(out.String()).To(ContainSubstring("reaches the NATS server"))
			Expect(out.String()).To(ContainSubstring("The carrier did not change."))
		})

		It("sends an empty value to clear a setting", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"saved": true, "settings": map[string]any{}}
			})
			defer f.srv.Close()
			empty := ""
			Expect((&SetSettingsCommand{NATSWorkerURL: &empty, Remote: remote(f)}).run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Body).To(Equal(map[string]any{"nats_worker_url": ""}))
		})

		It("refuses a call with nothing to store, and shows a refused address", func() {
			Expect((&SetSettingsCommand{}).run(context.Background(), io.Discard)).To(MatchError(ContainSubstring("at least one setting")))

			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 422, map[string]any{"error": "nats_url: this replica cannot reach the NATS server at nats://x:1"}
			})
			defer f.srv.Close()
			u := "nats://x:1"
			Expect((&SetSettingsCommand{NATSURL: &u, Remote: remote(f)}).run(context.Background(), &out)).To(MatchError(ContainSubstring("cannot reach the NATS server")))
		})
	})
})
