package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The frontend runs a start itself only on ErrNoRoute. A timeout or a worker's
// refusal leaves it unknown whether a turn started, so they must never look
// like one: running the turn here as well would send the message twice.
var _ = Describe("Chat starter", func() {
	var (
		mc *scriptedMessagingClient
		cs *ChatStarter
	)

	BeforeEach(func() {
		mc = newScriptedMessagingClient()
		cs = NewChatStarter(subjectQueue{mc})
	})

	start := func(ctx context.Context) (*workerctl.ChatStartReply, error) {
		return cs.StartChat(ctx, workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1", Text: "go"})
	}

	It("reports no responders as ErrNoRoute without the carrier sentinel", func() {
		mc.scriptNoResponders(messaging.SubjectChatStart)
		_, err := start(context.Background())
		Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "got %v", err)
		Expect(errors.Is(err, nats.ErrNoResponders)).To(BeFalse())
	})

	It("does not report a timeout as ErrNoRoute", func() {
		mc.scriptErr(messaging.SubjectChatStart, nats.ErrTimeout)
		_, err := start(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
	})

	It("returns the worker's answer, a refusal included, with a nil error", func() {
		mc.scriptReply(messaging.SubjectChatStart, workerctl.ChatStartReply{Error: "studio: the user is not active"})
		reply, err := start(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Error).To(Equal("studio: the user is not active"))
	})

	It("lets a carrier that picks the worker move a start past a full worker, and keeps ErrNotDelivered as itself", func() {
		q := &pickingRequester{replies: [][]byte{mustJSON(workerctl.ChatStartReply{Error: "full", Full: true}), mustJSON(workerctl.ChatStartReply{Started: true})}}
		reply, err := NewChatStarter(q).StartChat(context.Background(), workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Started).To(BeTrue())
		Expect(q.movedOn).To(Equal([]bool{true, false}))

		q = &pickingRequester{err: fmt.Errorf("%w: no worker reached", messaging.ErrNotDelivered)}
		_, err = NewChatStarter(q).StartChat(context.Background(), workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1"})
		Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
		Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "a start that reached no worker is not a route that does not exist")
	})

	It("sends the request on the chat start subject", func() {
		mc.scriptReply(messaging.SubjectChatStart, workerctl.ChatStartReply{Started: true})
		reply, err := start(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Started).To(BeTrue())
		mc.mu.Lock()
		defer mc.mu.Unlock()
		Expect(mc.calls).To(HaveLen(1))
		Expect(mc.calls[0].Subject).To(Equal(messaging.SubjectChatStart))
		var sent workerctl.ChatStartRequest
		Expect(json.Unmarshal(mc.calls[0].Data, &sent)).To(Succeed())
		Expect(sent).To(Equal(workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1", Text: "go"}))
	})

	It("bounds the request by the context deadline, or the default budget without one", func() {
		mc.scriptReply(messaging.SubjectChatStart, workerctl.ChatStartReply{Started: true})
		_, err := start(context.Background())
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = start(ctx)
		Expect(err).ToNot(HaveOccurred())
		mc.mu.Lock()
		defer mc.mu.Unlock()
		Expect(mc.calls[0].Timeout).To(Equal(DefaultChatStartTimeout))
		Expect(mc.calls[1].Timeout).To(BeNumerically("<=", 5*time.Second))
	})

	It("waits for the configured budget when the context has no deadline", func() {
		mc.scriptReply(messaging.SubjectChatStart, workerctl.ChatStartReply{Started: true})
		cs = NewChatStarter(subjectQueue{mc}, WithChatStartTimeout(90*time.Second))
		_, err := start(context.Background())
		Expect(err).ToNot(HaveOccurred())
		mc.mu.Lock()
		defer mc.mu.Unlock()
		Expect(mc.calls[0].Timeout).To(Equal(90 * time.Second))
	})

	It("keeps the default budget for a zero or negative timeout", func() {
		mc.scriptReply(messaging.SubjectChatStart, workerctl.ChatStartReply{Started: true})
		for _, d := range []time.Duration{0, -time.Second} {
			cs = NewChatStarter(subjectQueue{mc}, WithChatStartTimeout(d))
			_, err := start(context.Background())
			Expect(err).ToNot(HaveOccurred())
		}
		mc.mu.Lock()
		defer mc.mu.Unlock()
		Expect(mc.calls).To(HaveLen(2))
		Expect(mc.calls[0].Timeout).To(Equal(DefaultChatStartTimeout))
		Expect(mc.calls[1].Timeout).To(Equal(DefaultChatStartTimeout))
	})
})

var _ = Describe("Chat start server", func() {
	var (
		bus *testutil.FakeBus
		srv *ChatStartServer
	)

	BeforeEach(func() {
		bus = testutil.NewFakeBus()
		srv = NewChatStartServer(bus)
	})

	It("serves the chat start subject as itself, in no queue group: the frontend picks the worker", func() {
		_, err := srv.Serve(func(context.Context, workerctl.ChatStartRequest) workerctl.ChatStartReply {
			return workerctl.ChatStartReply{}
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(bus.QueueGroups()).ToNot(HaveKey(messaging.SubjectChatStart))
		_, ok := bus.DeliverReply(messaging.SubjectChatStart, []byte("{}"))
		Expect(ok).To(BeTrue())
	})

	It("passes the request to the handler and replies with its answer", func() {
		var seen workerctl.ChatStartRequest
		var deadline bool
		_, err := srv.Serve(func(ctx context.Context, req workerctl.ChatStartRequest) workerctl.ChatStartReply {
			seen = req
			_, deadline = ctx.Deadline()
			return workerctl.ChatStartReply{PendingID: "p1"}
		})
		Expect(err).ToNot(HaveOccurred())
		data, _ := json.Marshal(workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1", Text: "go"})
		raw, ok := bus.DeliverReply(messaging.SubjectChatStart, data)
		Expect(ok).To(BeTrue())
		var reply workerctl.ChatStartReply
		Expect(json.Unmarshal(raw, &reply)).To(Succeed())
		Expect(reply).To(Equal(workerctl.ChatStartReply{PendingID: "p1"}))
		Expect(seen).To(Equal(workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1", Text: "go"}))
		Expect(deadline).To(BeTrue(), "a start must not run without a bound")
	})

	It("gives the handler the configured budget", func() {
		var left time.Duration
		_, err := NewChatStartServer(bus, WithChatStartTimeout(2*time.Minute)).Serve(func(ctx context.Context, _ workerctl.ChatStartRequest) workerctl.ChatStartReply {
			d, _ := ctx.Deadline()
			left = time.Until(d)
			return workerctl.ChatStartReply{}
		})
		Expect(err).ToNot(HaveOccurred())
		data, _ := json.Marshal(workerctl.ChatStartRequest{Principal: "alice", ChatID: "c1"})
		_, ok := bus.DeliverReply(messaging.SubjectChatStart, data)
		Expect(ok).To(BeTrue())
		Expect(left).To(BeNumerically(">", DefaultChatStartTimeout))
		Expect(left).To(BeNumerically("<=", 2*time.Minute))
	})

	It("answers undecodable bytes with a refusal and does not call the handler", func() {
		called := false
		_, err := srv.Serve(func(context.Context, workerctl.ChatStartRequest) workerctl.ChatStartReply {
			called = true
			return workerctl.ChatStartReply{Started: true}
		})
		Expect(err).ToNot(HaveOccurred())
		raw, ok := bus.DeliverReply(messaging.SubjectChatStart, []byte("{not json"))
		Expect(ok).To(BeTrue(), "a bad request must still be answered")
		var reply workerctl.ChatStartReply
		Expect(json.Unmarshal(raw, &reply)).To(Succeed())
		Expect(reply.Error).To(HavePrefix("unmarshal error: "))
		Expect(reply.Started).To(BeFalse())
		Expect(called).To(BeFalse())
	})

	It("names the subject when the bus refuses the subscription", func() {
		failing := NewChatStartServer(&failingSubscribeBus{FakeBus: bus, err: errors.New("boom")})
		_, err := failing.Serve(func(context.Context, workerctl.ChatStartRequest) workerctl.ChatStartReply {
			return workerctl.ChatStartReply{}
		})
		Expect(err).To(MatchError(ContainSubstring(messaging.SubjectChatStart)))
	})
})

// pickingRequester is a carrier that picks the member of a queue: it hands its
// replies to moveOn in order, as a carrier that tries one member after another.
type pickingRequester struct {
	*scriptedMessagingClient
	replies [][]byte
	err     error
	movedOn []bool
}

func (p *pickingRequester) RequestQueue(_ string, _ []byte, _ time.Duration, moveOn func([]byte) bool) ([]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	for _, r := range p.replies {
		next := moveOn(r)
		p.movedOn = append(p.movedOn, next)
		if !next {
			return r, nil
		}
	}
	return p.replies[len(p.replies)-1], nil
}

// subjectQueue is a queue of one member over a scripted client: the request
// goes to the subject, and a refusal has no other member to move on to.
type subjectQueue struct{ *scriptedMessagingClient }

func (q subjectQueue) RequestQueue(subject string, data []byte, timeout time.Duration, _ func([]byte) bool) ([]byte, error) {
	return q.Request(subject, data, timeout)
}
