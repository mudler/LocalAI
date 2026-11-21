package carrier_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	nodesvc "github.com/mudler/LocalAI/core/services/nodes"
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

// fakeNet is the network that the replicas of a spec share: one floor for the
// broadcasts, so a message that one replica publishes reaches the replicas that
// listen on it.
type fakeNet struct {
	floor *testutil.FakeBus
}

func newFakeNet() *fakeNet {
	return &fakeNet{floor: testutil.NewFakeBus()}
}

// errUsedAfterClose is what a fake carrier answers once its set is closed. A real
// carrier answers with an error or a panic then, and a spec about "never reaches
// a closed set" must be able to see that.
var errUsedAfterClose = errors.New("the carrier was used after it was closed")

// routeSpy records whether the swapper attached the route of a set before the
// pointer named the set.
type routeSpy struct {
	*carrier.RouteTable
	n *replicaNode
}

func (w routeSpy) Attach(r *carrier.WorkerRoute) {
	if cur := w.n.cur.Load(); cur != nil && cur.WorkerRoute != r {
		w.n.windowRec.rec("attach:before-the-pointer-moved")
	} else {
		w.n.windowRec.rec("attach:after-the-pointer-moved")
	}
	w.RouteTable.Attach(r)
}

// att is the routes of a worker attached to NATS, to the tunnel, or to both.
func att(nats, tun bool) []nodesvc.Route {
	var r []nodesvc.Route
	if nats {
		r = append(r, nodesvc.Route{Kind: nodesvc.RouteDirect})
	}
	if tun {
		r = append(r, nodesvc.Route{Kind: nodesvc.RouteTunnel})
	}
	return r
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
	routes  *carrier.RouteTable
	cmds    *carrier.Commands
	swapper *carrier.Swapper
	life    *life

	mu        sync.Mutex
	builds    map[cluster.Carrier]int
	buildErr  map[cluster.Carrier]error
	sets      map[cluster.Carrier][]*fakeCarrier
	attached  map[string][]nodesvc.Route
	reconnect atomic.Int32
	windowRec recorder
}

func (n *replicaNode) build(_ context.Context, row cluster.CarrierRow, target cluster.Carrier) (*carrier.Set, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.buildErr[target]; err != nil {
		return nil, err
	}
	n.builds[target]++
	set := n.newSet(target, row.Epoch)
	if target == cluster.CarrierNATS {
		set.NATSURL = row.NATSURL
	}
	return set, nil
}

// newSet builds a fake set over the shared bus of the carrier. The caller holds
// n.mu.
func (n *replicaNode) newSet(name cluster.Carrier, epoch int64) *carrier.Set {
	f := newFakeCarrier(name, epoch)
	closed := &atomic.Bool{}
	f.commands.closed = closed
	f.set.Close = func() { closed.Store(true); n.life.rec("close:" + string(name)) }
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
		// settleFor is how long a released set stays open.
		settleFor time.Duration
	)

	// join starts a replica on the carrier that the row names, as initDistributed does.
	join := func(id string) *replicaNode {
		GinkgoHelper()
		n := &replicaNode{
			id: id, net: net, life: &life{},
			builds: map[cluster.Carrier]int{}, buildErr: map[cluster.Carrier]error{},
			sets: map[cluster.Carrier][]*fakeCarrier{}, attached: map[string][]nodesvc.Route{},
		}
		row, err := store.Get(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Register(ctx, id, "v1", 0, "")).To(Succeed())
		n.mu.Lock()
		n.cur.Store(n.newSet(row.Active, row.Epoch))
		n.mu.Unlock()
		n.bus, err = carrier.NewBroadcaster(carrier.BroadcasterOptions{Floor: fakeFloor(net.floor)})
		Expect(err).ToNot(HaveOccurred())
		n.routes = carrier.NewRouteTable(func(_ context.Context, nodeID string) ([]nodesvc.Route, error) {
			n.mu.Lock()
			defer n.mu.Unlock()
			return n.attached[nodeID], nil
		})
		n.routes.Attach(n.cur.Load().WorkerRoute)
		n.cmds = carrier.NewCommands(n.routes)
		n.swapper, err = carrier.NewSwapper(carrier.SwapperOptions{
			Cur: &n.cur, Routes: routeSpy{n.routes, n}, Rows: store,
			Ready: readyReporter{reg: reg, id: id}, Build: n.build,
			Interval: time.Hour, Settle: settleFor,
		})
		Expect(err).ToNot(HaveOccurred())
		n.bus.OnReconnect(func() { n.reconnect.Add(1) })
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
		settleFor = 50 * time.Millisecond
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
		It("builds again when the row names another NATS address than the set it holds was built from", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			row0 := row()
			_, err := store.Transition(ctx, row0.Epoch, cluster.Change{
				Active: cluster.CarrierTunnel, State: cluster.StatePrepare, Target: cluster.CarrierNATS,
				Draining: row0.Draining, DrainingUntil: row0.DrainingUntil,
				By: "admin", NATSURL: "nats://broker-one:4222",
			})
			Expect(err).ToNot(HaveOccurred())
			poll()
			Expect(a.buildCount(cluster.CarrierNATS)).To(Equal(1))
			first := a.fake(cluster.CarrierNATS)

			// Same address, new epoch: the set is reused.
			Expect(a.swapper.Reconcile(ctx, row())).To(Succeed())
			Expect(a.buildCount(cluster.CarrierNATS)).To(Equal(1))

			// Another replica saw another address: the row decides.
			_, err = store.Transition(ctx, row().Epoch, cluster.Change{
				Active: cluster.CarrierTunnel, State: cluster.StatePrepare, Target: cluster.CarrierNATS,
				Draining: row0.Draining, DrainingUntil: row0.DrainingUntil,
				By: "admin", NATSURL: "nats://broker-two:4222",
			})
			Expect(err).ToNot(HaveOccurred())
			poll()
			Expect(a.buildCount(cluster.CarrierNATS)).To(Equal(2), "a set built from the old address is not reused")
			Expect(a.fake(cluster.CarrierNATS)).ToNot(BeIdenticalTo(first))
			Expect(a.life.seen()).To(ContainElement("close:nats"), "the set built from the old address is closed")
		})

		It("builds the target while the old carrier still serves, and reports ready", func() {
			a, b := join("a"), join("b")

			request(cluster.CarrierTunnel)
			poll()

			Expect(a.buildCount(cluster.CarrierTunnel)).To(Equal(1))
			Expect(a.active()).To(Equal(cluster.CarrierNATS), "new calls still go on the old carrier")
			in, err := reg.Get(ctx, "a")
			Expect(err).ToNot(HaveOccurred())
			Expect(in.ReadyEpoch).To(Equal(row().Epoch))
			Expect(in.ReadyReason).To(BeEmpty())

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
			request(cluster.CarrierTunnel)
			poll()
			prepared := a.fake(cluster.CarrierTunnel)

			_, err := sw.Abort(ctx, "admin")
			Expect(err).ToNot(HaveOccurred())
			poll()

			Expect(a.life.seen()).To(ContainElement("close:tunnel"))
			Expect(a.active()).To(Equal(cluster.CarrierNATS))
			Expect(prepared).ToNot(BeNil())
		})
	})

	Describe("commit", func() {
		It("stores the new set, so that new calls go to it, and keeps the old carrier attached", func() {
			a, b := join("a"), join("b")
			request(cluster.CarrierTunnel)
			poll()
			drive()
			Expect(row().State).To(Equal(cluster.StateCommit))
			poll(a)
			Expect(a.active()).To(Equal(cluster.CarrierTunnel))
			Expect(b.active()).To(Equal(cluster.CarrierNATS), "b has not polled yet")
			Expect(a.routes.Route(nodesvc.RouteDirect)).ToNot(BeNil(), "the old carrier stays attached")
			Expect(a.routes.Route(nodesvc.RouteTunnel)).ToNot(BeNil())
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

		It("does not move the broadcasts: every message goes once over the buses of the holders while the replicas flip", func() {
			pub, sub, third := join("pub"), join("sub"), join("third")
			var mu sync.Mutex
			got := map[string]int{}
			_, err := sub.bus.Subscribe(swapSubject, func(raw []byte) {
				mu.Lock()
				got[string(raw)]++
				mu.Unlock()
			})
			Expect(err).ToNot(HaveOccurred())
			var reconnects atomic.Int32
			sub.bus.OnReconnect(func() { reconnects.Add(1) })
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
			Expect(net.floor.PublishCount(swapSubject)).To(Equal(sent), "the floor carried every broadcast")
			Expect(reconnects.Load()).To(BeZero(), "the broadcasts did not move, so the consumers need not read their tables")
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

	})

	Describe("the drain", func() {
		It("keeps the old carrier attached until the leader ends the drain", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			poll()

			Expect(a.routes.Route(nodesvc.RouteDirect)).ToNot(BeNil(), "a worker that is only on the old carrier is still reached")
			Expect(a.life.seen()).ToNot(ContainElement(ContainSubstring("close:nats")))
		})

		It("releases the old carrier when the drain ends: detaches and closes it, and runs no reconnect hook", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			expire()

			Eventually(a.life.seen, "5s").Should(ContainElement("close:nats"))
			Expect(a.routes.Route(nodesvc.RouteDirect)).To(BeNil(), "the old carrier is no longer attached")
			Expect(a.reconnect.Load()).To(BeZero(), "the broadcasts never moved")
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

	Describe("the routes of the workers during a change", func() {
		It("sends a control request to the carrier that the worker is attached to", func() {
			a := join("a")
			a.attached["on-nats"] = att(true, false)
			a.attached["on-tunnel"] = att(false, true)
			a.attached["on-both"] = att(true, true)
			settle(cluster.CarrierTunnel)

			Expect(a.cmds.PingNode("on-nats")).To(Succeed())
			Expect(a.cmds.PingNode("on-tunnel")).To(Succeed())
			Expect(a.cmds.PingNode("on-both")).To(Succeed())
			Expect(a.fake(cluster.CarrierNATS).commands.seen()).To(HaveLen(2), "the worker that stayed on NATS, and the one on both: the direct route comes first")
			Expect(a.fake(cluster.CarrierTunnel).commands.seen()).To(HaveLen(1), "the worker on the tunnel")
		})

		It("reports a worker that is attached to neither carrier as ErrNoRoute, and calls nothing", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			err := a.cmds.PingNode("nowhere")
			Expect(err).To(MatchError(ContainSubstring("no route")))
			Expect(a.fake(cluster.CarrierNATS).commands.seen()).To(BeEmpty())
			Expect(a.fake(cluster.CarrierTunnel).commands.seen()).To(BeEmpty())
		})

		It("drops the route of the carrier that drained once the drain is over", func() {
			a := join("a")
			a.attached["on-nats"] = att(true, false)
			a.attached["on-tunnel"] = att(false, true)
			settle(cluster.CarrierTunnel)
			expire()
			Eventually(a.life.seen, "5s").Should(ContainElement("close:nats"))

			Expect(a.cmds.PingNode("on-nats")).To(MatchError(nodesvc.ErrNoRoute), "no route of this frontend reaches a worker that is only on NATS")
			Expect(a.cmds.PingNode("on-tunnel")).To(Succeed())
			Expect(a.fake(cluster.CarrierNATS).commands.seen()).To(BeEmpty(), "the closed carrier gets no call")
			Expect(a.fake(cluster.CarrierTunnel).commands.seen()).To(Equal([]string{"PingNode"}))
		})

		It("unloads a model on each node through the carrier that reaches it", func() {
			a := join("a")
			a.attached["on-nats"] = att(true, false)
			a.attached["on-tunnel"] = att(false, true)
			settle(cluster.CarrierTunnel)
			holders := []string{"on-nats", "on-tunnel"}
			natsSet, tunSet := a.fake(cluster.CarrierNATS), a.fake(cluster.CarrierTunnel)
			natsSet.set.Commands = &holdersCommands{fakeCommands: natsSet.commands, holders: holders,
				reaches: func(id string) bool { return id == "on-nats" }}
			tunSet.set.Commands = &holdersCommands{fakeCommands: tunSet.commands, holders: holders,
				reaches: func(id string) bool { return id == "on-tunnel" }}
			Expect(a.cmds.UnloadRemoteModel("m")).To(Succeed())
			Expect(natsSet.commands.seen()).To(Equal([]string{"unload:on-nats"}))
			Expect(tunSet.commands.seen()).To(Equal([]string{"unload:on-tunnel"}))
		})
	})

	Describe("the robustness of a swap", func() {
		It("attaches the route of the new set before the pointer moves, so that no call meets a gap", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			Expect(a.windowRec.seen()).To(ContainElement("attach:before-the-pointer-moved"))
			Expect(a.windowRec.seen()).ToNot(ContainElement("attach:after-the-pointer-moved"))
		})

		It("hooks the buses only: a change of carrier adds and leaves no reconnect hook", func() {
			a := join("a")
			base := a.bus.HookedCount()
			Expect(base).To(Equal(1), "the floor")
			request(cluster.CarrierTunnel)
			poll()
			Expect(a.bus.HookedCount()).To(Equal(base))
			_, err := sw.Abort(ctx, "admin")
			Expect(err).ToNot(HaveOccurred())
			poll()
			settle(cluster.CarrierTunnel)
			expire()
			Expect(a.bus.HookedCount()).To(Equal(base))
		})

		It("attaches the draining carrier again when a replica starts during a drain, and its route", func() {
			a := join("a")
			settle(cluster.CarrierTunnel)
			Expect(row().Draining).To(Equal(cluster.CarrierNATS))

			// A replica that starts now begins on the active carrier only.
			b := join("b")
			b.attached["on-nats"] = att(true, false)
			Expect(b.routes.Route(nodesvc.RouteDirect)).To(BeNil())
			poll(b)

			Expect(b.buildCount(cluster.CarrierNATS)).To(Equal(1))
			Expect(b.routes.Route(nodesvc.RouteDirect)).ToNot(BeNil())
			Expect(b.cmds.PingNode("on-nats")).To(Succeed())
			Expect(b.fake(cluster.CarrierNATS).commands.seen()).To(Equal([]string{"PingNode"}), "a worker that is only on the draining carrier is reached")

			// The drain ends: b lets the set go like a.
			expire()
			Eventually(b.life.seen, "5s").Should(ContainElement("close:nats"))
			Expect(b.routes.Route(nodesvc.RouteDirect)).To(BeNil())
			_ = a
		})

	})

	Describe("a call that runs while the set changes", func() {
		It("never blocks and never reaches a closed set", func() {
			// A call that took the draining route just before it was detached gets this
			// long to finish. It is long against the pause of a busy machine.
			settleFor = 2 * time.Second
			a, b := join("a"), join("b")
			// A worker that is on the carrier that drains only: its calls go to the
			// route of the draining set, until the drain ends.
			a.attached["on-nats"] = att(true, false)
			stop := make(chan struct{})
			var wg sync.WaitGroup
			var calls atomic.Int64
			for range 4 {
				wg.Go(func() {
					defer GinkgoRecover()
					for {
						select {
						case <-stop:
							return
						default:
						}
						Expect(a.bus.Publish(swapSubject, "x")).To(Succeed())
						// The fake carriers answer a call to a closed set with an error, as
						// a real one does, so a call that reaches one fails this spec.
						Expect(a.cmds.PingNode("on-nats")).To(Succeed())
						calls.Add(1)
					}
				})
			}
			settle(cluster.CarrierTunnel)
			time.Sleep(100 * time.Millisecond)
			a.attached["on-nats"] = att(false, true)
			expire()
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
				Cur: &a.cur, Routes: a.routes, Rows: store,
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
				Cur: &a.cur, Routes: a.routes, Rows: store,
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
