package bgjob_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

// memCarrier is a carrier in memory: an instance without a handler has no
// route, as on a bus with no subscriber.
type memCarrier struct {
	mu       sync.Mutex
	handlers map[string]func(bgjob.Request) bgjob.Reply
}

func (m *memCarrier) Ask(instance string, req bgjob.Request) (bgjob.Reply, error) {
	m.mu.Lock()
	h := m.handlers[instance]
	m.mu.Unlock()
	if h == nil {
		return bgjob.Reply{}, bgjob.ErrNoRoute
	}
	raw, _ := json.Marshal(req) // the wire format is JSON
	var back bgjob.Request
	_ = json.Unmarshal(raw, &back)
	rep := h(back)
	raw, _ = json.Marshal(rep)
	var out bgjob.Reply
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

func (m *memCarrier) Serve(instance string, h func(bgjob.Request) bgjob.Reply) (func(), error) {
	m.mu.Lock()
	m.handlers[instance] = h
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.handlers, instance); m.mu.Unlock() }, nil
}

var _ = Describe("Carrier", func() {
	var (
		regA, regB *bgjob.Registry
		a, b       *bgjob.Router
		ctx        = context.Background()
	)
	BeforeEach(func() {
		regA = bgjob.New(bgjob.Options{Instance: "A"})
		regB = bgjob.New(bgjob.Options{Instance: "B"})
		c := &memCarrier{handlers: map[string]func(bgjob.Request) bgjob.Reply{}}
		a = &bgjob.Router{Local: regA, Self: "A", Carrier: c}
		b = &bgjob.Router{Local: regB, Self: "B", Carrier: c}
		stopA, err := a.Serve()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(stopA)
		stopB, err := b.Serve()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(stopB)
	})

	It("reads, writes to and kills a job of A from B", func() {
		p := newProc(syscall.SIGTERM)
		j, err := regA.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1", Label: "x"})
		Expect(err).NotTo(HaveOccurred())
		j.Run(p, func() {}, nil)
		_, _ = j.Output().Write([]byte("hello"))

		rep, err := b.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionRead, Owner: "alice", Chat: "c1", ID: j.ID()})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(rep.Obs.Output)).To(Equal("hello"))
		rep, err = b.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionRead, Owner: "alice", Chat: "c1", ID: j.ID()})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Obs.Output).To(BeEmpty(), "the read place is on A")

		go func() { buf := make([]byte, 8); _, _ = j.Stdin().Read(buf) }()
		rep, err = b.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionInput, Owner: "alice", Chat: "c1", ID: j.ID(), Input: "go\n"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Wrote).To(Equal(3))

		rep, err = b.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionList, Owner: "alice", Chat: "c1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.List).To(HaveLen(1))

		_, err = b.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionKill, Owner: "alice", Chat: "c1", ID: j.ID(), Reason: bgjob.EndStopped})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bgjob.State { return j.Info().State }).Should(Equal(bgjob.StateKilled))

		_, err = b.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionInput, Owner: "alice", Chat: "c1", ID: j.ID(), Input: "x"})
		var ended *bgjob.EndedError
		Expect(errors.As(err, &ended)).To(BeTrue())
		Expect(ended.Info.End).To(Equal(bgjob.EndStopped))
	})

	It("answers unknown for another owner, another chat or a dropped job", func() {
		j, _ := regA.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
		j.Run(newProc(), func() {}, nil)
		for _, q := range []bgjob.Request{
			{Action: bgjob.ActionObserve, Owner: "bob", Chat: "c1", ID: j.ID()},
			{Action: bgjob.ActionKill, Owner: "alice", Chat: "c2", ID: j.ID()},
			{Action: bgjob.ActionRead, Owner: "alice", Chat: "c1", ID: "job-nope00"},
		} {
			_, err := b.Do(ctx, "A", q)
			Expect(err).To(MatchError(bgjob.ErrUnknown))
			Expect(bgjob.Gone(err)).To(BeTrue())
		}
		Expect(j.Info().State).To(Equal(bgjob.StateRunning))
	})

	It("says no route when the node left, and changes nothing on a timeout", func() {
		_, err := b.Do(ctx, "Z", bgjob.Request{Action: bgjob.ActionList, Owner: "alice", Chat: "c1"})
		Expect(errors.Is(err, bgjob.ErrNoRoute)).To(BeTrue())
		Expect(bgjob.Gone(err)).To(BeTrue())

		slow := &bgjob.Router{Local: regB, Self: "B", Carrier: failing{errors.New("timeout")}}
		_, err = slow.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionList})
		Expect(err).To(HaveOccurred())
		Expect(bgjob.Gone(err)).To(BeFalse(), "a timeout says nothing about the job")
	})

	It("reads a job on this node without the carrier, and serves the observer of an instance", func() {
		j, _ := regA.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
		j.Run(newProc(), func() {}, nil)
		_, _ = j.Output().Write([]byte("abc"))
		o, err := b.Observer("A").Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: j.ID(), Since: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(o.Output)).To(Equal("bc"))
		Expect(o.Tail).To(Equal("abc"))
		_, err = b.Observer("B").Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: j.ID()})
		Expect(err).To(MatchError(bgjob.ErrUnknown))

		alone := &bgjob.Router{Local: regA}
		l, err := alone.Observer("").List(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(HaveLen(1))
	})

	It("ends the jobs of a chat from another node", func() {
		j, _ := regA.Reserve(bgjob.Spec{Owner: "alice", Chat: "c1"})
		j.Run(newProc(syscall.SIGTERM), func() {}, nil)
		rep, err := b.Do(ctx, "A", bgjob.Request{Action: bgjob.ActionEndChat, Owner: "alice", Chat: "c1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Ended).To(Equal(1))
		Eventually(func() bgjob.EndReason { return j.Info().End }).Should(Equal(bgjob.EndChatDeleted))
	})
})

type failing struct{ err error }

func (f failing) Ask(string, bgjob.Request) (bgjob.Reply, error) { return bgjob.Reply{}, f.err }
func (f failing) Serve(string, func(bgjob.Request) bgjob.Reply) (func(), error) {
	return func() {}, nil
}
