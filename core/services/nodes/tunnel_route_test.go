package nodes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The tunnel route a worker reports", func() {
	It("keeps the transports and paths it knows and drops the rest", func() {
		Expect(parseTunnelRoute("websocket", "relayed", true)).To(Equal(TunnelRoute{Transport: TunnelWebSocket}))
		Expect(parseTunnelRoute("p2p", "hole_punched", true)).To(Equal(TunnelRoute{Transport: TunnelP2P, Path: "hole_punched"}))
		Expect(parseTunnelRoute("p2p", "relayed", true)).To(Equal(TunnelRoute{Transport: TunnelP2P, Path: "relayed", Limited: true}))
		Expect(parseTunnelRoute("p2p", "made-up", false)).To(Equal(TunnelRoute{Transport: TunnelP2P}))
		Expect(parseTunnelRoute("carrier-pigeon", "direct", false)).To(Equal(TunnelRoute{}))
	})

	It("is part of the route report of a heartbeat", func() {
		rep, ok := HeartbeatUpdate{Routes: []string{"tunnel"}, Attached: []string{"tunnel"}, TunnelTransport: "p2p", TunnelPath: "direct"}.RouteReport()
		Expect(ok).To(BeTrue())
		Expect(rep.Tunnel).To(Equal(TunnelRoute{Transport: TunnelP2P, Path: "direct"}))
		Expect(rep.Attached).To(ConsistOf(RouteTunnelID))
		Expect(rep.Follow).To(ConsistOf(RouteTunnelID))
	})

	It("reads the routes a worker was given, the ones it holds and why the others are not held", func() {
		rep, ok := HeartbeatUpdate{
			Routes: []string{"nats", "tunnel", "carrier-pigeon"}, Attached: []string{"tunnel"},
			RouteError: "nats: connecting", TunnelTransport: "websocket",
		}.RouteReport()
		Expect(ok).To(BeTrue())
		Expect(rep.Follow).To(Equal([]string{RouteNATS, RouteTunnelID}))
		Expect(rep.Attached).To(Equal([]string{RouteTunnelID}))
		Expect(rep.FollowError).To(Equal("nats: connecting"))
		Expect(rep.Tunnel).To(Equal(TunnelRoute{Transport: TunnelWebSocket}))
	})

	It("is no report when the worker names no route", func() {
		_, ok := HeartbeatUpdate{Attached: []string{"nats"}}.RouteReport()
		Expect(ok).To(BeFalse())
	})
})
