package mitm

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LoadOrCreateCA", func() {
	It("generates and persists", func() {
		dir := GinkgoT().TempDir()

		ca1, err := LoadOrCreateCA(dir)
		Expect(err).NotTo(HaveOccurred(), "first call")
		Expect(ca1.cert).NotTo(BeNil())
		Expect(ca1.cert.IsCA).To(BeTrue(), "generated cert is not a CA")
		// Files must be on disk after first call.
		for _, name := range []string{"ca.crt", "ca.key"} {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred(), "expected %s to exist", path)
			mode := info.Mode().Perm()
			if name == "ca.key" {
				Expect(mode).To(Equal(os.FileMode(0o600)))
			}
		}

		// Second load must round-trip the same cert (same serial number
		// proves we read from disk rather than regenerating).
		ca2, err := LoadOrCreateCA(dir)
		Expect(err).NotTo(HaveOccurred(), "second call")
		Expect(ca1.cert.SerialNumber.Cmp(ca2.cert.SerialNumber)).To(Equal(0), "second load regenerated instead of reading from disk")
	})

	It("rejects non-CA stored cert", func() {
		dir := GinkgoT().TempDir()
		// Write a non-CA leaf cert into the slot reserved for the CA.
		ca, err := NewInMemoryCA()
		Expect(err).NotTo(HaveOccurred())
		leaf, err := ca.IssueLeaf("example.com")
		Expect(err).NotTo(HaveOccurred())
		leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]})
		Expect(os.WriteFile(filepath.Join(dir, "ca.crt"), leafPEM, 0o644)).To(Succeed())
		// Pair with a key file so LoadOrCreateCA proceeds to parse.
		Expect(os.WriteFile(filepath.Join(dir, "ca.key"), []byte("garbage"), 0o600)).To(Succeed())
		_, err = LoadOrCreateCA(dir)
		Expect(err).To(HaveOccurred(), "expected error for non-CA cert in CA slot")
		Expect(strings.Contains(err.Error(), "delete to regenerate")).To(BeTrue(), "error should mention regenerate path")
	})
})

var _ = Describe("PublicCertPEM", func() {
	It("is a valid certificate", func() {
		ca, err := NewInMemoryCA()
		Expect(err).NotTo(HaveOccurred())
		pemBytes := ca.PublicCertPEM()
		block, _ := pem.Decode(pemBytes)
		Expect(block).NotTo(BeNil())
		Expect(block.Type).To(Equal("CERTIFICATE"))
		cert, err := x509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(cert.IsCA).To(BeTrue(), "decoded cert is not a CA")
	})

	It("returns a copy", func() {
		// Mutating the returned slice must not poison subsequent calls.
		ca, err := NewInMemoryCA()
		Expect(err).NotTo(HaveOccurred())
		first := ca.PublicCertPEM()
		first[0] = 0x00 // corrupt
		second := ca.PublicCertPEM()
		Expect(second[0]).NotTo(Equal(byte(0x00)), "PublicCertPEM aliased its cache; mutation leaked")
	})
})
