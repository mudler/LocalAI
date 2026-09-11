// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The runtime symptom for a deferral whose cost changed between phases.
//
// Not refusing to start without an advertised address stays deferred on
// purpose: refusing would take out every single-host deployment. What is not
// deferred is telling the operator, repeatedly, that this replica is invisible
// and which workers that is costing - because the symptom it produces (a worker
// that 5xxs from most of the fleet) reads as a worker problem, and a single
// startup line has scrolled away long before anyone goes looking.
var _ = Describe("the alarm for a replica with no advertised address", func() {
	It("keeps firing for as long as the state lasts, and names the workers it costs", func() {
		// Repetition is the property. A one-shot alarm is the startup line
		// again, which is what was already there and was not enough.
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		alarms := make(chan []string, 8)
		go nagUnadvertisedReplica(ctx, func() []string { return []string{"w1", "w2"} },
			time.Millisecond, func(held []string) { alarms <- held })

		// Two, not one: the second is what a one-shot implementation fails.
		var first, second []string
		Eventually(alarms, "10s").Should(Receive(&first))
		Eventually(alarms, "10s").Should(Receive(&second))
		Expect(first).To(ConsistOf("w1", "w2"),
			"the workers this is costing are the answer to the question the symptom provokes")
		Expect(second).To(ConsistOf("w1", "w2"))
	})

	It("reads the held set on every tick rather than the one it started with", func() {
		// A replica accumulates tunnels while it runs, so an alarm bound to the
		// set at startup would name an empty list forever on exactly the
		// deployment where the cost is real.
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		workers := make(chan []string, 32)
		for range 32 {
			workers <- []string{"w-late"}
		}
		alarms := make(chan []string, 8)
		go nagUnadvertisedReplica(ctx, func() []string { return <-workers },
			time.Millisecond, func(held []string) { alarms <- held })

		var got []string
		Eventually(alarms, "10s").Should(Receive(&got))
		Expect(got).To(ConsistOf("w-late"))
	})

	It("stops when the process context ends", func() {
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			nagUnadvertisedReplica(ctx, func() []string { return nil }, time.Hour, func([]string) {})
			close(stopped)
		}()

		cancel()
		Eventually(stopped, "10s").Should(BeClosed())
	})
})
