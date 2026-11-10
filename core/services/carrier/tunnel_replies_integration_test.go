package carrier_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The three callers of request and reply between processes, on the tunnel
// carrier, through the holders of replicas that each run their own HTTP server
// and peer link over one database.
var _ = Describe("Request and reply between replicas on the tunnel carrier, end to end", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		dsn      string
		clusterR *cluster.Registry
		nodeReg  *nodes.NodeRegistry

		mu      sync.Mutex
		members []string

		front, b, c *replica
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		var err error
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		withChatWorkers := func(o *carrier.TunnelOptions) {
			o.Queues = map[string]carrier.QueueMembers{
				messaging.SubjectChatStart: func(context.Context) ([]string, error) {
					mu.Lock()
					defer mu.Unlock()
					return slices.Clone(members), nil
				},
			}
		}
		front = startReplica(ctx, "frontend", db, dsn, clusterR, nodeReg, withChatWorkers)
		b = startReplica(ctx, "replica-b", db, dsn, clusterR, nodeReg)
		c = startReplica(ctx, "replica-c", db, dsn, clusterR, nodeReg)
		// A replica that is in the deployment and does not answer.
		Expect(clusterR.RegisterPeer(ctx, "replica-dead", "test", 0, "", "127.0.0.1:1", cluster.NewPeerCredential().Hash())).To(Succeed())
	})

	setMembers := func(ids ...string) {
		mu.Lock()
		defer mu.Unlock()
		members = ids
	}

	Describe("the chat start", func() {
		var calls map[string]*atomic.Int32

		serveStarts := func(r *replica, reply func() workerctl.ChatStartReply) {
			GinkgoHelper()
			n := &atomic.Int32{}
			calls[r.id] = n
			_, err := nodes.NewNATSChatStartServer(r.bus).Serve(func(context.Context, workerctl.ChatStartRequest) workerctl.ChatStartReply {
				n.Add(1)
				return reply()
			})
			Expect(err).ToNot(HaveOccurred())
		}
		start := func(timeout time.Duration) (*workerctl.ChatStartReply, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return nodes.NewNATSChatStarter(front.bus).StartChat(ctx, workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1", Text: "go"})
		}

		BeforeEach(func() { calls = map[string]*atomic.Int32{} })

		It("reaches a chat worker over the peer link", func() {
			serveStarts(b, func() workerctl.ChatStartReply { return workerctl.ChatStartReply{Started: true} })
			setMembers("replica-b")
			reply, err := start(5 * time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Started).To(BeTrue())
			Expect(front.sessions.Holds("replica-b")).To(BeFalse())
			Expect(b.sessions.Holds("frontend")).To(BeTrue(), "the start crossed the link from the frontend")
		})

		It("runs nothing anywhere and lets the frontend run the turn when no chat worker is live", func() {
			setMembers()
			_, err := start(5 * time.Second)
			Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeTrue(), "got %v", err)
		})

		It("moves past a worker that cannot be reached and a full worker, to one that starts the turn", func() {
			serveStarts(b, func() workerctl.ChatStartReply { return workerctl.ChatStartReply{Error: "full", Full: true} })
			serveStarts(c, func() workerctl.ChatStartReply { return workerctl.ChatStartReply{Started: true} })
			setMembers("replica-dead", "replica-b", "replica-c")
			for range 6 {
				reply, err := start(10 * time.Second)
				Expect(err).ToNot(HaveOccurred())
				Expect(reply.Started).To(BeTrue())
			}
			Expect(calls["replica-c"].Load()).To(BeEquivalentTo(6), "every start ran once, on the worker with room")
		})

		It("reports that the start reached no worker when the only one cannot be reached", func() {
			setMembers("replica-dead")
			_, err := start(5 * time.Second)
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue(), "got %v", err)
			Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeFalse())
		})

		It("does not send a start whose answer did not come to a second worker", func() {
			hold := make(chan struct{})
			DeferCleanup(func() { close(hold) })
			serveStarts(b, func() workerctl.ChatStartReply { <-hold; return workerctl.ChatStartReply{Started: true} })
			serveStarts(c, func() workerctl.ChatStartReply { <-hold; return workerctl.ChatStartReply{Started: true} })
			setMembers("replica-b", "replica-c")
			_, err := start(500 * time.Millisecond)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeFalse(), "the worker may have started the turn")
			Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeFalse())
			Expect(calls["replica-b"].Load() + calls["replica-c"].Load()).To(BeEquivalentTo(1))
		})
	})

	Describe("the replay of chat events", func() {
		It("asks the replica that runs the turn for a full page of events", func() {
			page := make([]session.Event, 0, 64)
			for i := range 64 {
				page = append(page, session.Event{ID: "run." + string(rune('a'+i%26)), Kind: session.KindText, Text: strings.Repeat("x", 7<<10)})
			}
			stop, err := studio.NewReplayCarrier(b.bus, nil).Serve("replica-b", func(req studio.ReplayRequest) studio.ReplayReply {
				return studio.ReplayReply{Events: page, More: req.After == ""}
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(stop)

			rep, err := studio.NewReplayCarrier(front.bus, nil).Ask("replica-b", studio.ReplayRequest{Key: "k"})
			Expect(err).ToNot(HaveOccurred())
			Expect(rep.Events).To(HaveLen(64))
			Expect(rep.More).To(BeTrue())
		})

		It("fails an ask to a replica that does not answer, which the client reads as a resync", func() {
			_, err := studio.NewReplayCarrier(front.bus, nil).Ask("replica-dead", studio.ReplayRequest{Key: "k"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("the background jobs of a turn", func() {
		It("asks the replica that runs the job", func() {
			stop, err := studio.NewJobCarrier(b.bus, nil).Serve("replica-b", func(q bgjob.Request) bgjob.Reply {
				return bgjob.Reply{List: []bgjob.Info{{ID: "job-1", Owner: q.Owner, Chat: q.Chat}}}
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(stop)
			rep, err := studio.NewJobCarrier(front.bus, nil).Ask("replica-b", bgjob.Request{Action: bgjob.ActionList, Owner: "alice", Chat: "c1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(rep.List).To(HaveLen(1))
		})

		It("reads a replica that left as no route, and one that does not answer as a doubt", func() {
			ask := func(instance string) error {
				_, err := studio.NewJobCarrier(front.bus, nil).Ask(instance, bgjob.Request{Action: bgjob.ActionList, Owner: "alice", Chat: "c1"})
				return err
			}
			Expect(errors.Is(ask("replica-gone"), bgjob.ErrNoRoute)).To(BeTrue(), "a replica that is not in the deployment left it")
			Expect(errors.Is(ask("replica-c"), bgjob.ErrNoRoute)).To(BeTrue(), "a replica that serves no jobs has none")

			err := ask("replica-dead")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, bgjob.ErrNoRoute)).To(BeFalse(), "a replica that does not answer has not left: %v", err)
			Expect(bgjob.Gone(err)).To(BeFalse())
		})
	})
})
