package openai

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("webRTC ICE settings", func() {
	Describe("iceInterfaceFilter", func() {
		It("returns nil when no interfaces are configured", func() {
			Expect(iceInterfaceFilter(nil)).To(BeNil())
			Expect(iceInterfaceFilter([]string{})).To(BeNil())
		})

		It("admits only the configured interfaces", func() {
			f := iceInterfaceFilter([]string{"eth0", "wlan0"})
			Expect(f).NotTo(BeNil())
			Expect(f("eth0")).To(BeTrue())
			Expect(f("wlan0")).To(BeTrue())
			Expect(f("docker0")).To(BeFalse())
			Expect(f("veth123")).To(BeFalse())
		})
	})

	Describe("webRTCSettingEngine", func() {
		It("does not panic on a nil config", func() {
			Expect(func() { webRTCSettingEngine(nil) }).NotTo(Panic())
		})

		It("builds an engine with NAT 1:1 IPs and an interface filter configured", func() {
			cfg := &config.ApplicationConfig{
				WebRTCNAT1To1IPs:    []string{"192.168.1.10"},
				WebRTCICEInterfaces: []string{"eth0"},
			}
			Expect(func() { webRTCSettingEngine(cfg) }).NotTo(Panic())
		})
	})
})
