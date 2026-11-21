package studio_test

import (
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// undeliverable is a peer link that reaches no other process: every ask
// answers that it was not delivered, as for a peer that does not answer.
type undeliverable struct{}

func (undeliverable) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return noSubscription{}, nil
}

func (undeliverable) RequestInstance(instance, subject string, _ []byte, _ time.Duration) ([]byte, error) {
	return nil, fmt.Errorf("%w: the peer %s does not answer (%s)", messaging.ErrNotDelivered, instance, subject)
}

type noSubscription struct{}

func (noSubscription) Unsubscribe() error { return nil }

// The peer link answers that the ask reached no process. That is a fact about
// the link and not about the node that runs the job, so the job stays live
// however many sweeps pass.
var _ = Describe("a job ask that the peer link cannot deliver", func() {
	It("does not mark the job of a node that is still there lost", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		jobs := studio.NewJobCarrier(undeliverable{}, nil)
		clk := &tclock{t: time.Now()}
		files := newFiles()
		mk := func(instance string) jobChat {
			host := newFakeJobHost(instance)
			m := "on"
			svc, h := newToolService(jobScript, func(d *studio.Deps) {
				d.Store, d.Files, d.HostExec = st, files, host
				d.HostExecMode = func() string { return m }
				d.Instance, d.JobCarrier = instance, jobs
				d.Clock = clk.Now
			})
			return jobChat{svc: svc, h: h, host: host, mode: &m}
		}
		a, b := mk("A"), mk("B")

		id := create(a.svc, bob)
		a.say(bob, id, "start build run")
		job := a.stubs(bob, id)[0].ID

		for i := range 3 {
			Expect(b.say(bob, id, "read "+job)).To(Equal("Could not reach the node that runs "+job+"; try again."), "ask %d", i+1)
			clk.Advance(bgjob.SweepEvery)
		}
		Expect(b.say(bob, id, "read "+job)).To(Equal("Could not reach the node that runs " + job + "; try again."))
		stubs := b.stubs(bob, id)
		Expect(stubs[slices.IndexFunc(stubs, func(s session.JobStub) bool { return s.ID == job })].State).To(Equal(bgjob.StateRunning))
	})
})
