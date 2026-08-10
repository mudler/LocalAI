package openai

import (
	"net"
	"runtime"

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
		It("uses pion's ephemeral-port behavior by default", func() {
			_, err := webRTCSettingEngine(nil)
			Expect(err).NotTo(HaveOccurred())
		})

		It("builds an engine with NAT 1:1 IPs and an interface filter configured", func() {
			cfg := &config.ApplicationConfig{
				WebRTCNAT1To1IPs:    []string{"192.168.1.10"},
				WebRTCICEInterfaces: []string{"eth0"},
			}
			_, err := webRTCSettingEngine(cfg)
			Expect(err).NotTo(HaveOccurred())
		})

		It("binds the configured UDP port exclusively for reuse", func() {
			probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
			Expect(err).NotTo(HaveOccurred())
			port := probe.LocalAddr().(*net.UDPAddr).Port
			Expect(probe.Close()).To(Succeed())

			engine, err := webRTCSettingEngine(&config.ApplicationConfig{WebRTCUDPPort: port})
			Expect(err).NotTo(HaveOccurred())

			duplicate, bindErr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
			if duplicate != nil {
				Expect(duplicate.Close()).To(Succeed())
			}
			Expect(bindErr).To(HaveOccurred())
			runtime.KeepAlive(engine)
		})

		It("returns a bind error when the configured UDP port is occupied", func() {
			occupied, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(occupied.Close)

			_, err = webRTCSettingEngine(&config.ApplicationConfig{
				WebRTCUDPPort: occupied.LocalAddr().(*net.UDPAddr).Port,
			})
			Expect(err).To(MatchError(ContainSubstring("bind WebRTC UDP port")))
		})
	})
})
