package tunnel_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Registry", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		clusterR *cluster.Registry
		registry *tunnel.Registry
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())
		Expect(clusterR.Register(ctx, "replica-b", "test", 0, "")).To(Succeed())
		registry = tunnel.NewRegistry(clusterR, "replica-a")
	})

	owner := func(node string) (string, error) {
		id, _, err := clusterR.Owner(ctx, node)
		return id, err
	}

	Describe("the inference lane", func() {
		It("claims the node for this replica and holds the session", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)

			token, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())

			id, epoch, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal("replica-a"))
			Expect(epoch).To(Equal(token))
			Expect(registry.Holds("w1")).To(BeTrue())
		})

		It("refuses a nil session and an unknown lane without touching the table", func() {
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, nil)
			Expect(err).To(HaveOccurred())
			frontend, _ := sessionPair(tunnel.LaneInference)
			_, err = registry.Attach(ctx, "w1", tunnel.Lane("express"), frontend)
			Expect(err).To(HaveOccurred())
			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
			Expect(registry.Holds("w1")).To(BeFalse())
		})

		It("releases the claim and drops the entry on Detach", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			token, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())

			registry.Detach("w1", tunnel.LaneInference, token)

			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
			Expect(registry.Holds("w1")).To(BeFalse())
		})

		It("ignores a Detach with a token that is not the live one", func() {
			first, _ := sessionPair(tunnel.LaneInference)
			second, _ := sessionPair(tunnel.LaneInference)
			stale, err := registry.Attach(ctx, "w1", tunnel.LaneInference, first)
			Expect(err).ToNot(HaveOccurred())
			live, err := registry.Attach(ctx, "w1", tunnel.LaneInference, second)
			Expect(err).ToNot(HaveOccurred())
			Expect(live).ToNot(Equal(stale))

			registry.Detach("w1", tunnel.LaneInference, stale)

			id, epoch, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal("replica-a"))
			Expect(epoch).To(Equal(live))
			Expect(registry.Holds("w1")).To(BeTrue())
		})

		It("closes the session that a new dial replaced, and keeps the new one", func() {
			first, firstWorker := sessionPair(tunnel.LaneInference)
			second, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, first)
			Expect(err).ToNot(HaveOccurred())
			_, err = registry.Attach(ctx, "w1", tunnel.LaneInference, second)
			Expect(err).ToNot(HaveOccurred())

			Eventually(first.CloseChan()).Should(BeClosed())
			Eventually(firstWorker.CloseChan()).Should(BeClosed())
			Expect(second.IsClosed()).To(BeFalse())
		})

		It("does not close a session that is attached twice", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			_, err = registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			Expect(frontend.IsClosed()).To(BeFalse())
		})

		It("leaves this replica as it was when the claim fails", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			canceled, cancel := context.WithCancel(ctx)
			cancel()

			_, err := registry.Attach(canceled, "w1", tunnel.LaneInference, frontend)
			Expect(err).To(HaveOccurred())
			Expect(registry.Holds("w1")).To(BeFalse())
			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("keeps the entry whose claim the row carries when two dials race", func() {
			const dials = 8
			var wg sync.WaitGroup
			tokens := make([]int64, dials)
			sessions := make([]*tunnel.Session, dials)
			for i := range sessions {
				sessions[i], _ = sessionPair(tunnel.LaneInference)
			}
			for i := range dials {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					tok, err := registry.Attach(ctx, "w1", tunnel.LaneInference, sessions[i])
					Expect(err).ToNot(HaveOccurred())
					tokens[i] = tok
				}()
			}
			wg.Wait()

			_, epoch, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			// Exactly one session survives, and it is the one whose claim won
			// the row, so its Detach releases the row.
			alive := -1
			for i, s := range sessions {
				if !s.IsClosed() {
					Expect(alive).To(Equal(-1), "two sessions are still open")
					alive = i
				}
			}
			Expect(alive).ToNot(Equal(-1))
			Expect(tokens[alive]).To(Equal(epoch))

			registry.Detach("w1", tunnel.LaneInference, tokens[alive])
			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})
	})

	Describe("Open", func() {
		It("reports ErrNotOwner for a node that is not held here", func() {
			_, err := registry.Open(ctx, "ghost", tunnel.LaneInference)
			Expect(err).To(MatchError(tunnel.ErrNotOwner))
		})

		It("opens a stream that reaches the worker", func() {
			frontend, worker := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			serveStreams(worker, echoTCP)

			st, err := registry.Open(ctx, "w1", tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			_, err = st.Write([]byte("ping"))
			Expect(err).ToNot(HaveOccurred())
			got := make([]byte, 4)
			_, err = io.ReadFull(st, got)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("ping"))
			Expect(st.Close()).To(Succeed())
		})

		It("reports a session that ended under a held entry as itself and not as ErrNotOwner", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			Expect(frontend.Close()).To(Succeed())

			_, err = registry.Open(ctx, "w1", tunnel.LaneInference)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeFalse())
			id, err := owner("w1")
			Expect(err).ToNot(HaveOccurred(), "a failed open must not release the claim")
			Expect(id).To(Equal("replica-a"))
			Expect(registry.Disconnect("w1")).To(BeTrue(), "a failed open must not remove the entry")
		})

		It("blames the caller and not the tunnel when the budget of the caller is spent", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			Expect(frontend.Close()).To(Succeed())

			spent, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			_, err = registry.Open(spent, "w1", tunnel.LaneInference)
			Expect(err).To(MatchError(context.DeadlineExceeded))
			Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeFalse())
		})
	})

	Describe("the bulk lane", func() {
		attachBoth := func() (inference, bulk, inferenceWorker, bulkWorker *tunnel.Session, infToken, bulkToken int64) {
			GinkgoHelper()
			inference, inferenceWorker = sessionPair(tunnel.LaneInference)
			bulk, bulkWorker = sessionPair(tunnel.LaneBulk)
			var err error
			infToken, err = registry.Attach(ctx, "w1", tunnel.LaneInference, inference)
			Expect(err).ToNot(HaveOccurred())
			bulkToken, err = registry.Attach(ctx, "w1", tunnel.LaneBulk, bulk)
			Expect(err).ToNot(HaveOccurred())
			return
		}

		It("is held under the claim of the inference lane, without a claim of its own", func() {
			_, _, _, _, infToken, _ := attachBoth()
			_, epoch, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(epoch).To(Equal(infToken))
		})

		It("is refused with ErrNotOwner when this replica does not hold the inference lane", func() {
			bulk, _ := sessionPair(tunnel.LaneBulk)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneBulk, bulk)
			Expect(err).To(MatchError(tunnel.ErrNotOwner))
			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection), "a bulk session must not claim the node")
		})

		It("is refused when the inference session has ended", func() {
			inference, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, inference)
			Expect(err).ToNot(HaveOccurred())
			Expect(inference.Close()).To(Succeed())
			bulk, _ := sessionPair(tunnel.LaneBulk)
			_, err = registry.Attach(ctx, "w1", tunnel.LaneBulk, bulk)
			Expect(err).To(MatchError(tunnel.ErrNotOwner))
		})

		It("opens a stream on the lane that is asked for", func() {
			_, _, inferenceWorker, bulkWorker, _, _ := attachBoth()
			accepted := make(chan string, 2)
			serve := func(name string, s *tunnel.Session) {
				go func() {
					for {
						st, err := s.AcceptStream()
						if err != nil {
							return
						}
						accepted <- name
						_ = st.Close()
					}
				}()
			}
			serve("inference", inferenceWorker)
			serve("bulk", bulkWorker)

			st, err := registry.Open(ctx, "w1", tunnel.LaneBulk)
			Expect(err).ToNot(HaveOccurred())
			_, err = st.Write([]byte("x"))
			Expect(err).ToNot(HaveOccurred())
			Eventually(accepted).Should(Receive(Equal("bulk")))

			st, err = registry.Open(ctx, "w1", tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			_, err = st.Write([]byte("x"))
			Expect(err).ToNot(HaveOccurred())
			Eventually(accepted).Should(Receive(Equal("inference")))
		})

		It("falls back to the inference lane when the node has no bulk session", func() {
			inference, worker := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, inference)
			Expect(err).ToNot(HaveOccurred())
			serveStreams(worker, echoTCP)

			st, err := registry.Open(ctx, "w1", tunnel.LaneBulk)
			Expect(err).ToNot(HaveOccurred())
			_, err = st.Write([]byte("ok"))
			Expect(err).ToNot(HaveOccurred())
			got := make([]byte, 2)
			_, err = io.ReadFull(st, got)
			Expect(err).ToNot(HaveOccurred())
		})

		It("does not fall back when the caller asks for the bulk lane only", func() {
			inference, worker := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, inference)
			Expect(err).ToNot(HaveOccurred())
			serveStreams(worker, echoTCP)

			_, err = registry.Open(ctx, "w1", tunnel.LaneBulk, tunnel.WithoutFallback())
			Expect(err).To(MatchError(tunnel.ErrNoBulkSession))
			Expect(err).ToNot(MatchError(tunnel.ErrNotOwner), "the node is held here; only its bulk lane is missing")
		})

		It("does not fall back when the bulk session has ended", func() {
			_, bulk, _, _, _, _ := attachBoth()
			Expect(bulk.Close()).To(Succeed())

			_, err := registry.Open(ctx, "w1", tunnel.LaneBulk, tunnel.WithoutFallback())
			Expect(err).To(MatchError(tunnel.ErrNoBulkSession))
		})

		It("still opens the bulk lane when it is there, and ignores the option on the inference lane", func() {
			_, _, inferenceWorker, bulkWorker, _, _ := attachBoth()
			serveStreams(inferenceWorker, echoTCP)
			serveStreams(bulkWorker, echoTCP)

			st, err := registry.Open(ctx, "w1", tunnel.LaneBulk, tunnel.WithoutFallback())
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Close()).To(Succeed())
			st, err = registry.Open(ctx, "w1", tunnel.LaneInference, tunnel.WithoutFallback())
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Close()).To(Succeed())
		})

		It("falls back to the inference lane when the bulk session has ended", func() {
			_, bulk, inferenceWorker, _, _, _ := attachBoth()
			serveStreams(inferenceWorker, echoTCP)
			Expect(bulk.Close()).To(Succeed())

			st, err := registry.Open(ctx, "w1", tunnel.LaneBulk)
			Expect(err).ToNot(HaveOccurred())
			_, err = st.Write([]byte("ok"))
			Expect(err).ToNot(HaveOccurred())
			got := make([]byte, 2)
			_, err = io.ReadFull(st, got)
			Expect(err).ToNot(HaveOccurred())
		})

		It("closes the session that a new bulk dial replaced", func() {
			_, first, _, _, _, _ := attachBoth()
			second, _ := sessionPair(tunnel.LaneBulk)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneBulk, second)
			Expect(err).ToNot(HaveOccurred())
			Eventually(first.CloseChan()).Should(BeClosed())
			Expect(second.IsClosed()).To(BeFalse())
		})

		It("leaves the inference lane and the claim alone when the bulk lane is detached", func() {
			inference, _, _, _, infToken, bulkToken := attachBoth()
			registry.Detach("w1", tunnel.LaneBulk, bulkToken)

			Expect(inference.IsClosed()).To(BeFalse())
			_, epoch, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(epoch).To(Equal(infToken))
		})

		It("ignores a bulk Detach with the token of an attachment that was replaced", func() {
			_, _, _, _, _, staleBulk := attachBoth()
			second, _ := sessionPair(tunnel.LaneBulk)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneBulk, second)
			Expect(err).ToNot(HaveOccurred())

			registry.Detach("w1", tunnel.LaneBulk, staleBulk)
			Expect(second.IsClosed()).To(BeFalse())

			st, err := registry.Open(ctx, "w1", tunnel.LaneBulk)
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Close()).To(Succeed())
		})

		It("closes the bulk session with the inference lane that it belongs to", func() {
			_, bulk, _, _, infToken, _ := attachBoth()
			registry.Detach("w1", tunnel.LaneInference, infToken)
			Eventually(bulk.CloseChan()).Should(BeClosed())
		})

		It("closes the bulk session of an attachment that a new inference dial replaced", func() {
			_, bulk, _, _, _, _ := attachBoth()
			again, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, again)
			Expect(err).ToNot(HaveOccurred())
			Eventually(bulk.CloseChan()).Should(BeClosed())
		})
	})

	Describe("Disconnect", func() {
		It("ends both sessions of a node, releases the claim and drops the entry", func() {
			inference, bulk, inferenceWorker, bulkWorker, _, _ := func() (a, b, c, d *tunnel.Session, e, f int64) {
				a, c = sessionPair(tunnel.LaneInference)
				b, d = sessionPair(tunnel.LaneBulk)
				_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, a)
				Expect(err).ToNot(HaveOccurred())
				_, err = registry.Attach(ctx, "w1", tunnel.LaneBulk, b)
				Expect(err).ToNot(HaveOccurred())
				return
			}()

			Expect(registry.Disconnect("w1")).To(BeTrue())

			Eventually(inference.CloseChan()).Should(BeClosed())
			Eventually(bulk.CloseChan()).Should(BeClosed())
			Eventually(inferenceWorker.CloseChan()).Should(BeClosed())
			Eventually(bulkWorker.CloseChan()).Should(BeClosed())
			_, err := owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
			_, err = registry.Open(ctx, "w1", tunnel.LaneInference)
			Expect(err).To(MatchError(tunnel.ErrNotOwner))
		})

		It("says false for a node that is not held here and touches nothing", func() {
			other, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w2", tunnel.LaneInference, other)
			Expect(err).ToNot(HaveOccurred())

			Expect(registry.Disconnect("w1")).To(BeFalse())
			Expect(registry.Holds("w2")).To(BeTrue())
		})

		It("leaves the claim alone when the late Detach of the handler arrives", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			token, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.Disconnect("w1")).To(BeTrue())

			// The goroutine that accepted the session detaches when it ends.
			registry.Detach("w1", tunnel.LaneInference, token)

			// A worker that dialled again in between keeps its claim.
			again, _ := sessionPair(tunnel.LaneInference)
			live, err := registry.Attach(ctx, "w1", tunnel.LaneInference, again)
			Expect(err).ToNot(HaveOccurred())
			registry.Detach("w1", tunnel.LaneInference, token)
			_, epoch, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(epoch).To(Equal(live))
		})
	})

	Describe("Close", func() {
		It("ends every session, releases every claim and refuses a later attach", func() {
			a, aWorker := sessionPair(tunnel.LaneInference)
			b, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, a)
			Expect(err).ToNot(HaveOccurred())
			_, err = registry.Attach(ctx, "w2", tunnel.LaneInference, b)
			Expect(err).ToNot(HaveOccurred())

			registry.Close()

			Eventually(aWorker.CloseChan()).Should(BeClosed())
			for _, node := range []string{"w1", "w2"} {
				_, err := owner(node)
				Expect(err).To(MatchError(cluster.ErrNoConnection), node)
				Expect(registry.Holds(node)).To(BeFalse(), node)
			}
			late, _ := sessionPair(tunnel.LaneInference)
			_, err = registry.Attach(ctx, "w3", tunnel.LaneInference, late)
			Expect(err).To(MatchError(tunnel.ErrRegistryClosed))
			_, err = owner("w3")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("can be called twice and on a registry that holds nothing", func() {
			registry.Close()
			registry.Close()
		})
	})

	Describe("Reclaim", func() {
		It("claims the held tunnels again after this replica was swept", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			// A peer swept this replica: the rows are gone.
			Expect(db.Exec("DELETE FROM node_connections").Error).To(Succeed())
			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))

			n, err := registry.Reclaim(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(1))
			id, _, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal("replica-a"))
		})

		// The whole path: a peer reaps this replica, the membership loop registers
		// it again, and the loop asks the registry of tunnels to claim the held
		// sockets again. A replica that re-registers and does not do this serves
		// a worker that the table says nobody holds.
		It("claims the held tunnels again when the membership loop registers a reaped replica again", func() {
			membership := cluster.NewMembership(clusterR, "replica-a", "test")
			membership.SetReclaimer(registry)
			Expect(membership.Start(ctx)).To(Succeed())
			DeferCleanup(membership.Stop)

			frontend, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())

			Expect(db.Model(&cluster.Instance{}).Where("id = ?", "replica-a").
				Update("last_seen", gorm.Expr("now() - interval '1 hour'")).Error).To(Succeed())
			instances, cleared, err := clusterR.ReapStale(ctx, "replica-b", cluster.InstanceLiveness)
			Expect(err).ToNot(HaveOccurred())
			Expect(instances).To(Equal(int64(1)))
			Expect(cleared).To(Equal(int64(1)))
			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))

			Eventually(func() string {
				id, _ := owner("w1")
				return id
			}, 3*cluster.InstanceHeartbeat, 250*time.Millisecond).Should(Equal("replica-a"),
				"the replica registered again and left its held tunnel unclaimed")
		})

		It("lets the Detach of the attachment release the claim that Reclaim wrote", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			token, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			_, err = registry.Reclaim(ctx)
			Expect(err).ToNot(HaveOccurred())

			registry.Detach("w1", tunnel.LaneInference, token)
			_, err = owner("w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("skips a tunnel whose session is closed instead of taking the node back", func() {
			frontend, _ := sessionPair(tunnel.LaneInference)
			_, err := registry.Attach(ctx, "w1", tunnel.LaneInference, frontend)
			Expect(err).ToNot(HaveOccurred())
			// The worker went to the other replica.
			epochB, err := clusterR.Claim(ctx, "w1", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(frontend.Close()).To(Succeed())

			n, err := registry.Reclaim(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeZero())
			id, epoch, err := clusterR.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal("replica-b"))
			Expect(epoch).To(Equal(epochB))
		})
	})
})
