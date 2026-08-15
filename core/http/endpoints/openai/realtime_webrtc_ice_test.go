package openai

import (
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pion/webrtc/v4"
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

		// A fixed UDP port and an interface allow-list are the two settings an
		// operator behind a firewall reaches for together: one to write the
		// firewall rule against, the other to keep unreachable docker0/veth
		// addresses out of the candidate list. Pinning the port must not cost
		// them the filter.
		It("honours the interface allow-list when a UDP port is pinned", func() {
			_, err := webRTCSettingEngine(&config.ApplicationConfig{
				WebRTCICEInterfaces: []string{"localai-no-such-interface"},
				WebRTCUDPPort:       freeUDPPort(),
			})
			Expect(err).To(MatchError(ContainSubstring("localai-no-such-interface")))
		})

		It("gathers host candidates only on allowed interfaces when a UDP port is pinned", func() {
			allowed, others := splitLocalInterfaces()
			if allowed == "" || len(others) == 0 {
				Skip("needs at least two non-loopback interfaces with IPv4 addresses")
			}

			engine, err := webRTCSettingEngine(&config.ApplicationConfig{
				WebRTCICEInterfaces: []string{allowed},
				WebRTCUDPPort:       freeUDPPort(),
			})
			Expect(err).NotTo(HaveOccurred())

			gathered := gatheredHostIPs(engine)
			Expect(gathered).NotTo(BeEmpty(), "the allowed interface should still produce a candidate")
			Expect(gathered).To(ConsistOf(ipsOfInterface(allowed)))
			for _, excluded := range others {
				for _, ip := range ipsOfInterface(excluded) {
					Expect(gathered).NotTo(ContainElement(ip),
						"candidate from %s leaked despite the allow-list naming only %s", excluded, allowed)
				}
			}
		})
	})
})

// freeUDPPort asks the kernel for an unused UDP port and releases it.
func freeUDPPort() int {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	Expect(err).NotTo(HaveOccurred())
	port := conn.LocalAddr().(*net.UDPAddr).Port
	Expect(conn.Close()).To(Succeed())
	return port
}

// splitLocalInterfaces returns one interface to allow plus every other
// candidate-bearing interface, so a spec can assert the others stay out.
func splitLocalInterfaces() (allowed string, others []string) {
	ifaces, err := net.Interfaces()
	Expect(err).NotTo(HaveOccurred())
	var usable []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(ipsOfInterface(iface.Name)) > 0 {
			usable = append(usable, iface.Name)
		}
	}
	if len(usable) == 0 {
		return "", nil
	}
	return usable[0], usable[1:]
}

// ipsOfInterface returns the IPv4 addresses pion can gather a host candidate
// on for the named interface.
func ipsOfInterface(name string) []string {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var ips []string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil {
			continue
		}
		ips = append(ips, ipNet.IP.String())
	}
	return ips
}

// gatheredHostIPs runs a peer connection through the engine and returns the
// distinct IPs of the host candidates it advertises.
func gatheredHostIPs(engine webrtc.SettingEngine) []string {
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(engine)).NewPeerConnection(webrtc.Configuration{})
	Expect(err).NotTo(HaveOccurred())
	defer pc.Close()

	_, err = pc.CreateDataChannel("probe", nil)
	Expect(err).NotTo(HaveOccurred())
	offer, err := pc.CreateOffer(nil)
	Expect(err).NotTo(HaveOccurred())
	gathered := webrtc.GatheringCompletePromise(pc)
	Expect(pc.SetLocalDescription(offer)).To(Succeed())
	Eventually(gathered, 10*time.Second).Should(BeClosed())

	// Candidate lines read "a=candidate:<foundation> <component> udp
	// <priority> <ip> <port> typ host ...", so the IP is the fifth field.
	seen := map[string]struct{}{}
	var ips []string
	for _, line := range strings.Split(pc.LocalDescription().SDP, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "a=candidate:") || !strings.Contains(line, " typ host") {
			continue
		}
		ip := strings.Fields(line)[4]
		if _, dup := seen[ip]; dup {
			continue
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}
	return ips
}
