package distributed_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A frontend and a chat worker on a real NATS server, each with its own
// connection, sharing one chats store. The FakeBus tests next to the studio
// service pin the behaviour; this checks the NATS carrier keeps it: the start
// is a queue request/reply, and the events and the stop cross processes.
var _ = Describe("Chat workers over NATS", Label("Distributed"), func() {
	var (
		infra           *TestInfra
		workerNC        *messaging.Client
		st              store.Store
		front, worker   *studio.Service
		hFront, hWorker *studiotest.GateHarness
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
		front = newService(infra.NC, hFront, nodes.NewNATSChatStarter(infra.NC))
		worker = newService(workerNC, hWorker, nil)
	})

	turnOf := func(id string) store.TurnInfo {
		info, err := front.Turn(context.Background(), "alice", id)
		Expect(err).ToNot(HaveOccurred())
		return info
	}

	serve := func() messaging.Subscription {
		sub, err := nodes.NewNATSChatStartServer(workerNC).Serve(worker.ServeStart)
		Expect(err).ToNot(HaveOccurred())
		FlushNATS(workerNC)
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
		FlushNATS(workerNC)
		rec, err := front.Create(context.Background(), "alice", studio.CreateInput{Model: "m"})
		Expect(err).ToNot(HaveOccurred())
		res, err := front.SendBackground(context.Background(), "alice", rec.ID, "go", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Started).To(BeTrue())
		Eventually(hFront.Sent).Should(Equal([]string{"go"}))
		Expect(hWorker.Sent()).To(BeEmpty())
	})
})
