package mitm

import (
	"crypto/tls"
	"crypto/x509"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IssueLeaf", func() {
	It("chains to CA", func() {
		ca, err := NewInMemoryCA()
		Expect(err).NotTo(HaveOccurred())
		leaf, err := ca.IssueLeaf("api.anthropic.com")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(leaf.Certificate)).To(BeNumerically(">=", 1), "leaf has no DER")
		parsed, err := x509.ParseCertificate(leaf.Certificate[0])
		Expect(err).NotTo(HaveOccurred())
		// Verify it's actually signed by the CA we generated.
		pool := x509.NewCertPool()
		pool.AddCert(ca.Cert())
		_, err = parsed.Verify(x509.VerifyOptions{
			Roots:     pool,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSName:   "api.anthropic.com",
		})
		Expect(err).NotTo(HaveOccurred(), "verify chain")
	})

	It("populates DNS and IP SANs correctly", func() {
		ca, err := NewInMemoryCA()
		Expect(err).NotTo(HaveOccurred())

		// Hostname → DNSNames
		leafDNS, err := ca.IssueLeaf("example.com")
		Expect(err).NotTo(HaveOccurred())
		parsedDNS, _ := x509.ParseCertificate(leafDNS.Certificate[0])
		Expect(parsedDNS.DNSNames).NotTo(BeEmpty())
		Expect(parsedDNS.DNSNames[0]).To(Equal("example.com"))
		Expect(parsedDNS.IPAddresses).To(BeEmpty(), "hostname leaf should have no IP SAN")

		// IP → IPAddresses
		leafIP, err := ca.IssueLeaf("127.0.0.1")
		Expect(err).NotTo(HaveOccurred())
		parsedIP, _ := x509.ParseCertificate(leafIP.Certificate[0])
		Expect(parsedIP.IPAddresses).NotTo(BeEmpty())
		Expect(parsedIP.IPAddresses[0].Equal(net.ParseIP("127.0.0.1"))).To(BeTrue())
		Expect(parsedIP.DNSNames).To(BeEmpty(), "IP leaf should have no DNS SAN")
	})

	It("caches by host", func() {
		ca, err := NewInMemoryCA()
		Expect(err).NotTo(HaveOccurred())
		a, _ := ca.IssueLeaf("api.example.com")
		b, _ := ca.IssueLeaf("api.example.com")
		Expect(a).To(BeIdenticalTo(b), "expected cached leaf to be returned, got distinct certs")
		c, _ := ca.IssueLeaf("API.Example.com") // case-insensitive
		Expect(a).To(BeIdenticalTo(c), "expected case-insensitive cache hit")
		d, _ := ca.IssueLeaf("api.example.com:443") // host:port stripped
		Expect(a).To(BeIdenticalTo(d), "expected port-stripped cache hit")
	})

	It("handshake accepted by client", func() {
		// End-to-end check: a TLS server using the leaf, with a client
		// trusting the CA, completes a handshake. This is the property
		// every other flow in this package depends on.
		ca, err := NewInMemoryCA()
		Expect(err).NotTo(HaveOccurred())
		leaf, err := ca.IssueLeaf("localhost")
		Expect(err).NotTo(HaveOccurred())

		pool := x509.NewCertPool()
		pool.AddCert(ca.Cert())

		listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
			Certificates: []tls.Certificate{*leaf},
		})
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = listener.Close() }()

		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = conn.Write([]byte("ok"))
		}()

		conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{
			RootCAs:    pool,
			ServerName: "localhost",
		})
		Expect(err).NotTo(HaveOccurred(), "client TLS dial")
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 2)
		_, err = conn.Read(buf)
		Expect(err).NotTo(HaveOccurred(), "read")
		Expect(string(buf)).To(Equal("ok"))
	})
})
