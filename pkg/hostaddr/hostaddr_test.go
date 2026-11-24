package hostaddr_test

import (
	"net"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/hostaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// /proc/net/if_inet6 as on a host right after an IPv6 prefix change (addresses
// from the documentation range): the old prefix (2001:db8:0:2::/64) is
// deprecated (flag 0x20), the new one (2001:db8:0:1::/64) is not.
const ifInet6Sample = `20010db8000000014f288a20abf6d0eb 04 40 00 00 wlan0
20010db800000002e7c4e3890143ff0e 02 40 00 20 eth0
20010db8000000012c90c6abbcf822fb 02 40 00 01 eth0
fd000000000000000000000000000002 05 80 00 80 tun0
00000000000000000000000000000001 01 80 10 80 lo
`

var _ = Describe("deprecated IPv6 addresses in WebRTC ICE", func() {
	var old string
	BeforeEach(func() { old = hostaddr.IfInet6Path })
	AfterEach(func() { hostaddr.IfInet6Path = old })

	It("reads exactly the deprecated addresses", func() {
		dep := hostaddr.DeprecatedIPv6(ifInet6Sample)
		Expect(dep).To(HaveLen(1))
		Expect(dep).To(HaveKey("2001:db8:0:2:e7c4:e389:143:ff0e"))
	})

	It("drops a deprecated address and keeps current IPv6 and IPv4", func() {
		hostaddr.IfInet6Path = filepath.Join(GinkgoT().TempDir(), "if_inet6")
		Expect(os.WriteFile(hostaddr.IfInet6Path, []byte(ifInet6Sample), 0o600)).To(Succeed())
		Expect(hostaddr.NotDeprecated(net.ParseIP("2001:db8:0:2:e7c4:e389:143:ff0e"))).To(BeFalse())
		Expect(hostaddr.NotDeprecated(net.ParseIP("2001:db8:0:1:4f28:8a20:abf6:d0eb"))).To(BeTrue())
		Expect(hostaddr.NotDeprecated(net.ParseIP("2001:db8:0:1:2c90:c6ab:bcf8:22fb"))).To(BeTrue(), "temporary is not deprecated")
		Expect(hostaddr.NotDeprecated(net.ParseIP("192.0.2.10"))).To(BeTrue())
	})

	It("keeps every address when the flag file cannot be read", func() {
		hostaddr.IfInet6Path = filepath.Join(GinkgoT().TempDir(), "missing")
		Expect(hostaddr.NotDeprecated(net.ParseIP("2001:db8:0:2:e7c4:e389:143:ff0e"))).To(BeTrue())
	})
})
