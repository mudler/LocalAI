package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeAttachment is a hold on a carrier that a spec controls.
type fakeAttachment struct {
	connected atomic.Bool
	inflight  atomic.Int32
	failed    atomic.Bool
	closed    atomic.Bool
}

func (a *fakeAttachment) Connected() bool { return a.connected.Load() && !a.closed.Load() }
func (a *fakeAttachment) InFlight() int   { return int(a.inflight.Load()) }
func (a *fakeAttachment) Failed() bool    { return a.failed.Load() }
func (a *fakeAttachment) Close() error    { a.closed.Store(true); return nil }

// rowFrontend answers heartbeats with the row a spec sets, and records what the
// worker reported.
type rowFrontend struct {
	mu      sync.Mutex
	beat    workerregistry.HeartbeatReply
	reports []map[string]any
	asked   []string
}

func (f *rowFrontend) set(b workerregistry.HeartbeatReply) {
	f.mu.Lock()
	f.beat = b
	f.mu.Unlock()
}

func (f *rowFrontend) heartbeat(_ context.Context, body map[string]any) (*workerregistry.HeartbeatReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, body)
	b := f.beat
	return &b, nil
}

func (f *rowFrontend) last() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reports) == 0 {
		return nil
	}
	return f.reports[len(f.reports)-1]
}

func (f *rowFrontend) register(c string) workerregistry.RegisterFunc {
	return func(context.Context) (*workerregistry.RegisterResponse, error) {
		f.mu.Lock()
		f.asked = append(f.asked, c)
		f.mu.Unlock()
		res := &workerregistry.RegisterResponse{ID: "node-1", Status: "healthy", Carrier: f.beat.Carrier, CredentialFor: c}
		if c == "tunnel" {
			res.TunnelToken = "tunnel-token"
		} else {
			res.NatsURL = "nats://handed:4222"
		}
		return res, nil
	}
}

var _ = Describe("A worker that follows the carrier of the cluster", func() {
	var (
		ctx      context.Context
		front    *rowFrontend
		nats     *fakeAttachment
		held     map[cluster.Carrier]*fakeAttachment
		attaches map[cluster.Carrier]*atomic.Int32
		attachFn func(c cluster.Carrier) Attacher
		cannot   map[cluster.Carrier]func(CarrierView) string
		f        *Follower
		failNext map[cluster.Carrier]*atomic.Int32
	)

	row := func(active cluster.Carrier, epoch int64, state, target, draining string) workerregistry.HeartbeatReply {
		return workerregistry.HeartbeatReply{Carrier: string(active), CarrierEpoch: epoch, CarrierState: state, CarrierTarget: target, CarrierDraining: draining}
	}
	attachedNow := func() []cluster.Carrier { return f.Attached() }

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		front = &rowFrontend{}
		front.set(row(cluster.CarrierNATS, 1, "stable", "", ""))
		held = map[cluster.Carrier]*fakeAttachment{}
		attaches = map[cluster.Carrier]*atomic.Int32{cluster.CarrierNATS: {}, cluster.CarrierTunnel: {}}
		failNext = map[cluster.Carrier]*atomic.Int32{cluster.CarrierNATS: {}, cluster.CarrierTunnel: {}}
		cannot = map[cluster.Carrier]func(CarrierView) string{}
		var mu sync.Mutex
		attachFn = func(c cluster.Carrier) Attacher {
			return func(_ context.Context, h Handover) (Attachment, error) {
				attaches[c].Add(1)
				if failNext[c].Load() > 0 {
					failNext[c].Add(-1)
					return nil, errors.New("the server refused the connection")
				}
				if c == cluster.CarrierTunnel {
					Expect(h.Creds.TunnelToken()).To(Equal("tunnel-token"))
				} else {
					Expect(h.NATSURL).To(Equal("nats://handed:4222"))
				}
				a := &fakeAttachment{}
				a.connected.Store(true)
				mu.Lock()
				held[c] = a
				mu.Unlock()
				return a, nil
			}
		}
		nats = &fakeAttachment{}
		nats.connected.Store(true)
	})

	start := func(maxDelay time.Duration) {
		f = NewFollower(FollowerOptions{
			Attachers: map[cluster.Carrier]Attacher{
				cluster.CarrierNATS:   attachFn(cluster.CarrierNATS),
				cluster.CarrierTunnel: attachFn(cluster.CarrierTunnel),
			},
			Credentials: func(c cluster.Carrier) *workerregistry.CredentialManager {
				return workerregistry.NewCredentialManagerFor(front.register(string(c)), false, string(c))
			},
			Heartbeat: front.heartbeat,
			Cannot:    cannot,
			Interval:  100 * time.Millisecond,
			MaxDelay:  maxDelay,
		})
		f.Adopt(cluster.CarrierNATS, nats, 1)
		go f.Run(ctx)
		DeferCleanup(f.Close)
	}

	It("reports the carrier it holds at once, and holds no other while the cluster is stable", func() {
		start(-1)
		Eventually(func() map[string]any { return front.last() }, "3s").ShouldNot(BeNil())
		rep := front.last()
		Expect(rep["attached"]).To(Equal([]string{"nats"}))
		Expect(rep["follow_capabilities"]).To(ContainElement("nats"))
		Consistently(attachedNow, "500ms").Should(Equal([]cluster.Carrier{cluster.CarrierNATS}))
		Expect(attaches[cluster.CarrierTunnel].Load()).To(BeZero())
	})

	It("attaches to the target while a change is prepared, keeps the old carrier, and reports both", func() {
		start(-1)
		front.set(row(cluster.CarrierNATS, 2, "prepare", "tunnel", ""))
		Eventually(attachedNow, "5s").Should(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		Eventually(func() any { return front.last()["attached"] }, "3s").Should(Equal([]string{"nats", "tunnel"}))
		Expect(front.last()["attached_epoch"]).To(BeEquivalentTo(2))
		Expect(nats.closed.Load()).To(BeFalse())
		Expect(front.asked).To(ContainElement("tunnel"))
	})

	It("never changes the active carrier by itself: it attaches to what the frontend names and nothing else", func() {
		start(-1)
		Consistently(func() int32 { return attaches[cluster.CarrierTunnel].Load() }, "700ms").Should(BeZero())
	})

	It("keeps the old carrier through the drain, and releases it when the cluster does, after telling the frontend", func() {
		start(-1)
		front.set(row(cluster.CarrierNATS, 2, "prepare", "tunnel", ""))
		Eventually(attachedNow, "5s").Should(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		front.set(row(cluster.CarrierTunnel, 3, "commit", "nats", "nats"))
		front.set(row(cluster.CarrierTunnel, 4, "stable", "", "nats"))
		Consistently(func() bool { return nats.closed.Load() }, "800ms").Should(BeFalse(), "the drain keeps NATS attached")

		front.set(row(cluster.CarrierTunnel, 5, "stable", "", ""))
		Eventually(func() bool { return nats.closed.Load() }, "5s").Should(BeTrue())
		Eventually(attachedNow, "3s").Should(Equal([]cluster.Carrier{cluster.CarrierTunnel}))
		Eventually(func() any { return front.last()["attached"] }, "3s").Should(Equal([]string{"tunnel"}))
	})

	It("does not close a carrier while a request runs on it, and closes it when the request ends", func() {
		start(-1)
		front.set(row(cluster.CarrierNATS, 2, "prepare", "tunnel", ""))
		Eventually(attachedNow, "5s").Should(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		nats.inflight.Store(1)
		front.set(row(cluster.CarrierTunnel, 3, "stable", "", ""))
		Consistently(func() bool { return nats.closed.Load() }, "1500ms").Should(BeFalse())
		nats.inflight.Store(0)
		Eventually(func() bool { return nats.closed.Load() }, "5s").Should(BeTrue())
	})

	It("drops the target when the change is aborted", func() {
		start(-1)
		front.set(row(cluster.CarrierNATS, 2, "prepare", "tunnel", ""))
		Eventually(attachedNow, "5s").Should(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		front.set(row(cluster.CarrierNATS, 3, "stable", "", ""))
		Eventually(func() bool { return held[cluster.CarrierTunnel].closed.Load() }, "5s").Should(BeTrue())
		Expect(nats.closed.Load()).To(BeFalse())
	})

	It("cannot follow: stays on its carrier after the drain, says why, and does not try", func() {
		cannot[cluster.CarrierTunnel] = func(CarrierView) string { return "no frontend URL that a tunnel can use" }
		start(-1)
		front.set(row(cluster.CarrierTunnel, 3, "stable", "", ""))
		Eventually(func() any { return front.last()["follow_error"] }, "3s").Should(ContainSubstring("no frontend URL that a tunnel can use"))
		front.set(row(cluster.CarrierTunnel, 4, "stable", "", ""))
		Consistently(func() bool { return nats.closed.Load() }, "800ms").Should(BeFalse(), "it keeps the only carrier it has")
		Expect(attachedNow()).To(Equal([]cluster.Carrier{cluster.CarrierNATS}))
		Expect(attaches[cluster.CarrierTunnel].Load()).To(BeZero())
		Expect(front.last()["follow_capabilities"]).To(Equal([]string{"nats"}))
	})

	It("reports a carrier it can use as one it can follow, and one it cannot as an error", func() {
		cannot[cluster.CarrierTunnel] = func(CarrierView) string { return "" }
		cannot[cluster.CarrierNATS] = func(CarrierView) string { return "needs a client certificate" }
		start(-1)
		Eventually(func() any {
			if r := front.last(); r != nil {
				return r["follow_capabilities"]
			}
			return nil
		}, "3s").Should(Equal([]string{"nats", "tunnel"}))
	})

	It("tries again with a wait when the carrier refuses, keeps the old one meanwhile, and says why", func() {
		failNext[cluster.CarrierTunnel].Store(1)
		start(-1)
		front.set(row(cluster.CarrierNATS, 2, "prepare", "tunnel", ""))
		Eventually(func() any { return front.last()["follow_error"] }, "5s").Should(ContainSubstring("refused"))
		Expect(attachedNow()).To(Equal([]cluster.Carrier{cluster.CarrierNATS}))
		Eventually(attachedNow, "10s").Should(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		Eventually(func() any { return front.last()["follow_error"] }, "3s").Should(BeEmpty())
		Expect(attaches[cluster.CarrierTunnel].Load()).To(BeEquivalentTo(2))
	})

	It("leaves a worker alone when the frontend predates carriers", func() {
		front.set(workerregistry.HeartbeatReply{})
		start(-1)
		Consistently(func() int32 { return attaches[cluster.CarrierTunnel].Load() + attaches[cluster.CarrierNATS].Load() }, "700ms").Should(BeZero())
		Expect(nats.closed.Load()).To(BeFalse())
	})

	It("waits a random time within the bound before it attaches", func() {
		start(1500 * time.Millisecond)
		began := time.Now()
		front.set(row(cluster.CarrierNATS, 2, "prepare", "tunnel", ""))
		Eventually(attachedNow, "6s").Should(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		Expect(time.Since(began)).To(BeNumerically("<", 5*time.Second))
	})

	It("attaches again to a carrier that ended for good", func() {
		start(-1)
		front.set(row(cluster.CarrierNATS, 2, "prepare", "tunnel", ""))
		Eventually(attachedNow, "5s").Should(ConsistOf(cluster.CarrierNATS, cluster.CarrierTunnel))
		first := held[cluster.CarrierTunnel]
		first.failed.Store(true)
		Eventually(func() bool { return first.closed.Load() }, "5s").Should(BeTrue())
		Eventually(func() int32 { return attaches[cluster.CarrierTunnel].Load() }, "10s").Should(BeEquivalentTo(2))
	})

	It("reports no carrier as ready when all are down", func() {
		start(-1)
		nats.connected.Store(false)
		Expect(f.Ready()).To(HaveOccurred())
		nats.connected.Store(true)
		Expect(f.Ready()).To(Succeed())
	})
})

var _ = Describe("The requests that run on a control server", func() {
	It("counts a request of the HTTP server while it runs, and not after", func() {
		srv := newHTTPControlServer()
		release := make(chan struct{})
		entered := make(chan struct{})
		Expect(srv.handle(verbBackendList, func(context.Context, []byte) (any, error) {
			close(entered)
			<-release
			return map[string]string{}, nil
		})).To(Succeed())
		ts := httptest.NewServer(srv)
		DeferCleanup(ts.Close)

		done := make(chan struct{})
		go func() {
			defer close(done)
			resp, err := http.Post(ts.URL+workerctl.PathOf(workerctl.VerbBackendList), "application/json", strings.NewReader("{}"))
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		Eventually(entered, "5s").Should(BeClosed())
		Expect(srv.InFlight()).To(Equal(1))
		close(release)
		Eventually(done, "5s").Should(BeClosed())
		Eventually(srv.InFlight, "5s").Should(BeZero())
	})
})
