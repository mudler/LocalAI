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

	It("is part of the carrier report of a heartbeat", func() {
		rep, ok := HeartbeatUpdate{FollowCapabilities: []string{"tunnel"}, TunnelTransport: "p2p", TunnelPath: "direct"}.CarrierReport()
		Expect(ok).To(BeTrue())
		Expect(rep.Tunnel).To(Equal(TunnelRoute{Transport: TunnelP2P, Path: "direct"}))
	})
})
