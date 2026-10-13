package application

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The TLS settings of the peer link", func() {
	DescribeTable("warns about clear text only for an address that is not on this host",
		func(addr string, want bool) {
			Expect(clearTextOnNetwork(addr)).To(Equal(want))
		},
		Entry("nothing published", "", false),
		Entry("loopback", "127.0.0.1:8080", false),
		Entry("loopback v6", "[::1]:8080", false),
		Entry("localhost", "localhost:8080", false),
		Entry("a private address", "10.1.2.3:8080", true),
		Entry("a public address", "203.0.113.9:443", true),
		Entry("a name", "frontend-2.internal:8080", true),
	)

	It("adds no option and keeps the link plain when TLS is off", func() {
		opts, err := peerPoolOptions(&config.ApplicationConfig{}, "10.1.2.3:8080")
		Expect(err).ToNot(HaveOccurred())
		Expect(opts).To(BeEmpty())
	})

	It("adds the TLS option when it is on, with the system roots", func() {
		cfg := &config.ApplicationConfig{}
		cfg.Distributed.PeerTLS = true
		opts, err := peerPoolOptions(cfg, "10.1.2.3:8080")
		Expect(err).ToNot(HaveOccurred())
		Expect(opts).To(HaveLen(1))
	})

	It("refuses a CA file that holds no certificate, and one that is missing", func() {
		cfg := &config.ApplicationConfig{}
		cfg.Distributed.PeerTLSCA = filepath.Join(GinkgoT().TempDir(), "missing.pem")
		_, err := peerPoolOptions(cfg, "")
		Expect(err).To(HaveOccurred())

		empty := filepath.Join(GinkgoT().TempDir(), "empty.pem")
		Expect(os.WriteFile(empty, []byte("not a certificate"), 0o600)).To(Succeed())
		cfg.Distributed.PeerTLSCA = empty
		_, err = peerPoolOptions(cfg, "")
		Expect(err).To(HaveOccurred())
	})
})
