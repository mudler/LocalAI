// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// presenceStub is any source of absence. What it answers does not matter here:
// these specs are about whether a source was wired at all, which is the one
// property that has no other symptom.
type presenceStub struct{}

func (presenceStub) Presence(context.Context, string, time.Duration) (cluster.Presence, error) {
	return cluster.PresenceConnected, nil
}

// The guard on the two lines that connect the absence decision to production.
//
// Absence is read in exactly two places, and each reads it through one field
// assigned in a twenty-field construction literal in initDistributed. Deleting
// either assignment compiles, passes every suite in this repository, and
// returns the deployment to "absence is decided by nothing" without a log line.
// That is the failure this guard exists for, and these specs are what keep the
// guard honest: an assertion that never fails is not one.
var _ = Describe("stamping the absence wiring onto the scheduler's options", func() {
	It("gives the scheduler the deployment's source of absence", func() {
		reg := presenceStub{}

		opts := distributedSchedulerOptions(config.DistributedConfig{}, reg, nodes.SmartRouterOptions{})

		Expect(opts.Presence).To(Equal(nodes.NodePresenceReader(reg)))
	})

	It("gives it the operator's reconnect grace", func() {
		opts := distributedSchedulerOptions(
			config.DistributedConfig{WorkerReconnectGrace: 4 * time.Minute}, presenceStub{}, nodes.SmartRouterOptions{})

		Expect(opts.ReconnectGrace).To(Equal(4 * time.Minute))
	})

	It("falls back to the documented default when the operator set no grace", func() {
		opts := distributedSchedulerOptions(config.DistributedConfig{}, presenceStub{}, nodes.SmartRouterOptions{})

		Expect(opts.ReconnectGrace).To(Equal(config.DefaultWorkerReconnectGrace))
	})

	It("leaves every other option the caller built untouched", func() {
		// The negative control: a stamp that rebuilt the options would drop the
		// twenty fields the caller assembled, and the two assertions above
		// would still pass.
		opts := distributedSchedulerOptions(config.DistributedConfig{}, presenceStub{},
			nodes.SmartRouterOptions{GalleriesJSON: "[]", SharedModels: true})

		Expect(opts.GalleriesJSON).To(Equal("[]"))
		Expect(opts.SharedModels).To(BeTrue())
	})

	It("produces a scheduler that reads absence", func() {
		// The property the boot guard checks, asserted through the same call
		// initDistributed makes.
		router := nodes.NewSmartRouter(nil,
			distributedSchedulerOptions(config.DistributedConfig{}, presenceStub{}, nodes.SmartRouterOptions{}))

		Expect(router.ReadsAbsence()).To(BeTrue())
	})
})

var _ = Describe("the absence wiring a distributed deployment refuses to start without", func() {
	present := func() (*nodes.SmartRouter, *nodes.HealthMonitor) {
		router := nodes.NewSmartRouter(nil, nodes.SmartRouterOptions{Presence: presenceStub{}})
		health := nodes.NewHealthMonitor(nil, nil, time.Second, time.Minute, "", false, presenceStub{}, time.Minute)
		return router, health
	}

	It("accepts a deployment where both readers have a source", func() {
		router, health := present()
		Expect(requireAbsenceWiring(router, health)).To(Succeed())
	})

	It("refuses a scheduler built without one, and says what it would do", func() {
		_, health := present()
		blind := nodes.NewSmartRouter(nil, nodes.SmartRouterOptions{})

		err := requireAbsenceWiring(blind, health)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("scheduler"))
		Expect(err.Error()).To(ContainSubstring("never demote"))
	})

	It("refuses a health monitor built without one, and says what it would do", func() {
		router, _ := present()
		blind := nodes.NewHealthMonitor(nil, nil, time.Second, time.Minute, "", false, nil, 0)

		err := requireAbsenceWiring(router, blind)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("health monitor"))
		Expect(err.Error()).To(ContainSubstring("healthy indefinitely"))
	})

	// Each reader is named separately on purpose. One guard covering "at least
	// one of them" would accept a deployment that had lost the other, and the
	// two failures are different: the scheduler's places work on a dead worker,
	// the monitor's leaves it listed healthy while its models are unreachable.
	It("names the scheduler and the health monitor as separate requirements", func() {
		blindRouter := nodes.NewSmartRouter(nil, nodes.SmartRouterOptions{})
		blindHealth := nodes.NewHealthMonitor(nil, nil, time.Second, time.Minute, "", false, nil, 0)
		router, health := present()

		Expect(requireAbsenceWiring(blindRouter, health)).ToNot(Succeed())
		Expect(requireAbsenceWiring(router, blindHealth)).ToNot(Succeed())
	})
})
