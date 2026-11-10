package bgjob_test

import (
	"context"
	"errors"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nats-io/nats.go"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// natsBus makes the fake bus answer a missing subscriber as NATS does.
type natsBus struct{ *testutil.FakeBus }

func (b natsBus) Request(subject string, data []byte, d time.Duration) ([]byte, error) {
	out, err := b.FakeBus.Request(subject, data, d)
	if errors.Is(err, testutil.ErrNoResponders) {
		return nil, nats.ErrNoResponders
	}
	return out, err
}

var _ = Describe("Carrier", func() {
	var (
		bus        natsBus
		regA, regB *bgjob.Registry
		a, b       *bgjob.Router
		ctx        = context.Background()
	)
	BeforeEach(func() {
		bus = natsBus{testutil.NewFakeBus()}
		regA = bgjob.New(bgjob.Options{Instance: "A"})
		regB = bgjob.New(bgjob.Options{Instance: "B"})
		c := bgjob.NewBusCarrier(bus, nil)
		a = &bgjob.Router{Local: regA, Self: "A", Carrier: c}
		b = &bgjob.Router{Local: regB, Self: "B", Carrier: c}
		stopA, err := a.Serve()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(stopA)
		stopB, err := b.Serve()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(stopB)
	})

	It("uses a subject the bus serves", func() {
		Expect(messaging.ValidateSubject(bgjob.Subject("node/1:x"))).To(Succeed())
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
