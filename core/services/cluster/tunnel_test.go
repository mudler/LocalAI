package cluster_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// claimHook runs an action once, from inside the database call that issued a
// matching statement, on that call's own goroutine.
//
// It is how a spec pins an interleaving instead of racing for one. gorm calls
// its logger's Trace after the statement has executed and before the Create or
// Delete that issued it returns (gorm@v1.31.1/callbacks.go:139-145), with the
// bind values interpolated into the SQL, so an action installed here runs at
// the one instant a claim has been written and not yet recorded. Racing two
// goroutines and hoping to land in that window is the flaky spec this replaces.
//
// It fires at most once: the action itself issues statements through the same
// session, and an unguarded hook would recurse.
type claimHook struct {
	gormlogger.Interface
	mu     sync.Mutex
	fired  bool
	match  func(sql string) bool
	action func(sql string)
}

func newClaimHook(match func(sql string) bool) *claimHook {
	return &claimHook{Interface: gormlogger.Default.LogMode(gormlogger.Silent), match: match}
}

// setAction installs what the hook runs, under the same lock that guards fired.
// The action is written from the spec's goroutine and read from whichever
// goroutine issues the statement, which need not be the same one.
func (h *claimHook) setAction(action func(sql string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.action = action
}

func (h *claimHook) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	h.mu.Lock()
	action := h.action
	fire := !h.fired && action != nil && h.match(sql)
	if fire {
		h.fired = true
	}
	h.mu.Unlock()
	if fire {
		action(sql)
	}
	h.Interface.Trace(ctx, begin, func() (string, int64) { return sql, rows }, err)
}

// isClaimOf matches the upsert Claim issues for one node.
func isClaimOf(nodeIDs ...string) func(string) bool {
	return func(sql string) bool {
		if !strings.Contains(sql, "INSERT INTO \"node_connections\"") {
			return false
		}
		for _, nodeID := range nodeIDs {
			if strings.Contains(sql, "'"+nodeID+"'") {
				return true
			}
		}
		return false
	}
}

// claimedNode reports which of the named nodes a claim statement was for.
func claimedNode(sql string, nodeIDs ...string) string {
	GinkgoHelper()
	for _, nodeID := range nodeIDs {
		if strings.Contains(sql, "'"+nodeID+"'") {
			return nodeID
		}
	}
	Fail("the claim statement named none of " + strings.Join(nodeIDs, ", "))
	return ""
}

// serializationProbe is how long a spec watches for something that must not
// happen. It bounds an assertion about an ABSENT event, which is the only kind
// of wait a spec cannot replace with a channel: there is no event to receive.
// The thing it watches for takes one database round trip when the serialisation
// it guards is missing, so this is orders of magnitude longer than it needs.
const serializationProbe = 500 * time.Millisecond

// workerTunnel returns the two halves of a worker's tunnel: the frontend holds
// the server half, because the worker is the side that dials. yamuxPair already
// builds exactly that pairing for peer links; this names the halves the way the
// worker path uses them so a spec cannot silently attach the wrong end.
func workerTunnel() (frontend *yamux.Session, worker *yamux.Session) {
	GinkgoHelper()
	worker, frontend = yamuxPair()
	return frontend, worker
}

// echoOnce accepts one stream on the worker's half and echoes what it reads.
// It is how a spec proves Open produced a stream that carries bytes, rather
// than a handle that merely exists.
func echoOnce(worker *yamux.Session) {
	go func() {
		defer GinkgoRecover()
		stream, err := worker.AcceptStream()
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()
		buf := make([]byte, 4)
		if _, err := stream.Read(buf); err != nil {
			return
		}
		_, _ = stream.Write(buf)
	}()
}

// drain accepts and discards every stream on the worker's half, so a spec that
// opens streams without reading them does not park on the accept backlog.
func drain(worker *yamux.Session) {
	go func() {
		defer GinkgoRecover()
		for {
			stream, err := worker.AcceptStream()
			if err != nil {
				return
			}
			_ = stream.Close()
		}
	}()
}

var _ = Describe("The worker tunnel registry", func() {
	var (
		db  *gorm.DB
		reg *cluster.Registry
		tun *cluster.TunnelRegistry
		ctx context.Context
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
		Expect(reg.Register(ctx, "me", "10.0.0.1:8080", "v1")).To(Succeed())
		tun = cluster.NewTunnelRegistry(reg, "me")
	})

	It("claims the node in the database before it stores the session", func() {
		// A claimant that installs itself and only then tries to claim has,
		// for that window, made the registry disagree with the table: Held
		// names a tunnel no row records, and a peer asking Owner is told the
		// worker is connected nowhere. The failure is injected through the
		// production refusal path, a dialect with no epoch sequence, so the
		// spec exercises the real error return rather than a fake.
		sqliteDB, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Migrate(ctx, sqliteDB)).To(Succeed())
		unclaimable := cluster.NewTunnelRegistry(cluster.NewRegistry(sqliteDB), "me")

		frontend, _ := workerTunnel()
		_, err = unclaimable.Attach(ctx, "w1", frontend)
		Expect(err).To(HaveOccurred())

		Expect(unclaimable.Held()).To(BeEmpty(),
			"a claimant whose claim failed installed itself anyway, so the registry now disagrees with the table")
		_, err = unclaimable.Open(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNotOwner))
	})

	It("records the claim, so another replica can find the owner", func() {
		frontend, _ := workerTunnel()
		epoch, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		Expect(tun.Held()).To(ConsistOf("w1"))
		owner, stored, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("me"))
		Expect(stored).To(Equal(epoch), "Attach handed back an epoch that is not the one it wrote")
	})

	It("opens a stream that carries bytes to the worker", func() {
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		echoOnce(worker)

		conn, err := tun.Open(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(conn).ToNot(BeNil())
		DeferCleanup(func() { _ = conn.Close() })

		Expect(conn.SetDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
		_, err = conn.Write([]byte("ping"))
		Expect(err).ToNot(HaveOccurred())
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("ping"))
	})

	It("reports ErrNotOwner for a node whose tunnel it does not hold", func() {
		// This is a routing fact and nothing more: some other replica may hold
		// the worker perfectly well. It must never be produced by anything that
		// merely failed.
		_, err := tun.Open(ctx, "nobody")
		Expect(err).To(MatchError(cluster.ErrNotOwner))
	})

	It("supersedes an earlier attachment, and the superseded holder's Detach is a no-op", func() {
		first, _ := workerTunnel()
		firstEpoch, err := tun.Attach(ctx, "w1", first)
		Expect(err).ToNot(HaveOccurred())

		second, secondWorker := workerTunnel()
		secondEpoch, err := tun.Attach(ctx, "w1", second)
		Expect(err).ToNot(HaveOccurred())
		// Compared for difference, never for order. Claim guarantees an epoch
		// is unique and never reissued; it does NOT guarantee the later claim
		// draws the larger number, because the sequence value on the insert
		// path is drawn before the row lock.
		Expect(secondEpoch).ToNot(Equal(firstEpoch))

		Expect(first.IsClosed()).To(BeTrue(),
			"the superseded session was left open, so whoever is accepting on it never learns it was replaced")

		tun.Detach("w1", firstEpoch)

		Expect(tun.Held()).To(ConsistOf("w1"), "a stale Detach evicted the live session")
		owner, stored, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("me"))
		Expect(stored).To(Equal(secondEpoch), "a stale Detach released the live claim")

		echoOnce(secondWorker)
		conn, err := tun.Open(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })
		Expect(conn.SetDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
		_, err = conn.Write([]byte("ping"))
		Expect(err).ToNot(HaveOccurred())
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("ping"))

		tun.Detach("w1", secondEpoch)
		Expect(tun.Held()).To(BeEmpty())
		_, _, err = reg.OwnerRow(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection))
	})

	It("ignores a Detach naming an epoch it was never handed", func() {
		frontend, _ := workerTunnel()
		epoch, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		// Not an ordering probe: both directions are tried because an epoch is
		// unique but unordered, so a stale token can compare either way against
		// the live one and neither may be allowed to evict it.
		tun.Detach("w1", epoch+1)
		tun.Detach("w1", epoch-1)

		Expect(tun.Held()).To(ConsistOf("w1"))
		_, stored, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(stored).To(Equal(epoch))
	})

	It("does not report a held tunnel whose session has died as ErrNotOwner", func() {
		// Absence and unreachability are different answers and callers act
		// differently on them: a dialer told the worker is not here relays
		// elsewhere or reports it gone, where the truth is that this replica
		// holds the tunnel and the socket underneath it broke.
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		Expect(worker.Close()).To(Succeed())
		Eventually(frontend.IsClosed, "10s").Should(BeTrue())

		_, err = tun.Open(ctx, "w1")
		Expect(err).To(HaveOccurred())
		Expect(err).ToNot(MatchError(cluster.ErrNotOwner),
			"a broken socket was reported as this replica not holding the tunnel")
		Expect(tun.Held()).To(ConsistOf("w1"),
			"holding the tunnel is a routing fact, and a failed Open is not what un-holds it")
	})

	It("blames the caller's own spent budget, not the tunnel, when a stream cannot be opened", func() {
		// The second of the three sites where peerlink.go's callerRanOut rule
		// has to hold. A caller whose budget ran out gets the multiplexer's
		// error back before the scheduler has necessarily run its context's
		// cancel func, so ctx.Err() can still read nil while the failure is
		// entirely the caller's; reported plainly it becomes "the tunnel this
		// replica holds would not carry a stream" in an operator's log for a
		// worker that is fine.
		//
		// deadlinePassed is that window made deterministic: deadline elapsed,
		// cancellation not delivered. Nothing about the broken session is
		// faked.
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		Expect(worker.Close()).To(Succeed())
		Eventually(frontend.IsClosed, "10s").Should(BeTrue())

		_, err = tun.Open(deadlinePassed{ctx}, "w1")
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(context.DeadlineExceeded),
			"the caller's budget was spent, and only it can say so")
		Expect(err.Error()).To(ContainSubstring("the caller's own budget ran out"))
	})

	It("refuses a nil session rather than claiming a tunnel that cannot carry anything", func() {
		// A claim written for a session that does not exist publishes a tunnel
		// to every replica in the deployment, and the fence would then have to
		// be unwound by a Detach nobody is going to call.
		_, err := tun.Attach(ctx, "w1", nil)
		Expect(err).To(HaveOccurred())

		Expect(tun.Held()).To(BeEmpty())
		_, _, err = reg.OwnerRow(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection),
			"a node with no session was published to the deployment as connected here")
	})

	It("returns the nodes it holds in sorted order", func() {
		// Attached out of order on purpose: sorted output is what makes a log
		// line and a re-claim pass comparable between two runs, and a map
		// range would only look sorted until it did not.
		for _, nodeID := range []string{"w3", "w1", "w2"} {
			frontend, _ := workerTunnel()
			_, err := tun.Attach(ctx, nodeID, frontend)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(tun.Held()).To(Equal([]string{"w1", "w2", "w3"}))
	})

	It("serialises two Attach calls for one node, so the surviving entry holds the row's epoch", func() {
		// Two claims for one node are serialised by PostgreSQL, but nothing
		// orders the two map writes against the two commits. Unserialised, the
		// entry left installed can carry the epoch of the claim that lost the
		// row: its Detach then releases an epoch the row does not hold, the
		// release matches nothing, and the row outlives the socket. Nothing
		// sweeps that, because this replica is alive and heartbeating, so Owner
		// keeps sending dialers here to be told ErrNotOwner.
		hook := newClaimHook(isClaimOf("w1"))
		hooked := cluster.NewTunnelRegistry(
			cluster.NewRegistry(db.Session(&gorm.Session{Logger: hook})), "me")

		secondSession, _ := workerTunnel()
		secondStarted := make(chan struct{})
		secondEpochs := make(chan int64, 1)
		hook.setAction(func(string) {
			// Launched from inside the first claim, so the second Attach is
			// provably reaching for the same node while the first is between
			// its claim and its store. Starting it before the call would leave
			// which one claims first to the scheduler.
			go func() {
				defer GinkgoRecover()
				close(secondStarted)
				epoch, err := hooked.Attach(ctx, "w1", secondSession)
				Expect(err).ToNot(HaveOccurred())
				secondEpochs <- epoch
			}()
			<-secondStarted
			Consistently(secondEpochs, serializationProbe, 10*time.Millisecond).ShouldNot(Receive(),
				"a second Attach for this node claimed AND recorded its epoch while the first was between its own claim and store")
		})

		firstSession, _ := workerTunnel()
		firstEpoch, err := hooked.Attach(ctx, "w1", firstSession)
		Expect(err).ToNot(HaveOccurred())
		var secondEpoch int64
		Eventually(secondEpochs, "10s").Should(Receive(&secondEpoch))
		Expect(secondEpoch).ToNot(Equal(firstEpoch))

		_, stored, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect([]int64{firstEpoch, secondEpoch}).To(ContainElement(stored))

		// Whichever attachment survived, one of these two Detach calls is the
		// live one and must take the row with it. If neither does, the row is
		// carrying an epoch no attachment holds.
		hooked.Detach("w1", firstEpoch)
		hooked.Detach("w1", secondEpoch)
		Expect(hooked.Held()).To(BeEmpty())
		_, _, err = reg.OwnerRow(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection),
			"the surviving attachment could not release its row, so the row outlived the socket")
	})

	It("serves Attach, Open, Held and Detach from independent goroutines", func() {
		// Run under -race. The point is contention on one node's entry, not a
		// tidy per-goroutine partition: a registry whose map is only ever
		// touched by one goroutine at a time proves nothing about the one that
		// is not.
		const workers = 8
		start := make(chan struct{})
		var wg sync.WaitGroup

		attached := make(chan int64, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				frontend, worker := workerTunnel()
				drain(worker)
				<-start
				epoch, err := tun.Attach(ctx, fmt.Sprintf("w%d", i%2), frontend)
				Expect(err).ToNot(HaveOccurred())
				attached <- epoch
				if conn, err := tun.Open(ctx, fmt.Sprintf("w%d", i%2)); err == nil {
					_ = conn.Close()
				}
			}(i)
		}
		readers := make(chan struct{})
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				for {
					select {
					case <-readers:
						return
					default:
						tun.Held()
					}
				}
			}()
		}

		close(start)
		epochs := make([]int64, 0, workers)
		for i := 0; i < workers; i++ {
			epochs = append(epochs, <-attached)
		}
		close(readers)
		wg.Wait()

		// Exactly one attachment per node survived, whichever won, and the
		// table agrees with the map about which.
		Expect(tun.Held()).To(ConsistOf("w0", "w1"))
		for _, node := range tun.Held() {
			_, stored, err := reg.OwnerRow(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(epochs).To(ContainElement(stored),
				"the table records an epoch no Attach ever handed out")
		}

		// Every loser's Detach is a no-op; only the two winners empty the map.
		for _, epoch := range epochs {
			tun.Detach("w0", epoch)
			tun.Detach("w1", epoch)
		}
		Expect(tun.Held()).To(BeEmpty())
		for _, node := range []string{"w0", "w1"} {
			_, _, err := reg.OwnerRow(ctx, node)
			Expect(err).To(MatchError(cluster.ErrNoConnection),
				"node %s kept a row no attachment could release", node)
		}
	})
})

var _ = Describe("Re-claiming tunnels after this replica's rows were reaped", func() {
	var (
		db  *gorm.DB
		reg *cluster.Registry
		tun *cluster.TunnelRegistry
		ctx context.Context
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
		Expect(reg.Register(ctx, "me", "10.0.0.1:8080", "v1")).To(Succeed())
		tun = cluster.NewTunnelRegistry(reg, "me")
	})

	// reapSelf performs the two deletes a peer's sweep performs on this
	// replica: the instance row and, in the same transaction, every connection
	// it owned. Deregister is that transaction; ReapStale reaches it by aging
	// last_seen, which cannot be done deterministically against a heartbeat
	// loop that is refreshing the same column. That ReapStale deletes both is
	// pinned separately, with no loop running, in the reaping specs.
	reapSelf := func() {
		GinkgoHelper()
		Expect(reg.Deregister(ctx, "me")).To(Succeed())
	}

	It("re-claims every held tunnel when the loop finds its instance row gone", func() {
		// Without this a replica that stalled long enough to be swept sits
		// holding live worker sockets that no row records. Every other replica
		// then answers "not connected" for workers that are connected, which is
		// the absence-versus-unreachable failure the design forbids.
		frontend, _ := workerTunnel()
		epoch, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		membership := cluster.NewMembership(reg, "me", "10.0.0.1:8080", "v1")
		membership.SetTunnels(tun)
		Expect(membership.Start(ctx)).To(Succeed())
		DeferCleanup(membership.Stop)

		reapSelf()
		_, _, err = reg.OwnerRow(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection))

		owner := func() (string, error) {
			owner, _, err := reg.OwnerRow(ctx, "w1")
			return owner, err
		}
		Eventually(owner, 3*cluster.InstanceHeartbeat, time.Second).Should(Equal("me"))

		_, reclaimed, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(reclaimed).ToNot(Equal(epoch), "the re-claim reused the epoch of a claim the sweep deleted")

		// The holder still carries the epoch Attach handed it, and is the only
		// thing that will ever release this row. If Detach matched only the
		// epoch the re-claim drew, the row would outlive the socket and no
		// caller could tell.
		tun.Detach("w1", epoch)
		Expect(tun.Held()).To(BeEmpty())
		_, _, err = reg.OwnerRow(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection))
	})

	It("does not re-claim a tunnel whose session has already died", func() {
		// Re-claiming is an upsert, so it takes the row from whoever holds it
		// now. A worker whose socket here is dead has already reconnected
		// somewhere, and claiming it back would point every dialer at a replica
		// that cannot carry a byte to it.
		live, _ := workerTunnel()
		liveEpoch, err := tun.Attach(ctx, "live", live)
		Expect(err).ToNot(HaveOccurred())
		dead, deadWorker := workerTunnel()
		_, err = tun.Attach(ctx, "dead", dead)
		Expect(err).ToNot(HaveOccurred())
		Expect(deadWorker.Close()).To(Succeed())
		Eventually(dead.IsClosed, "10s").Should(BeTrue())

		membership := cluster.NewMembership(reg, "me", "10.0.0.1:8080", "v1")
		membership.SetTunnels(tun)
		Expect(membership.Start(ctx)).To(Succeed())
		DeferCleanup(membership.Stop)

		reapSelf()
		owner := func() (string, error) {
			owner, _, err := reg.OwnerRow(ctx, "live")
			return owner, err
		}
		Eventually(owner, 3*cluster.InstanceHeartbeat, time.Second).Should(Equal("me"))

		_, reclaimedLive, err := reg.OwnerRow(ctx, "live")
		Expect(err).ToNot(HaveOccurred())
		Expect(reclaimedLive).ToNot(Equal(liveEpoch))

		_, _, err = reg.OwnerRow(ctx, "dead")
		Expect(err).To(MatchError(cluster.ErrNoConnection),
			"a tunnel whose session is closed was claimed back from whoever holds the worker now")
	})

	It("records the re-claim on the attachment installed now, not the one it listed", func() {
		// Reclaim lists the nodes it holds, then claims them one at a time. A
		// worker that re-dials in between leaves an entry the list never saw.
		// The claim is drawn under that node's gate, so it is the NEWEST claim
		// for the node and the row carries it: recording it on the entry that
		// is installed is the only thing that lets that entry release the row.
		// Refusing to record it because the entry is not the one listed would
		// leave the row behind when the socket dies.
		hook := newClaimHook(isClaimOf("w1", "w2"))
		hooked := cluster.NewTunnelRegistry(
			cluster.NewRegistry(db.Session(&gorm.Session{Logger: hook})), "me")

		for _, nodeID := range []string{"w1", "w2"} {
			frontend, _ := workerTunnel()
			_, err := hooked.Attach(ctx, nodeID, frontend)
			Expect(err).ToNot(HaveOccurred())
		}

		// The re-dial lands on whichever node this pass has not reached yet,
		// so the spec does not depend on which one Reclaim takes first.
		type redial struct {
			node  string
			epoch int64
		}
		redialled := make(chan redial, 1)
		hook.setAction(func(sql string) {
			node := "w2"
			if claimedNode(sql, "w1", "w2") == "w2" {
				node = "w1"
			}
			frontend, _ := workerTunnel()
			epoch, err := hooked.Attach(ctx, node, frontend)
			Expect(err).ToNot(HaveOccurred())
			redialled <- redial{node: node, epoch: epoch}
		})

		count, err := hooked.Reclaim(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(2))

		var latest redial
		Expect(redialled).To(Receive(&latest))
		_, stored, err := reg.OwnerRow(ctx, latest.node)
		Expect(err).ToNot(HaveOccurred())
		Expect(stored).ToNot(Equal(latest.epoch), "the re-claim never reached the node that re-dialled")

		// The attachment that re-dialled is the one holding the socket, so its
		// Detach has to be the one that removes the row.
		hooked.Detach(latest.node, latest.epoch)
		_, _, err = reg.OwnerRow(ctx, latest.node)
		Expect(err).To(MatchError(cluster.ErrNoConnection),
			"the re-claim was recorded on nothing, so the attachment that holds the socket cannot release its row")
	})

	It("serialises a re-claim against an Attach for the same node", func() {
		// The re-claim takes the same gate Attach does, and for the same
		// reason. Unserialised, a worker re-dialling between the re-claim's
		// commit and its record leaves the row carrying the re-dial's epoch
		// while the entry carries the re-claim's, so the attachment holding the
		// socket releases an epoch the row does not have. Nothing sweeps the
		// row that is left, because this replica is alive: Owner keeps naming
		// it as the owner of a tunnel it does not hold.
		hook := newClaimHook(isClaimOf("w1"))
		hooked := cluster.NewTunnelRegistry(
			cluster.NewRegistry(db.Session(&gorm.Session{Logger: hook})), "me")

		first, _ := workerTunnel()
		attachEpoch, err := hooked.Attach(ctx, "w1", first)
		Expect(err).ToNot(HaveOccurred())

		redialSession, _ := workerTunnel()
		redialStarted := make(chan struct{})
		redialEpochs := make(chan int64, 1)
		hook.setAction(func(string) {
			// Launched from inside the re-claim's own claim, so the re-dial is
			// provably reaching for this node while the re-claim is between its
			// commit and its record.
			go func() {
				defer GinkgoRecover()
				close(redialStarted)
				epoch, err := hooked.Attach(ctx, "w1", redialSession)
				Expect(err).ToNot(HaveOccurred())
				redialEpochs <- epoch
			}()
			<-redialStarted
			Consistently(redialEpochs, serializationProbe, 10*time.Millisecond).ShouldNot(Receive(),
				"a worker re-dialled and recorded its claim while a re-claim for the same node was between its own claim and record")
		})

		count, err := hooked.Reclaim(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))

		var redialEpoch int64
		Eventually(redialEpochs, "10s").Should(Receive(&redialEpoch))
		Expect(redialEpoch).ToNot(Equal(attachEpoch))

		// The re-dial claimed after the re-claim, so the row carries its epoch
		// and its attachment is the one that has to be able to release it. The
		// superseded token must still be a no-op.
		hooked.Detach("w1", attachEpoch)
		Expect(hooked.Held()).To(ConsistOf("w1"))
		hooked.Detach("w1", redialEpoch)
		Expect(hooked.Held()).To(BeEmpty())
		_, _, err = reg.OwnerRow(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection),
			"the attachment that re-dialled could not release its row, so the row outlived the socket")
	})

	It("releases a re-claim whose attachment detached while the claim was in flight", func() {
		// Detach is not gated against a re-claim, so this interleave is real:
		// the claim commits, then the socket dies and Detach releases the epoch
		// it was given, which the claim has already replaced. Left alone, the
		// row names this replica for a tunnel it no longer holds, nothing
		// sweeps it because this replica is alive, and Owner sends every dialer
		// here to be told ErrNotOwner.
		hook := newClaimHook(isClaimOf("w1"))
		hooked := cluster.NewTunnelRegistry(
			cluster.NewRegistry(db.Session(&gorm.Session{Logger: hook})), "me")

		frontend, _ := workerTunnel()
		epoch, err := hooked.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		hook.setAction(func(string) { hooked.Detach("w1", epoch) })

		count, err := hooked.Reclaim(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero(), "a node that detached mid-claim was counted as re-claimed")

		Expect(hooked.Held()).To(BeEmpty())
		_, _, err = reg.OwnerRow(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection),
			"the re-claim left a row behind that no attachment holds and no sweep will remove")
	})

	It("keeps re-claiming out of the ordinary heartbeat, which has nothing to rebuild", func() {
		// A claim per tick would draw a fresh epoch every five seconds for
		// every worker on this replica, and every one of those writes is a
		// chance to take a row a reconnect has just moved elsewhere.
		frontend, _ := workerTunnel()
		epoch, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		membership := cluster.NewMembership(reg, "me", "10.0.0.1:8080", "v1")
		membership.SetTunnels(tun)
		Expect(membership.Start(ctx)).To(Succeed())
		DeferCleanup(membership.Stop)

		stored := func() (int64, error) {
			_, stored, err := reg.OwnerRow(ctx, "w1")
			return stored, err
		}
		Consistently(stored, 2*cluster.InstanceHeartbeat, time.Second).Should(Equal(epoch))
	})

	It("frees the node's gate when a re-claim panics", func() {
		// The same property Attach's gate specs pin, on the other function that
		// takes the gate. Reclaim runs from the heartbeat loop, so a wedged gate
		// here is worse than one wedged by a dial: nothing retries it, and the
		// worker can never re-attach to this replica because its Attach blocks
		// in enterClaim until its own context expires.
		//
		// The panic is thrown from inside the re-claim's own Claim statement,
		// on that statement's goroutine, using the same gorm Trace hook the
		// interleaving specs above use. That is the window a real panic under
		// Claim would land in: the row is written and the gate is held.
		hook := newClaimHook(isClaimOf("w1"))
		hooked := cluster.NewTunnelRegistry(
			cluster.NewRegistry(db.Session(&gorm.Session{Logger: hook})), "me")

		frontend, _ := workerTunnel()
		_, err := hooked.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		// Installed after the attach so the hook fires on the RE-claim, not on
		// the claim that set the tunnel up.
		hook.setAction(func(string) { panic("claim exploded") })

		panicked := func() (p bool) {
			defer func() { p = recover() != nil }()
			_, _ = hooked.Reclaim(ctx)
			return
		}()
		Expect(panicked).To(BeTrue(),
			"the hook did not fire inside the re-claim, so this spec is no longer testing what it claims")

		// A wedged gate is indistinguishable from a slow one except by waiting.
		// The hook has already fired once and will not fire again, so this
		// Attach either completes or never reaches Claim at all.
		bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		next, _ := workerTunnel()
		_, err = hooked.Attach(bounded, "w1", next)
		Expect(err).ToNot(HaveOccurred(),
			"the panicking re-claim left this node's gate closed, so the worker can never attach to this replica again")
	})
})

var _ = Describe("The worker tunnel registry's claim gate", func() {
	// These specs need no database. They pin what happens when the database
	// call under the gate does not return normally, which is the one exit a
	// release-on-every-return-path cannot cover.
	//
	// The panic is produced by the production code itself: a registry with no
	// *Registry behind it dereferences nothing at the gate and then panics
	// inside Claim, which is exactly where a real one does its work.
	var tun *cluster.TunnelRegistry

	BeforeEach(func() {
		tun = cluster.NewTunnelRegistry(nil, "me")
	})

	// attachPanics runs one Attach that is expected to panic, swallowing the
	// panic so the spec can go on to ask what state it left behind.
	attachPanics := func(ctx context.Context, nodeID string) {
		defer GinkgoRecover()
		defer func() { _ = recover() }()
		frontend, _ := workerTunnel()
		_, _ = tun.Attach(ctx, nodeID, frontend)
		Fail("Attach was expected to panic inside Claim, so this spec is no longer testing what it claims")
	}

	It("frees the node's gate when the claim panics", func() {
		attachPanics(context.Background(), "w1")

		// A wedged gate is indistinguishable from a slow one except by waiting,
		// so the second attempt is given a deadline. Reaching Claim means
		// panicking again; returning a context error means it never got past
		// enterClaim and this worker could never reconnect to this replica.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		reached := make(chan any, 1)
		go func() {
			defer GinkgoRecover()
			defer func() { reached <- recover() }()
			frontend, _ := workerTunnel()
			_, err := tun.Attach(ctx, "w1", frontend)
			Expect(err).To(MatchError(context.DeadlineExceeded),
				"the gate for this node was never released, so every later dial from it blocks until its own context expires")
		}()
		Eventually(reached, "5s").Should(Receive(Not(BeNil())),
			"the second Attach did not reach Claim, so the panicking one left the gate closed")
	})

	It("leaves another node's gate alone", func() {
		// The gate is per node so that one wedged worker cannot stop the rest;
		// this holds that property against the panic path too.
		attachPanics(context.Background(), "w1")

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		reached := make(chan any, 1)
		go func() {
			defer GinkgoRecover()
			defer func() { reached <- recover() }()
			frontend, _ := workerTunnel()
			_, _ = tun.Attach(ctx, "w2", frontend)
		}()
		Eventually(reached, "5s").Should(Receive(Not(BeNil())))
	})
})

var _ = Describe("Membership.SetTunnels", func() {
	It("is safe on a nil receiver, like Stop", func() {
		// A nil *Membership is a value this codebase deliberately produces when
		// no peer-reachable address can be derived, so the asymmetry with Stop
		// would be a trap for the next caller.
		var m *cluster.Membership
		Expect(func() { m.SetTunnels(cluster.NewTunnelRegistry(nil, "me")) }).ToNot(Panic())
	})
})
