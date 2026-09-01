package distributed_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	clustersvc "github.com/mudler/LocalAI/core/services/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	// instanceRosterTimeout bounds the wait for a replica's row to appear.
	// Registration is synchronous in startup, so this only has to cover the gap
	// between /readyz answering and this spec's first query.
	instanceRosterTimeout = "30s"
	instanceRosterPoll    = "500ms"

	// deadReplicaTimeout bounds the wait for a survivor to reap a replica that
	// was killed: the liveness window plus a sweep interval plus slack. It is
	// deliberately derived from the constants rather than a round number, so
	// tightening the window shortens the spec instead of leaving it passing for
	// the wrong reason.
	deadReplicaTimeout = clustersvc.InstanceLiveness + 4*clustersvc.InstanceHeartbeat

	// peerDialTimeout bounds one peer dial. Every replica here is a local
	// process, so a dial that needs longer has failed, not slowed.
	peerDialTimeout = 20 * time.Second
)

// openClusterDB connects to the database the cluster was given, so a spec can
// read the tables the peer link keeps. Nothing serves them over HTTP: they are
// replica-to-replica state, not an admin surface, and inventing an endpoint to
// observe them would be a bigger change than the thing under test.
func openClusterDB(dsn string) *gorm.DB {
	GinkgoHelper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { closeDB(db) })
	return db
}

// hostPortOf strips the scheme off a frontend URL, giving the form the
// instances table stores.
func hostPortOf(url string) string {
	return strings.TrimPrefix(strings.TrimPrefix(url, "http://"), "https://")
}

// instanceRoster reads the live replica rows, keeping the last error so a
// failing Eventually can name it.
type instanceRoster struct {
	registry *clustersvc.Registry
	ctx      context.Context

	lastErr error
	lastSaw []clustersvc.Instance
}

func newInstanceRoster(db *gorm.DB) *instanceRoster {
	return &instanceRoster{registry: clustersvc.NewRegistry(db), ctx: context.Background()}
}

// addresses returns the advertised address of every live replica, or nil on a
// query error so Eventually keeps trying.
func (r *instanceRoster) addresses() []string {
	live, err := r.registry.Live(r.ctx, clustersvc.InstanceLiveness)
	if err != nil {
		r.lastErr = err
		return nil
	}
	r.lastErr = nil
	r.lastSaw = live
	addrs := []string{}
	for _, instance := range live {
		addrs = append(addrs, instance.AdvertisedAddr)
	}
	return addrs
}

// idAt returns the id of the live replica advertising addr, or "" if no such
// row is present yet.
func (r *instanceRoster) idAt(addr string) string {
	for _, instance := range r.lastSaw {
		if instance.AdvertisedAddr == addr {
			return instance.ID
		}
	}
	return ""
}

func (r *instanceRoster) describe() string {
	if r.lastErr != nil {
		return fmt.Sprintf("the last read of the instances table failed: %v", r.lastErr)
	}
	return fmt.Sprintf("the instances table held %d live replica(s): %+v", len(r.lastSaw), r.lastSaw)
}

// awaitReplicas waits for every frontend of c to publish its address and
// returns the roster, positioned on that reading.
func awaitReplicas(roster *instanceRoster, addrs ...string) {
	GinkgoHelper()
	Eventually(roster.addresses, instanceRosterTimeout, instanceRosterPoll).
		Should(ConsistOf(addrs), roster.describe)
}

var _ = Describe("Cluster peer link", Label("Distributed"), Label("Cluster"), func() {
	It("publishes an address for every replica that peers can actually dial", func() {
		// A wrong implementation registers nothing (the whole of phase 1 had no
		// call site until this spec), registers one row for two replicas, or
		// records an address nothing can connect to: the bind address of a
		// replica behind a service, or the loopback address the route to a
		// co-located database would suggest.
		c, dsn := startClusterOnFreshDB(2, 0)

		roster := newInstanceRoster(openClusterDB(dsn))
		awaitReplicas(roster, hostPortOf(c.FrontendURL(0)), hostPortOf(c.FrontendURL(1)))

		// "Routable" is not a property of the string. Connect to each address,
		// which is the only check that would have caught a replica publishing
		// the port it was configured with rather than the one it serves on.
		for _, instance := range roster.lastSaw {
			conn, err := net.DialTimeout("tcp", instance.AdvertisedAddr, peerDialTimeout)
			Expect(err).ToNot(HaveOccurred(),
				"replica %s advertises %q, which nothing can connect to", instance.ID, instance.AdvertisedAddr)
			Expect(conn.Close()).To(Succeed())
		}
	})

	It("carries a peer stream between two replicas, and refuses one without the cluster token", func() {
		// A wrong implementation fails here on WebSocket framing, which is the
		// likeliest defect in the peer link: the adapter has to turn
		// message-oriented WebSocket frames into the undelimited byte stream
		// yamux drives. It also fails if the route was never registered on the
		// real server, or if the global session middleware answers it: a peer
		// carries no session and no user, only the cluster token.
		//
		// The stream is opened with the production dialler, resolving the peer
		// through the production registry, over a real socket to a real
		// process. This spec plays the sibling replica, because phase 1 has
		// nothing that makes a frontend dial one on its own.
		c, dsn := startClusterOnFreshDB(2, 0)

		roster := newInstanceRoster(openClusterDB(dsn))
		awaitReplicas(roster, hostPortOf(c.FrontendURL(0)), hostPortOf(c.FrontendURL(1)))

		peerID := roster.idAt(hostPortOf(c.FrontendURL(1)))
		Expect(peerID).ToNot(BeEmpty())

		ctx, cancel := context.WithTimeout(context.Background(), peerDialTimeout)
		defer cancel()

		pool := clustersvc.NewPeerPool("e2e-peer", c.RegistrationToken(), roster.registry)
		DeferCleanup(pool.Close)

		// OpenStream is only acknowledged once the far side accepts, so this
		// returning at all proves the frontend is accepting streams on the
		// session it took, in addition to proving the handshake.
		stream, err := pool.Open(ctx, peerID)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })

		// Phase 1 installs no relay, so the accepted stream is refused at once.
		// What matters here is that the refusal arrives: a replica that held
		// the session without accepting on it would leave this read parked
		// until the deadline, which is the failure mode a live cluster would
		// experience as every relayed request hanging.
		Expect(stream.SetReadDeadline(time.Now().Add(peerDialTimeout))).To(Succeed())
		_, err = stream.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred(),
			"the peer accepted the stream and then neither answered nor closed it")
		Expect(err).ToNot(MatchError(context.DeadlineExceeded))

		// The same dial with the wrong credentials must be refused, otherwise
		// the success above says nothing about authentication.
		impostor := clustersvc.NewPeerPool("e2e-peer", "not-the-cluster-token", roster.registry)
		DeferCleanup(impostor.Close)
		_, err = impostor.Open(ctx, peerID)
		Expect(err).To(MatchError(clustersvc.ErrPeerUnreachable))
		Expect(err).ToNot(MatchError(clustersvc.ErrInstanceNotFound),
			"a peer refusing credentials is a live peer; reading it as absence is how a replica evicts healthy workers")
	})

	It("reports a killed replica as unreachable, reaps what it owned, and evicts no worker", func() {
		// This is the absence rule, pinned before phase 2 can depend on it. A
		// wrong implementation lets a peer that will not answer surface as node
		// absence, and a caller entitled to act on absence then reclaims what
		// the peer was running: a network hiccup between two healthy replicas
		// evicts healthy workers.
		//
		// It also pins the reaper: the connection rows a dead replica owned are
		// swept by the same sweeper that decides the replica is dead, so the
		// two can never disagree about who is alive.
		c, dsn := startClusterOnFreshDB(2, 1)

		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		// The worker registers with frontend 0, so frontend 1 is the replica
		// that can die without taking the worker's registrar with it.
		registrar, err := c.WorkerRegistrar(0)
		Expect(err).ToNot(HaveOccurred())
		Expect(registrar).To(Equal(0), "this spec kills frontend 1 and needs the worker to have registered elsewhere")

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		workerID := probe.idOf(c.WorkerName(0))
		Expect(workerID).ToNot(BeEmpty())

		roster := newInstanceRoster(openClusterDB(dsn))
		awaitReplicas(roster, hostPortOf(c.FrontendURL(0)), hostPortOf(c.FrontendURL(1)))
		survivorID := roster.idAt(hostPortOf(c.FrontendURL(0)))
		doomedID := roster.idAt(hostPortOf(c.FrontendURL(1)))
		Expect(survivorID).ToNot(BeEmpty())
		Expect(doomedID).ToNot(BeEmpty())

		// Give frontend 1 the worker's tunnel. Phase 2 makes the worker do this
		// by dialling; here the claim is written directly, because the point
		// under test is what happens to the claim when its owner dies.
		ctx := context.Background()
		epoch, err := roster.registry.Claim(ctx, workerID, doomedID)
		Expect(err).ToNot(HaveOccurred())
		Expect(epoch).ToNot(BeZero())

		Expect(c.KillFrontend(1)).To(Succeed())
		Eventually(func() bool { return c.FrontendAlive(1) }, "20s", "500ms").Should(BeFalse())

		// The row is still there for the whole liveness window, so this is the
		// case that matters: the peer is KNOWN and will not answer.
		dialCtx, cancel := context.WithTimeout(ctx, peerDialTimeout)
		defer cancel()
		pool := clustersvc.NewPeerPool("e2e-peer", c.RegistrationToken(), roster.registry)
		DeferCleanup(pool.Close)
		_, err = pool.Open(dialCtx, doomedID)
		Expect(err).To(MatchError(clustersvc.ErrPeerUnreachable))
		Expect(err).ToNot(MatchError(clustersvc.ErrInstanceNotFound),
			"a dead replica whose row is still present is unreachable, not absent")

		// The survivor sweeps the dead replica and, in the same pass, the claim
		// it left behind.
		Eventually(roster.addresses, deadReplicaTimeout, instanceRosterPoll).
			Should(ConsistOf(hostPortOf(c.FrontendURL(0))), roster.describe)
		ownerErr := func() error {
			_, _, err := roster.registry.Owner(ctx, workerID)
			return err
		}
		Eventually(ownerErr, deadReplicaTimeout, instanceRosterPoll).
			Should(MatchError(clustersvc.ErrNoConnection),
				"the claim held by a replica that no longer exists was never reaped")

		// And the worker itself is untouched throughout. Nothing about a peer
		// dying may reach the node roster.
		Consistently(probe.healthyNames, "6s", "1s").
			Should(ContainElement(c.WorkerName(0)), probe.describe)
	})
})
