package carrier_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gorm.io/gorm"
)

// swapSubject is a broadcast subject that every carrier serves.
const swapSubject = "state.swap-spec"

// fakeNet is the network that the replicas of a spec share. There is one bus for
// each carrier, so a message that one replica publishes on a carrier reaches the
// replicas that listen on it.
type fakeNet struct {
	bus map[cluster.Carrier]*testutil.FakeBus
}

func newFakeNet() *fakeNet {
	return &fakeNet{bus: map[cluster.Carrier]*testutil.FakeBus{
		cluster.CarrierNATS:   testutil.NewFakeBus(),
		cluster.CarrierTunnel: testutil.NewFakeBus(),
	}}
}

// life records what the replica did to the sets it built.
type life struct {
	recorder
}

// replicaNode is one frontend replica of a spec: its own pointer, holders,
// swapper and the sets it built.
type replicaNode struct {
	id      string
	net     *fakeNet
	cur     atomic.Pointer[carrier.Set]
	bus     *carrier.Broadcaster
	window  *carrier.Window
	cmds    *carrier.Commands
	queue   *carrier.WorkQueue
	swapper *carrier.Swapper
	life    *life

	mu        sync.Mutex
	builds    map[cluster.Carrier]int
	buildErr  map[cluster.Carrier]error
	sets      map[cluster.Carrier][]*fakeCarrier
	handedTo  []cluster.Carrier
	attached  map[string]carrier.Attachment
	reconnect atomic.Int32
}

func (n *replicaNode) build(_ context.Context, row cluster.CarrierRow, target cluster.Carrier) (*carrier.Set, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.buildErr[target]; err != nil {
		return nil, err
	}
	n.builds[target]++
	return n.newSet(target, row.Epoch), nil
}

// newSet builds a fake set over the shared bus of the carrier. The caller holds
// n.mu.
func (n *replicaNode) newSet(name cluster.Carrier, epoch int64) *carrier.Set {
	f := newFakeCarrier(name, epoch)
	bus := n.net.bus[name]
	f.set.Broadcaster = bus
	f.set.OnReconnect = bus.OnReconnect
	f.set.Start = func(ctx context.Context) (func(), error) {
		n.life.rec("start:" + string(name))
		return func() {
			released := errors.Is(context.Cause(ctx), messaging.ErrCarrierReleased)
			n.life.rec(fmt.Sprintf("stop:%s:released=%t", name, released))
		}, nil
	}
	f.set.Handoff = func(_ context.Context, next *carrier.Set) error {
		n.mu.Lock()
		n.handedTo = append(n.handedTo, next.Name)
		n.mu.Unlock()
		n.life.rec("handoff:" + string(name) + "->" + string(next.Name))
		return nil
	}
	f.set.Close = func() { n.life.rec("close:" + string(name)) }
	n.sets[name] = append(n.sets[name], f)
	return f.set
}

func (n *replicaNode) fake(name cluster.Carrier) *fakeCarrier {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sets[name]) == 0 {
		return nil
	}
	return n.sets[name][len(n.sets[name])-1]
}

func (n *replicaNode) buildCount(name cluster.Carrier) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.builds[name]
}

func (n *replicaNode) failBuild(name cluster.Carrier, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.buildErr[name] = err
}

func (n *replicaNode) active() cluster.Carrier { return n.cur.Load().Name }

var _ = Describe("The swap of a replica", func() {
	var (
		ctx     context.Context
		db      *gorm.DB
		store   *cluster.CarrierStore
		reg     *cluster.Registry
		sw      *cluster.Switch
		net     *fakeNet
		nodes   []*replicaNode
		timings cluster.Timings
	)

	// join starts a replica on the carrier that the row names, as initDistributed does.
	join := func(id string) *replicaNode {
		GinkgoHelper()
		n := &replicaNode{
			id: id, net: net, life: &life{},
			builds: map[cluster.Carrier]int{}, buildErr: map[cluster.Carrier]error{},
			sets: map[cluster.Carrier][]*fakeCarrier{}, attached: map[string]carrier.Attachment{},
		}
		row, err := store.Get(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Register(ctx, id, "v1", 0, "")).To(Succeed())
		n.mu.Lock()
		n.cur.Store(n.newSet(row.Active, row.Epoch))
		n.mu.Unlock()
		n.bus = carrier.NewBroadcaster(&n.cur)
		n.window = carrier.NewWindow(func(_ context.Context, nodeID string) (carrier.Attachment, error) {
			n.mu.Lock()
			defer n.mu.Unlock()
			return n.attached[nodeID], nil
		}, nil)
		n.cmds = carrier.NewCommands(&n.cur)
		n.cmds.UseWindow(n.window)
		n.queue = carrier.NewWorkQueue(&n.cur)
		n.swapper, err = carrier.NewSwapper(carrier.SwapperOptions{
			Cur: &n.cur, Bus: n.bus, Window: n.window, Rows: store,
			Ready: readyReporter{reg: reg, id: id}, Build: n.build,
			Interval: time.Hour, Settle: 50 * time.Millisecond,
		})
		Expect(err).ToNot(HaveOccurred())
		n.bus.OnReconnect(func() { n.reconnect.Add(1) })
		n.swapper.Adopt(ctx)
		nodes = append(nodes, n)
		return n
	}

	row := func() cluster.CarrierRow {
		GinkgoHelper()
		r, err := store.Get(ctx)
		Expect(err).ToNot(HaveOccurred())
		return r
	}
	// poll is one pass of every replica over the row.
	poll := func(which ...*replicaNode) {
		GinkgoHelper()
		if len(which) == 0 {
			which = nodes
		}
		for _, n := range which {
			Expect(n.swapper.Poll(ctx)).To(Succeed(), n.id)
		}
	}
	drive := func() { GinkgoHelper(); Expect(sw.Drive(ctx)).To(Succeed()) }
	// availability makes every live replica report that it can use both carriers.
	availability := func() {
		GinkgoHelper()
		for _, n := range nodes {
			Expect(reg.ReportAvailability(ctx, n.id, map[cluster.Carrier]string{cluster.CarrierNATS: "", cluster.CarrierTunnel: ""})).To(Succeed())
		}
	}
	request := func(target cluster.Carrier) {
		GinkgoHelper()
		availability()
		_, _, err := sw.Request(ctx, cluster.Request{Target: target, By: "admin"})
		Expect(err).ToNot(HaveOccurred())
	}
	// settle runs a whole change with every replica prompt.
	settle := func(target cluster.Carrier) {
		GinkgoHelper()
		request(target)
		poll()
		drive() // commit
		poll()
		drive() // stable, draining
		Expect(row().State).To(Equal(cluster.StateStable))
		Expect(row().Active).To(Equal(target))
	}
	expire := func() {
		GinkgoHelper()
		Expect(db.Exec(`UPDATE cluster_carrier SET draining_until = now() - interval '1 second'`).Error).To(Succeed())
		drive()
		Expect(row().Draining).To(BeEmpty())
		poll()
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		var err error
		store, err = cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = store.Seed(ctx, cluster.CarrierNATS, "seed")
		Expect(err).ToNot(HaveOccurred())
		reg = cluster.NewRegistry(db)
		net = newFakeNet()
		nodes = nil
		timings = cluster.Timings{PrepareTimeout: time.Minute, TransitionWindow: time.Minute, MaxDrain: time.Minute}
		sw, err = cluster.NewSwitch(cluster.SwitchOptions{Store: store, Registry: reg, Timings: func() cluster.Timings { return timings }})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		for _, n := range nodes {
			n.swapper.Close()
		}
	})

	Describe("prepare", func() {
		It("builds the target while the old carrier still serves, listens on both, and reports ready", func() {
			a, b := join("a"), join("b")
			var heardOnNew atomic.Int32
			_, err := a.bus.Subscribe(swapSubject, func([]byte) { heardOnNew.Add(1) })
			Expect(err).ToNot(HaveOccurred())

			request(cluster.CarrierTunnel)
			poll()

			Expect(a.buildCount(cluster.CarrierTunnel)).To(Equal(1))
			Expect(a.active()).To(Equal(cluster.CarrierNATS), "publishing is still on the old carrier")
			in, err := reg.Get(ctx, "a")
			Expect(err).ToNot(HaveOccurred())
			Expect(in.ReadyEpoch).To(Equal(row().Epoch))
			Expect(in.ReadyReason).To(BeEmpty())

			// A replica that has not swapped yet publishes on the new carrier, and the
			// subscription of this one hears it.
			Expect(net.bus[cluster.CarrierTunnel].Publish(swapSubject, "x")).To(Succeed())
			Expect(heardOnNew.Load()).To(Equal(int32(1)))
			_ = b
		})

		It("builds the target once, however many times it polls", func() {
			a := join("a")
			request(cluster.CarrierTunnel)
			poll()
			poll()
			poll()
			Expect(a.buildCount(cluster.CarrierTunnel)).To(Equal(1))
		})

		It("reports why it cannot build the target, and the leader aborts naming it", func() {
			a, b := join("a"), join("b")
			b.failBuild(cluster.CarrierTunnel, errors.New("the LISTEN connection was refused"))
			request(cluster.CarrierTunnel)
			poll()

			in, err := reg.Get(ctx, "b")
			Expect(err).ToNot(HaveOccurred())
			Expect(in.ReadyReason).To(ContainSubstring("the LISTEN connection was refused"))

			drive()
			got := row()
			Expect(got.State).To(Equal(cluster.StateStable))
			Expect(got.Active).To(Equal(cluster.CarrierNATS))
			Expect(got.Note).To(ContainSubstring("b"))
			poll()
			Expect(a.active()).To(Equal(cluster.CarrierNATS))
			Expect(b.active()).To(Equal(cluster.CarrierNATS))
		})

		It("tries again to build the target after a failure", func() {
			b := join("b")
			b.failBuild(cluster.CarrierTunnel, errors.New("not yet"))
			request(cluster.CarrierTunnel)
			poll()
			b.failBuild(cluster.CarrierTunnel, nil)
			poll()
			in, err := reg.Get(ctx, "b")
			Expect(err).ToNot(HaveOccurred())
			Expect(in.ReadyReason).To(BeEmpty())
			Expect(in.ReadyEpoch).To(Equal(row().Epoch))
		})

		It("drops the prepared set when the change is aborted, and keeps the old carrier", func() {
			a := join("a")
			var heard atomic.Int32
			_, err := a.bus.Subscribe(swapSubject, func([]byte) { heard.Add(1) })
			Expect(err).ToNot(HaveOccurred())
			request(cluster.CarrierTunnel)
			poll()
			prepared := a.fake(cluster.CarrierTunnel)

			_, err = sw.Abort(ctx, "admin")
			Expect(err).ToNot(HaveOccurred())
			poll()

			Expect(a.life.seen()).To(ContainElement("close:tunnel"))
			Expect(a.active()).To(Equal(cluster.CarrierNATS))
			Expect(net.bus[cluster.CarrierTunnel].Publish(swapSubject, "x")).To(Succeed())
			Expect(heard.Load()).To(BeZero(), "the subscription is not attached to the prepared set any more")
			Expect(net.bus[cluster.CarrierNATS].Publish(swapSubject, "x")).To(Succeed())
			Expect(heard.Load()).To(Equal(int32(1)))
			Expect(prepared).ToNot(BeNil())
		})
	})

	Describe("commit", func() {
		It("stores the new set, so that publishes go to it, and keeps the old carrier attached", func() {
			a, b := join("a"), join("b")
			var heardOld, heardNew atomic.Int32
			_, err := b.bus.Subscribe(swapSubject, func([]byte) {})
			Expect(err).ToNot(HaveOccurred())
			_, err = net.bus[cluster.CarrierNATS].Subscribe(swapSubject, func([]byte) { heardOld.Add(1) })
			Expect(err).ToNot(HaveOccurred())
			_, err = net.bus[cluster.CarrierTunnel].Subscribe(swapSubject, func([]byte) { heardNew.Add(1) })
			Expect(err).ToNot(HaveOccurred())

			request(cluster.CarrierTunnel)
			poll()
			drive()
			Expect(row().State).To(Equal(cluster.StateCommit))
			poll(a)
			Expect(a.active()).To(Equal(cluster.CarrierTunnel))
			Expect(b.active()).To(Equal(cluster.CarrierNATS), "b has not polled yet")

			Expect(a.bus.Publish(swapSubject, "from-a")).To(Succeed())
			Expect(heardNew.Load()).To(Equal(int32(1)))
			Expect(heardOld.Load()).To(BeZero(), "a publish goes to one carrier only")
			// b has not swapped, publishes on the old carrier, and is heard by a, which
			// still listens there.
			var heardByA atomic.Int32
			_, err = a.bus.Subscribe(swapSubject, func([]byte) { heardByA.Add(1) })
			Expect(err).ToNot(HaveOccurred())
			Expect(b.bus.Publish(swapSubject, "from-b")).To(Succeed())
			Expect(heardByA.Load()).To(Equal(int32(1)))
		})

		It("confirms the commit so the leader can settle", func() {
			a, b := join("a"), join("b")
			request(cluster.CarrierTunnel)
			poll()
			drive()
			commit := row()
			poll()
			for _, n := range []*replicaNode{a, b} {
				in, err := reg.Get(ctx, n.id)
				Expect(err).ToNot(HaveOccurred())
				Expect(in.ReadyEpoch).To(Equal(commit.Epoch), n.id)
			}
			drive()
			Expect(row().State).To(Equal(cluster.StateStable))
			Expect(row().Draining).To(Equal(cluster.CarrierNATS))
		})

		It("delivers every message once to a subscriber while the replicas flip one after the other", func() {
			pub, sub, third := join("pub"), join("sub"), join("third")
			var mu sync.Mutex
			got := map[string]int{}
			_, err := sub.bus.Subscribe(swapSubject, func(raw []byte) {
				mu.Lock()
				got[string(raw)]++
				mu.Unlock()
			})
			Expect(err).ToNot(HaveOccurred())
			sent := 0
			publish := func(from *replicaNode) {
				sent++
				Expect(from.bus.Publish(swapSubject, fmt.Sprintf("m%d", sent))).To(Succeed())
			}

			publish(pub)
			request(cluster.CarrierTunnel)
			poll()
			publish(pub)
			drive()
			publish(pub)
			poll(pub) // pub flips first
			publish(pub)
			publish(sub) // sub still on the old carrier
			poll(sub)
			publish(third)
			poll(third)
			publish(third)
			drive()
			publish(pub)
			expire()
			publish(sub)

			mu.Lock()
			defer mu.Unlock()
			Expect(got).To(HaveLen(sent), "no message is lost")
			for m, n := range got {
				Expect(n).To(Equal(1), "message %s was delivered %d times", m, n)
			}
		})

		It("builds the target at commit for a replica that missed the prepare", func() {
			a := join("a")
			request(cluster.CarrierTunnel)
			poll(a)
			late := join("late") // joins during prepare, on the old carrier
			drive()              // late was not ready, so this is a no-op
			Expect(row().State).To(Equal(cluster.StatePrepare))
			poll(late)
			drive()
			Expect(row().State).To(Equal(cluster.StateCommit))
			Expect(late.buildCount(cluster.CarrierTunnel)).To(Equal(1))
		})

		It("goes straight to the new carrier for a replica that sees the commit and never saw the prepare", func() {
			a := join("a")
			request(cluster.CarrierTunnel)
			poll(a)
			drive()
			commit := row()
			Expect(commit.State).To(Equal(cluster.StateCommit))
			late := join("late") // starts on the carrier that the row names
			Expect(late.active()).To(Equal(cluster.CarrierTunnel))
			poll(late)
			Expect(late.buildCount(cluster.CarrierNATS)).To(BeZero(), "it never builds the old carrier")
			in, err := reg.Get(ctx, "late")
			Expect(err).ToNot(HaveOccurred())
			Expect(in.ReadyEpoch).To(Equal(commit.Epoch))
		})

		It("catches up when it polled too late to see the commit", func() {
			a, b := join("a"), join("b")
			request(cluster.CarrierTunnel)
			poll()
			drive()
			poll(a)
			drive() // settle is not due: b has not confirmed
			Expect(row().State).To(Equal(cluster.StateCommit))
			Expect(db.Exec(`UPDATE cluster_carrier SET state = 'stable', epoch = epoch + 1`).Error).To(Succeed())
			poll(b)
			Expect(b.active()).To(Equal(cluster.CarrierTunnel))
		})

		It("starts the work of the new carrier when it commits, and keeps the old one running through the drain", func() {
			a := join("a")
			request(cluster.CarrierTunnel)
			poll()
			Expect(a.life.seen()).To(ConsistOf("start:nats"), "only the carrier in use runs its work")
			drive()
			poll()
			Expect(a.life.seen()).To(ConsistOf("start:nats", "start:tunnel"))
		})
	})

	Describe("the drain", func() {
		It("keeps the old carrier attached until the leader ends the drain", func() {
			a := join("a")
			var heard atomic.Int32
			_, err := a.bus.Subscribe(swapSubject, func([]byte) { heard.Add(1) })
			Expect(err).ToNot(HaveOccurred())
			settle(cluster.CarrierTunnel)
			poll()

			Expect(net.bus[cluster.CarrierNATS].Publish(swapSubject, "late")).To(Succeed())
			Expect(heard.Load()).To(Equal(int32(1)), "a late publisher on the old carrier is still heard")
			Expect(a.life.seen()).ToNot(ContainElement(ContainSubstring("close:nats")))
		})

		It("releases the old carrier when the drain ends: unsubscribes, stops its work, hands over, closes, and replays the hooks once", func() {
			a := join("a")
			var heard atomic.Int32
			_, err := a.bus.Subscribe(swapSubject, func([]byte) { heard.Add(1) })
			Expect(err).ToNot(HaveOccurred())
			settle(cluster.CarrierTunnel)
			expire()

			Eventually(a.life.seen, "5s").Should(ContainElements(
				"stop:nats:released=true", "handoff:nats->tunnel", "close:nats"))
			Expect(net.bus[cluster.CarrierNATS].Publish(swapSubject, "after")).To(Succeed())
			Expect(heard.Load()).To(BeZero(), "the old carrier is no longer listened on")
			Expect(a.reconnect.Load()).To(Equal(int32(1)), "the consumers read their tables once")

			seen := a.life.seen()
			Expect(slices.Index(seen, "stop:nats:released=true")).To(BeNumerically("<", slices.Index(seen, "handoff:nats->tunnel")),
				"the work is stopped before what it held is handed over")
			Expect(slices.Index(seen, "handoff:nats->tunnel")).To(BeNumerically("<", slices.Index(seen, "close:nats")))
		})

		It("hands over a second time after the producers that lag have had time to flip", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			expire()
			Eventually(func() int {
				n := 0
				for _, s := range a.life.seen() {
					if s == "handoff:nats->tunnel" {
						n++
					}
				}
				return n
			}, "5s", "20ms").Should(Equal(2))
		})

		It("reuses the draining set when the admin switches back during the drain", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			natsBuilds := a.buildCount(cluster.CarrierNATS)
			Expect(natsBuilds).To(Equal(0), "the replica started on NATS and never built it")

			request(cluster.CarrierNATS)
			poll()
			Expect(a.buildCount(cluster.CarrierNATS)).To(Equal(0), "the old set is still attached, so there is nothing to build")
			drive()
			poll()
			Expect(a.active()).To(Equal(cluster.CarrierNATS))
			Expect(a.life.seen()).ToNot(ContainElement("close:nats"))
		})

		It("closes both sets of a replica that shuts down", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			a.swapper.Close()
			Eventually(a.life.seen, "5s").Should(ContainElement("close:nats"))
		})
	})

	Describe("the window", func() {
		It("sends a control request to the carrier that the worker is attached to", func() {
			a := join("a")
			a.attached["on-nats"] = carrier.Attachment{NATS: true}
			a.attached["on-tunnel"] = carrier.Attachment{Tunnel: true}
			a.attached["on-both"] = carrier.Attachment{NATS: true, Tunnel: true}
			settle(cluster.CarrierTunnel)

			Expect(a.cmds.PingNode("on-nats")).To(Succeed())
			Expect(a.cmds.PingNode("on-tunnel")).To(Succeed())
			Expect(a.cmds.PingNode("on-both")).To(Succeed())
			Expect(a.fake(cluster.CarrierNATS).commands.seen()).To(HaveLen(1), "only the worker that stayed on NATS")
			Expect(a.fake(cluster.CarrierTunnel).commands.seen()).To(HaveLen(2), "the worker on the tunnel and the one on both")
		})

		It("reports a worker that is attached to neither carrier as ErrNoRoute, and calls nothing", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			err := a.cmds.PingNode("nowhere")
			Expect(err).To(MatchError(ContainSubstring("no route")))
			Expect(a.fake(cluster.CarrierNATS).commands.seen()).To(BeEmpty())
			Expect(a.fake(cluster.CarrierTunnel).commands.seen()).To(BeEmpty())
		})

		It("uses the active carrier only once the drain is over", func() {
			a := join("a")
			a.attached["on-nats"] = carrier.Attachment{NATS: true}
			settle(cluster.CarrierTunnel)
			expire()
			Eventually(a.life.seen, "5s").Should(ContainElement("close:nats"))

			Expect(a.cmds.PingNode("on-nats")).To(Succeed())
			Expect(a.fake(cluster.CarrierNATS).commands.seen()).To(BeEmpty(), "the closed carrier gets no call")
			Expect(a.fake(cluster.CarrierTunnel).commands.seen()).To(Equal([]string{"PingNode"}), "the active carrier answers for every worker, and its own error says that the worker is not there")
		})

		It("unloads a model on the nodes that each carrier reaches, and reports only what neither could do", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			Expect(a.cmds.UnloadRemoteModel("m")).To(Succeed())
			Expect(a.fake(cluster.CarrierTunnel).commands.seen()).To(ContainElement("UnloadRemoteModelContext"))
			Expect(a.fake(cluster.CarrierNATS).commands.seen()).To(BeEmpty(), "the active carrier did it all")
		})
	})

	Describe("a call that runs while the set changes", func() {
		It("never blocks and never reaches a closed set", func() {
			a, b := join("a"), join("b")
			stop := make(chan struct{})
			var wg sync.WaitGroup
			var calls atomic.Int64
			for range 4 {
				wg.Go(func() {
					for {
						select {
						case <-stop:
							return
						default:
						}
						Expect(a.bus.Publish(swapSubject, "x")).To(Succeed())
						Expect(a.queue.Enqueue(ctx, messaging.WorkMCPCI, "p")).To(Succeed())
						calls.Add(1)
					}
				})
			}
			settle(cluster.CarrierTunnel)
			settle(cluster.CarrierNATS)
			close(stop)
			wg.Wait()
			Expect(calls.Load()).To(BeNumerically(">", 0))
			_ = b
		})
	})

	Describe("the metrics", func() {
		It("report the epoch, the state, the carrier in use, the readiness and how long the drain has to go", func() {
			reader := sdkmetric.NewManualReader()
			meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("spec")
			a := join("a")
			measured, err := carrier.NewSwapper(carrier.SwapperOptions{
				Cur: &a.cur, Bus: a.bus, Window: a.window, Rows: store,
				Ready: readyReporter{reg: reg, id: "a"}, Build: a.build,
				Interval: time.Hour, Meter: meter,
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(measured.Close)

			gauge := func(name, attr string) float64 {
				GinkgoHelper()
				var rm metricdata.ResourceMetrics
				Expect(reader.Collect(ctx, &rm)).To(Succeed())
				for _, sm := range rm.ScopeMetrics {
					for _, m := range sm.Metrics {
						if m.Name != name {
							continue
						}
						switch d := m.Data.(type) {
						case metricdata.Gauge[int64]:
							for _, p := range d.DataPoints {
								if attr == "" || p.Attributes.Len() > 0 && attrValue(p.Attributes, attr) {
									return float64(p.Value)
								}
							}
						case metricdata.Gauge[float64]:
							for _, p := range d.DataPoints {
								return p.Value
							}
						}
					}
				}
				Fail("no data point for " + name + " " + attr)
				return 0
			}

			Expect(measured.Poll(ctx)).To(Succeed())
			Expect(gauge("localai_carrier_epoch", "")).To(BeEquivalentTo(1))
			Expect(gauge("localai_carrier_state", "stable")).To(BeEquivalentTo(1))
			Expect(gauge("localai_carrier_state", "prepare")).To(BeZero())
			Expect(gauge("localai_carrier_active", "nats")).To(BeEquivalentTo(1))
			Expect(gauge("localai_carrier_active", "tunnel")).To(BeZero())

			request(cluster.CarrierTunnel)
			Expect(measured.Poll(ctx)).To(Succeed())
			Expect(gauge("localai_carrier_state", "prepare")).To(BeEquivalentTo(1))
			Expect(gauge("localai_carrier_replica_ready", "")).To(BeEquivalentTo(1))
			drive()
			Expect(measured.Poll(ctx)).To(Succeed())
			Expect(gauge("localai_carrier_state", "commit")).To(BeEquivalentTo(1))
			Expect(gauge("localai_carrier_active", "tunnel")).To(BeEquivalentTo(1))
			ready := func() { Expect(reg.ReportReady(ctx, "a", row().Epoch, "")).To(Succeed()) }
			ready()
			drive()
			Expect(measured.Poll(ctx)).To(Succeed())
			Expect(gauge("localai_carrier_drain_remaining_seconds", "")).To(BeNumerically(">", 0), "the drain has time to go")
		})
	})

	Describe("the polling loop", func() {
		It("follows the row on its own, and wakes at once on a hint", func() {
			a := join("a")
			loopCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			fast, err := carrier.NewSwapper(carrier.SwapperOptions{
				Cur: &a.cur, Bus: a.bus, Window: a.window, Rows: store,
				Ready: readyReporter{reg: reg, id: "a"}, Build: a.build,
				Interval: time.Hour, Settle: 50 * time.Millisecond,
			})
			Expect(err).ToNot(HaveOccurred())
			done := make(chan struct{})
			go func() { fast.Run(loopCtx); close(done) }()
			DeferCleanup(func() { cancel(); <-done })

			request(cluster.CarrierTunnel)
			// The interval is an hour, so only the hint can make it look.
			Eventually(func() int64 {
				fast.Wake()
				in, err := reg.Get(ctx, "a")
				Expect(err).ToNot(HaveOccurred())
				return in.ReadyEpoch
			}, "10s", "50ms").Should(Equal(row().Epoch))
		})
	})
})

// readyReporter writes the readiness of one replica to the instances table, as
// cluster.Membership does.
type readyReporter struct {
	reg *cluster.Registry
	id  string
}

func (r readyReporter) ReportReady(ctx context.Context, epoch int64, reason string) error {
	return r.reg.ReportReady(ctx, r.id, epoch, reason)
}

// attrValue reports whether an attribute set holds the value.
func attrValue(set attribute.Set, want string) bool {
	for _, kv := range set.ToSlice() {
		if kv.Value.AsString() == want {
			return true
		}
	}
	return false
}
