package distributed_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// localPeers stands for the peer links between processes that run in this test
// process: a request reaches the handlers of the process it names.
type localPeers map[string]*tunnel.Replies

func (p localPeers) Request(ctx context.Context, target, subject string, data []byte, _ int, _ time.Duration) ([]byte, error) {
	r, ok := p[target]
	if !ok {
		return nil, tunnel.ErrNoResponder
	}
	return r.Dispatch(ctx, subject, data)
}

// A frontend and a chat worker that broadcast on a real NATS server, each with
// its own connection, sharing one chats store. The FakeBus tests next to the
// studio service pin the behaviour; this checks that it holds on a NATS
// cluster: the start goes over the peer link to the worker that the frontend
// picks, and the events and the stop cross processes on NATS.
var _ = Describe("Chat workers on a NATS cluster", Label("Distributed"), func() {
	var (
		infra           *TestInfra
		workerNC        *messaging.Client
		st              store.Store
		front, worker   *studio.Service
		hFront, hWorker *studiotest.GateHarness
		workerRR        messaging.ProcessReplies
	)

	newService := func(nc *messaging.Client, h *studiotest.GateHarness, workers studio.ChatWorkers) *studio.Service {
		svc, err := studio.New(studio.Deps{
			Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h,
			Bus: studio.NewBroadcastBus(nc, nil), HeartbeatEvery: 50 * time.Millisecond, Workers: workers,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		return svc
	}

	BeforeEach(func() {
		infra = SetupNATSOnly()
		var err error
		workerNC, err = messaging.New(infra.NatsURL)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(workerNC.Close)
		st, err = store.NewJSON("")
		Expect(err).ToNot(HaveOccurred())
		hFront, hWorker = studiotest.NewGateHarness(), studiotest.NewGateHarness()
		peers := localPeers{
			"frontend": tunnel.NewReplies("frontend", messaging.MaxBroadcastBytes),
			"worker":   tunnel.NewReplies("worker", messaging.MaxBroadcastBytes),
		}
		frontRR, err := carrier.NewPeerReplies(carrier.PeerRepliesOptions{
			Self: "frontend", Peers: peers, Local: peers["frontend"],
			Queues: map[string]carrier.QueueMembers{
				messaging.SubjectChatStart: func(context.Context) ([]string, error) { return []string{"worker"}, nil },
			},
		})
		Expect(err).ToNot(HaveOccurred())
		workerRR, err = carrier.NewPeerReplies(carrier.PeerRepliesOptions{Self: "worker", Peers: peers, Local: peers["worker"]})
		Expect(err).ToNot(HaveOccurred())
		front = newService(infra.NC, hFront, nodes.NewChatStarter(frontRR))
		worker = newService(workerNC, hWorker, nil)
	})

	turnOf := func(id string) store.TurnInfo {
		info, err := front.Turn(context.Background(), "alice", id)
		Expect(err).ToNot(HaveOccurred())
		return info
	}

	serve := func() messaging.Subscription {
		sub, err := nodes.NewChatStartServer(workerRR).Serve(worker.ServeStart)
		Expect(err).ToNot(HaveOccurred())
		return sub
	}

	It("runs a background start on the worker and streams its events to the frontend", func() {
		serve()
		rec, err := front.Create(context.Background(), "alice", studio.CreateInput{Model: "m"})
		Expect(err).ToNot(HaveOccurred())
		events, cancel, err := front.Events(context.Background(), "alice", rec.ID)
		Expect(err).ToNot(HaveOccurred())
		defer cancel()
		FlushNATS(infra.NC)

		res, err := front.SendBackground(context.Background(), "alice", rec.ID, "go", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Started).To(BeTrue())
		Eventually(hWorker.Sent).Should(Equal([]string{"go"}))
		hWorker.Gate <- struct{}{}

		var kinds []session.EventKind
		Eventually(func() []session.EventKind {
			for {
				select {
				case e := <-events:
					kinds = append(kinds, e.Kind)
				default:
					return kinds
				}
			}
		}, 10*time.Second).Should(ContainElements(session.KindText, session.KindTurnEnd))
		Eventually(func() store.TurnStatus { return turnOf(rec.ID).Last }).Should(Equal(store.TurnDone))
		Expect(hFront.Sent()).To(BeEmpty())
	})

	It("stops the worker's turn from the frontend", func() {
		serve()
		rec, err := front.Create(context.Background(), "alice", studio.CreateInput{Model: "m"})
		Expect(err).ToNot(HaveOccurred())
		_, err = front.SendBackground(context.Background(), "alice", rec.ID, "go", nil)
		Expect(err).ToNot(HaveOccurred())
		Eventually(hWorker.Sent).Should(HaveLen(1))
		Expect(front.Stop(context.Background(), "alice", rec.ID)).To(Succeed())
		Eventually(func() store.TurnStatus { return turnOf(rec.ID).Last }, 10*time.Second).Should(Equal(store.TurnStopped))
	})

	It("runs the start on the frontend when no worker listens", func() {
		sub := serve()
		Expect(sub.Unsubscribe()).To(Succeed())
		rec, err := front.Create(context.Background(), "alice", studio.CreateInput{Model: "m"})
		Expect(err).ToNot(HaveOccurred())
		res, err := front.SendBackground(context.Background(), "alice", rec.ID, "go", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Started).To(BeTrue())
		Eventually(hFront.Sent).Should(Equal([]string{"go"}))
		Expect(hWorker.Sent()).To(BeEmpty())
	})
})
