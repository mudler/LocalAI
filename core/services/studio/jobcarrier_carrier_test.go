package studio_test

import (
	"slices"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The carrier holder answers for a set that cannot carry the ask. That is a
// fact about the carrier and not about the node that runs the job, so the job
// stays live however many sweeps pass.
var _ = Describe("a job ask over a carrier that cannot carry it", func() {
	It("does not mark the job of a node that is still there lost", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		// A set with fan-out and no request and reply between processes.
		active := &atomic.Pointer[carrier.Set]{}
		active.Store(&carrier.Set{Name: cluster.CarrierTunnel, Broadcaster: testutil.NewFakeBus()})
		jobs := studio.NewJobCarrier(carrier.NewBroadcaster(active), nil)
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
