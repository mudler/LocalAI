package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/nodes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeAttachment is an attachment whose state a spec sets.
type fakeAttachment struct {
	connected atomic.Bool
	failed    atomic.Bool
	closed    atomic.Bool
	route     nodes.TunnelRoute
}

func (a *fakeAttachment) Connected() bool                { return a.connected.Load() }
func (a *fakeAttachment) Failed() bool                   { return a.failed.Load() }
func (a *fakeAttachment) Close() error                   { a.closed.Store(true); return nil }
func (a *fakeAttachment) TunnelRoute() nodes.TunnelRoute { return a.route }

// fakeRoutes is the attachers and the credentials of a worker whose routes a
// spec controls.
type fakeRoutes struct {
	mu       sync.Mutex
	fails    map[Route]int // attempts that fail before one works
	attempts map[Route]int
	made     map[Route][]*fakeAttachment
	creds    map[Route]int // registrations of each route
}

func newFakeRoutes() *fakeRoutes {
	return &fakeRoutes{fails: map[Route]int{}, attempts: map[Route]int{}, made: map[Route][]*fakeAttachment{}, creds: map[Route]int{}}
}

func (f *fakeRoutes) attacher(r Route) Attacher {
	return func(_ context.Context, h Handover) (Attachment, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		Expect(h.Route).To(Equal(r))
		f.attempts[r]++
		if f.attempts[r] <= f.fails[r] {
			return nil, errors.New("the " + string(r) + " route is down")
		}
		a := &fakeAttachment{route: nodes.TunnelRoute{Transport: nodes.TunnelWebSocket}}
		a.connected.Store(true)
		f.made[r] = append(f.made[r], a)
		return a, nil
	}
}

func (f *fakeRoutes) last(r Route) *fakeAttachment {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := len(f.made[r]); n > 0 {
		return f.made[r][n-1]
	}
	return nil
}

func (f *fakeRoutes) count(m map[Route]int, r Route) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return m[r]
}

func (f *fakeRoutes) options(routes ...Route) RouteHolderOptions {
	return RouteHolderOptions{
		Routes:    routes,
		Attachers: map[Route]Attacher{RouteNATS: f.attacher(RouteNATS), RouteTunnel: f.attacher(RouteTunnel)},
		Credentials: func(r Route) *workerregistry.CredentialManager {
			return workerregistry.NewCredentialManagerFor(func(context.Context) (*workerregistry.RegisterResponse, error) {
				f.mu.Lock()
				f.creds[r]++
				f.mu.Unlock()
				return &workerregistry.RegisterResponse{ID: "n1", Status: "healthy", TunnelToken: "tok"}, nil
			}, false, string(r))
		},
		Interval: 20 * time.Millisecond,
	}
}

// beats records the heartbeats that a holder sends.
type beats struct {
	mu   sync.Mutex
	last map[string]any
	n    int
}

func (b *beats) send(_ context.Context, body map[string]any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last, b.n = body, b.n+1
	return nil
}

func (b *beats) get() (map[string]any, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last, b.n
}

var _ = Describe("The routes of a worker", func() {
	Describe("which routes a worker holds", func() {
		It("puts the routes in order, once each, and refuses a name that is not a route", func() {
			rs, err := ParseRoutes([]string{"tunnel", " NATS ", "tunnel"})
			Expect(err).ToNot(HaveOccurred())
			Expect(rs).To(Equal([]Route{RouteNATS, RouteTunnel}))
			_, err = ParseRoutes([]string{"carrier-pigeon"})
			Expect(err).To(MatchError(ContainSubstring("unknown worker route")))
		})

		DescribeTable("follows the flags of the operator",
			func(cfg Config, want []Route) {
				got, err := cfg.routes()
				Expect(err).ToNot(HaveOccurred())
				Expect(got).To(Equal(want))
			},
			Entry("NATS and a frontend URL: both, NATS first", Config{RegisterTo: "http://f", NatsURL: "nats://n:4222"}, []Route{RouteNATS, RouteTunnel}),
			Entry("a frontend URL only: the websocket tunnel", Config{RegisterTo: "http://f"}, []Route{RouteTunnel}),
			Entry("an invite: the P2P tunnel", Config{Join: "localai://pair?x"}, []Route{RouteTunnel}),
			Entry("an invite and a NATS URL: still the P2P tunnel only", Config{Join: "localai://pair?x", NatsURL: "nats://n:4222"}, []Route{RouteTunnel}),
			Entry("--routes nats: NATS only", Config{RegisterTo: "http://f", NatsURL: "nats://n:4222", Routes: []string{"nats"}}, []Route{RouteNATS}),
			Entry("--routes tunnel: the tunnel only, whatever the NATS URL", Config{RegisterTo: "http://f", NatsURL: "nats://n:4222", Routes: []string{"tunnel"}}, []Route{RouteTunnel}),
			Entry("--routes nats with no URL: NATS from the address the frontend hands over", Config{RegisterTo: "http://f", Routes: []string{"nats"}}, []Route{RouteNATS}),
		)

		It("refuses --routes that names nothing, or NATS for a worker that joins over P2P", func() {
			_, err := Config{RegisterTo: "http://f", Routes: []string{" "}}.routes()
			Expect(err).To(HaveOccurred())
			_, err = Config{Join: "localai://pair?x", Routes: []string{"nats"}}.routes()
			Expect(err).To(HaveOccurred())
		})

		It("binds loopback only when no NATS route and no address let a frontend dial it", func() {
			Expect((&Config{}).bindHost(false)).To(Equal(loopbackHost))
			Expect((&Config{}).bindHost(true)).To(Equal("0.0.0.0"))
			Expect((&Config{Addr: "10.0.0.1:50051"}).bindHost(false)).To(Equal("0.0.0.0"))
		})
	})

	Describe("the route holder", func() {
		var (
			ctx    context.Context
			cancel context.CancelFunc
		)
		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			DeferCleanup(cancel)
		})
		run := func(h *RouteHolder) {
			done := make(chan struct{})
			go func() { defer close(done); h.Run(ctx) }()
			DeferCleanup(func() {
				cancel()
				Eventually(done, "5s").Should(BeClosed())
				h.Close()
			})
		}

		DescribeTable("holds every route it was given, and reports them",
			func(routes []Route, transport string) {
				f := newFakeRoutes()
				b := &beats{}
				o := f.options(routes...)
				o.Heartbeat = b.send
				h := NewRouteHolder(o)
				run(h)
				Eventually(h.Held, "5s").Should(Equal(routes))
				Expect(h.Ready()).To(Succeed())
				names := make([]string, 0, len(routes))
				for _, r := range routes {
					names = append(names, string(r))
				}
				Eventually(func() any { last, _ := b.get(); return last["attached"] }, "5s").Should(Equal(names))
				last, _ := b.get()
				Expect(last["routes"]).To(Equal(names))
				Expect(last["route_error"]).To(BeEmpty())
				if transport == "" {
					Expect(last).ToNot(HaveKey("tunnel_transport"))
				} else {
					Expect(last["tunnel_transport"]).To(Equal(transport))
				}
				for _, r := range routes {
					Expect(f.count(f.creds, r)).To(Equal(1), "one registration for the credential of %s", r)
				}
			},
			Entry("NATS only", []Route{RouteNATS}, ""),
			Entry("the tunnel only", []Route{RouteTunnel}, nodes.TunnelWebSocket),
			Entry("NATS and the tunnel", []Route{RouteNATS, RouteTunnel}, nodes.TunnelWebSocket),
		)

		It("retries a route that cannot be attached in the background, and serves on the others meanwhile", func() {
			f := newFakeRoutes()
			f.fails[RouteNATS] = 1
			b := &beats{}
			o := f.options(RouteNATS, RouteTunnel)
			o.Heartbeat = b.send
			h := NewRouteHolder(o)
			run(h)

			Eventually(h.Held, "2s").Should(Equal([]Route{RouteTunnel}), "the tunnel does not wait for NATS")
			Eventually(func() any { last, _ := b.get(); return last["route_error"] }, "2s").Should(And(HavePrefix("nats: "), ContainSubstring("the nats route is down")))
			last, _ := b.get()
			Expect(last["attached"]).To(Equal([]string{"tunnel"}))
			Expect(last["routes"]).To(Equal([]string{"nats", "tunnel"}))

			Eventually(h.Held, "5s").Should(Equal([]Route{RouteNATS, RouteTunnel}))
			Expect(f.count(f.attempts, RouteNATS)).To(Equal(2))
			Expect(f.count(f.attempts, RouteTunnel)).To(Equal(1), "the tunnel was attached once")
		})

		It("reports a route that is attached and not connected as not held", func() {
			f := newFakeRoutes()
			h := NewRouteHolder(f.options(RouteNATS, RouteTunnel))
			run(h)
			Eventually(h.Held, "2s").Should(HaveLen(2))
			f.last(RouteNATS).connected.Store(false)
			Expect(h.Held()).To(Equal([]Route{RouteTunnel}))
			Expect(h.Report()["route_error"]).To(Equal("nats: connecting"))
			f.last(RouteTunnel).connected.Store(false)
			Expect(h.Ready()).To(MatchError(ContainSubstring("no route of this worker is connected")))
		})

		It("attaches a route again when its attachment ends for good", func() {
			f := newFakeRoutes()
			h := NewRouteHolder(f.options(RouteNATS))
			run(h)
			Eventually(h.Held, "2s").Should(Equal([]Route{RouteNATS}))
			first := f.last(RouteNATS)
			first.failed.Store(true)
			Eventually(func() int { return f.count(f.attempts, RouteNATS) }, "5s").Should(Equal(2))
			Expect(first.closed.Load()).To(BeTrue())
			Eventually(h.Held, "2s").Should(Equal([]Route{RouteNATS}))
			Expect(f.count(f.creds, RouteNATS)).To(Equal(2), "a new attachment asks for a new credential")
		})

		It("sends no heartbeat while no route is connected", func() {
			f := newFakeRoutes()
			f.fails[RouteTunnel] = 1000
			b := &beats{}
			o := f.options(RouteTunnel)
			o.Heartbeat = b.send
			run(NewRouteHolder(o))
			Consistently(func() int { _, n := b.get(); return n }, "300ms").Should(BeZero())
		})
	})

	Describe("the registrations of the routes", func() {
		It("asks for the credential of one route at a time, and for none for the identity of the node", func() {
			var (
				mu    sync.Mutex
				asked [][]string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Routes []string `json:"routes"`
				}
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				mu.Lock()
				asked = append(asked, body.Routes)
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "n1"})
			}))
			DeferCleanup(srv.Close)
			client := &workerregistry.RegistrationClient{FrontendURL: srv.URL}
			body := func() map[string]any { return map[string]any{"name": "w1"} }
			for _, rs := range [][]Route{nil, {RouteNATS}, {RouteTunnel}} {
				_, err := registerFor(client, body, rs...)(context.Background())
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(asked).To(Equal([][]string{{}, {"nats"}, {"tunnel"}}))
		})
	})
})
