// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("studio background work on the add-on owner", func() {
	newApp := func(opts ...config.AppOption) *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		base := []config.AppOption{config.WithContext(ctx), config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithDataPath(GinkgoT().TempDir())}
		app, err := New(append(base, opts...)...)
		if app != nil {
			DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		}
		Expect(err).NotTo(HaveOccurred())
		return app
	}

	It("runs connector polling, triggers, loops and agent jobs on a combined or frontend process", func() {
		for _, c := range []config.AddonConfig{{}, {Frontend: true}} {
			app := newApp(config.WithAddons(c))
			Expect(app.studioConnectorsStop).NotTo(BeNil(), "the connector loop did not start")
			Expect(app.studioTriggersStop).NotTo(BeNil(), "the trigger loop did not start")
			Expect(app.studioLoopsStop).NotTo(BeNil(), "the loop host did not start")
			Expect(app.studioJobsStop).NotTo(BeNil(), "the agent job host did not start")
		}
	})

	It("runs none of them on a dedicated add-on owner, which keeps the connector authority and inbox", func() {
		app := newApp(config.WithAddons(config.AddonConfig{DedicatedOwner: true}))
		Expect(app.StudioService()).NotTo(BeNil())
		Expect(app.studioConnectorsStop).To(BeNil(), "the owner polls connectors")
		Expect(app.studioTriggersStop).To(BeNil(), "the owner runs triggers")
		Expect(app.studioLoopsStop).To(BeNil(), "the owner runs loops")
		Expect(app.studioJobsStop).To(BeNil(), "the owner runs agent jobs")
		// Frontends reach the connector credentials and add-ons push messages
		// through the manager, so it stays.
		Expect(app.StudioConnectors()).NotTo(BeNil())
	})
})

var _ = Describe("connector.inbound on the add-on owner", func() {
	newBroker := func() *hostops.Broker {
		return hostops.New(hostops.NewInFlight(), nil, contract.DefaultLimits())
	}

	It("is registered and refuses a pushed message on a dedicated owner", func() {
		b := newBroker()
		a := &Application{
			applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{DedicatedOwner: true}},
			addonHostOps:      &addonruntime.HostOps{Broker: b},
			studioConnectors:  &studio.Connectors{},
		}
		Expect(a.RegisterAddonConnector()).To(Succeed())
		Expect(b.Ops()).To(ContainElement(contract.ConnectorInboundOp))
		_, err := refuseConnectorInbound(context.Background(), hostops.Call{})
		Expect(err).To(MatchError(hostops.ErrForbidden))
	})

	It("takes the message into the chats on a combined process", func() {
		b := newBroker()
		a := &Application{
			applicationConfig: &config.ApplicationConfig{},
			addonHostOps:      &addonruntime.HostOps{Broker: b},
			studioConnectors:  &studio.Connectors{},
		}
		Expect(a.RegisterAddonConnector()).To(Succeed())
		Expect(b.Ops()).To(ContainElement(contract.ConnectorInboundOp))
	})
})
