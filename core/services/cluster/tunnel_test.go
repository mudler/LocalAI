package cluster_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
})
