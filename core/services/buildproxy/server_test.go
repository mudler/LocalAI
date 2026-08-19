package buildproxy

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Interception certificates", func() {
	It("issues host certificates trusted by the generated CA", func() {
		ca, path, err := createCA(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		leaf, err := ca.leaf("registry.example.test")
		Expect(err).NotTo(HaveOccurred())

		caPEM, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		block, _ := pem.Decode(caPEM)
		Expect(block).NotTo(BeNil())
		root, err := x509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		roots := x509.NewCertPool()
		roots.AddCert(root)
		certificate, err := x509.ParseCertificate(leaf.Certificate[0])
		Expect(err).NotTo(HaveOccurred())
		_, err = certificate.Verify(x509.VerifyOptions{DNSName: "registry.example.test", Roots: roots})
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects plain HTTP", func() {
		dir := GinkgoT().TempDir()
		recorder, err := NewRecorder(dir + "/events.jsonl")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = recorder.Close() }()
		server, err := NewServer("127.0.0.1:0", dir+"/ca", http.NotFoundHandler(), recorder)
		Expect(err).NotTo(HaveOccurred())
		response := httptest.NewRecorder()
		server.serveHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.test/file", nil))
		Expect(response.Code).To(Equal(http.StatusUpgradeRequired))
	})
})
