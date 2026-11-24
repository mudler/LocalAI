package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testRelayAddr = "/ip4/192.0.2.2/tcp/4001/p2p/12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN"

var _ = Describe("P2P settings", func() {
	It("defaults to mode off, public DHT, mDNS and relaying for paired devices in mode auto", func() {
		s := NewApplicationConfig().ToRuntimeSettings()
		Expect(*s.P2PMode).To(Equal("off"))
		Expect(*s.P2PPublicDHT).To(BeTrue())
		Expect(*s.P2PMDNS).To(BeTrue())
		Expect(*s.P2PRelayPaired).To(BeTrue())
		Expect(*s.P2PRelayMode).To(Equal("auto"))
		Expect(*s.P2PRelayRateLimit).To(BeZero())
	})

	It("refuses an unknown mode, relay mode, negative rate limit and relay without a peer ID", func() {
		o := NewApplicationConfig()
		bad, neg := "both", int64(-1)
		relays := []P2PRelay{{ID: "r", Addr: "/ip4/192.0.2.2/tcp/4001"}}
		o.ApplyRuntimeSettings(&RuntimeSettings{P2PMode: &bad, P2PRelayMode: &bad, P2PRelayRateLimit: &neg, P2PRelays: &relays})
		Expect(o.P2P).To(Equal(P2PSettings{}))
	})

	It("applies valid values", func() {
		o := NewApplicationConfig()
		mode, rm, off := "Connect", "on", false
		relays := []P2PRelay{{ID: "r", Addr: testRelayAddr, Label: "home"}}
		o.ApplyRuntimeSettings(&RuntimeSettings{P2PMode: &mode, P2PRelayMode: &rm, P2PPublicDHT: &off, P2PRelays: &relays})
		Expect(o.P2P.EffectiveMode()).To(Equal(P2PModeConnect))
		Expect(o.P2P.EffectiveRelayMode()).To(Equal(P2PRelayOn))
		Expect(o.P2P.DisablePublicDHT).To(BeTrue())
		Expect(o.P2P.Relays).To(Equal(relays))
	})

	It("takes the mode from the file when the flag kept its default, and from the flag otherwise", func() {
		share := "share"
		o := NewApplicationConfig(WithP2PMode("off"))
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{P2PMode: &share})
		Expect(o.P2P.EffectiveMode()).To(Equal(P2PModeShare))

		o = NewApplicationConfig(WithP2PMode("connect"))
		o.ApplyRuntimeSettingsAtStartup(&RuntimeSettings{P2PMode: &share})
		Expect(o.P2P.EffectiveMode()).To(Equal(P2PModeConnect))
	})

	It("checks relay lists", func() {
		Expect(ValidateP2PRelays([]P2PRelay{{ID: "a", Addr: testRelayAddr}, {ID: "a", Addr: testRelayAddr}})).To(MatchError(ContainSubstring("used twice")))
		Expect(ValidateP2PRelays([]P2PRelay{{Addr: testRelayAddr}})).To(HaveOccurred())
		Expect(ValidateP2PRelays([]P2PRelay{{ID: "a", Addr: testRelayAddr}})).To(Succeed())
	})

	It("needs a data path or an explicit identity file", func() {
		_, err := ResolveP2PIdentityPath("", "")
		Expect(err).To(MatchError(ErrP2PNoDataPath))
		Expect(ResolveP2PIdentityPath("", "/data")).To(Equal("/data/p2p/identity.key"))
		Expect(ResolveP2PIdentityPath("/k", "")).To(Equal("/k"))
	})
})
