package tlsconf_test

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/http/tlsconf"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("tlsconf", func() {
	Describe("Resolve", func() {
		It("is off with no flag, and refuses a half or a double source", func() {
			m, err := tlsconf.Resolve("", "", false, GinkgoT().TempDir())
			Expect(err).ToNot(HaveOccurred())
			Expect(m).To(BeNil())
			_, err = tlsconf.Resolve("a.crt", "", false, "")
			Expect(err).To(HaveOccurred())
			_, err = tlsconf.Resolve("", "a.key", false, "")
			Expect(err).To(HaveOccurred())
			_, err = tlsconf.Resolve("a.crt", "a.key", true, "")
			Expect(err).To(MatchError(tlsconf.ErrBothSources))
		})

		It("makes a self-signed certificate on request", func() {
			m, err := tlsconf.Resolve("", "", true, GinkgoT().TempDir())
			Expect(err).ToNot(HaveOccurred())
			Expect(m.SelfSigned).To(BeTrue())
		})
	})

	Describe("a self-signed certificate", func() {
		It("is made once, kept in the data path with a private key, and reused", func() {
			dir := GinkgoT().TempDir()
			first, err := tlsconf.SelfSigned(dir, time.Now())
			Expect(err).ToNot(HaveOccurred())
			Expect(first.Persistent).To(BeTrue())
			keyPath := filepath.Join(dir, tlsconf.SelfSignedDir, "self-signed.key")
			info, err := os.Stat(keyPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
			dirInfo, err := os.Stat(filepath.Join(dir, tlsconf.SelfSignedDir))
			Expect(err).ToNot(HaveOccurred())
			Expect(dirInfo.Mode().Perm()).To(Equal(os.FileMode(0o700)))

			second, err := tlsconf.SelfSigned(dir, time.Now())
			Expect(err).ToNot(HaveOccurred())
			Expect(second.Fingerprint).To(Equal(first.Fingerprint), "a restart keeps the fingerprint, so pins hold")
			Expect(second.SelfSigned).To(BeTrue())
		})

		It("has a fingerprint that is the SHA-256 of the certificate, and a digest like AB12-CD34", func() {
			m, err := tlsconf.SelfSigned(GinkgoT().TempDir(), time.Now())
			Expect(err).ToNot(HaveOccurred())
			sum := sha256.Sum256(m.Certificate.Certificate[0])
			Expect(m.Fingerprint).To(Equal(hex.EncodeToString(sum[:])))
			Expect(m.Fingerprint).To(MatchRegexp(`^[0-9a-f]{64}$`))
			Expect(m.Digest()).To(MatchRegexp(`^[0-9A-F]{4}-[0-9A-F]{4}$`))
			Expect(m.Digest()).To(Equal(tlsconf.Digest(m.Fingerprint)))
			Expect(strings.ToLower(strings.ReplaceAll(m.Digest(), "-", ""))).To(Equal(m.Fingerprint[:8]))
			Expect(tlsconf.Digest("ab12cd34ef")).To(Equal("AB12-CD34"))
			Expect(tlsconf.Digest("ab12")).To(BeEmpty())
		})

		It("is replaced when it is close to its end, and not before", func() {
			dir := GinkgoT().TempDir()
			first, err := tlsconf.SelfSigned(dir, time.Now())
			Expect(err).ToNot(HaveOccurred())
			nearEnd := first.Certificate.Leaf.NotAfter.Add(-10 * 24 * time.Hour)
			again, err := tlsconf.SelfSigned(dir, nearEnd)
			Expect(err).ToNot(HaveOccurred())
			Expect(again.Fingerprint).ToNot(Equal(first.Fingerprint))
			kept, err := tlsconf.SelfSigned(dir, nearEnd.Add(-time.Hour))
			Expect(err).ToNot(HaveOccurred())
			Expect(kept.Fingerprint).To(Equal(again.Fingerprint), "the new one is valid for years")
		})

		It("is replaced when the files are damaged", func() {
			dir := GinkgoT().TempDir()
			first, err := tlsconf.SelfSigned(dir, time.Now())
			Expect(err).ToNot(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, tlsconf.SelfSignedDir, "self-signed.key"), []byte("junk"), 0o600)).To(Succeed())
			again, err := tlsconf.SelfSigned(dir, time.Now())
			Expect(err).ToNot(HaveOccurred())
			Expect(again.Fingerprint).ToNot(Equal(first.Fingerprint))
		})

		It("stays in memory without a data path, and is new each time", func() {
			a, err := tlsconf.SelfSigned("", time.Now())
			Expect(err).ToNot(HaveOccurred())
			b, err := tlsconf.SelfSigned("", time.Now())
			Expect(err).ToNot(HaveOccurred())
			Expect(a.Persistent).To(BeFalse())
			Expect(a.Fingerprint).ToNot(Equal(b.Fingerprint))
		})

		It("names the loopback addresses", func() {
			m, err := tlsconf.SelfSigned("", time.Now())
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Certificate.Leaf.DNSNames).To(ContainElement("localhost"))
			Expect(m.Certificate.Leaf.IPAddresses).To(ContainElement(net.IPv4(127, 0, 0, 1).To4()))
		})
	})

	Describe("Load", func() {
		It("reads a certificate and a key and gives the same fingerprint as the file holds", func() {
			dir := GinkgoT().TempDir()
			made, err := tlsconf.SelfSigned(dir, time.Now())
			Expect(err).ToNot(HaveOccurred())
			m, err := tlsconf.Load(filepath.Join(dir, tlsconf.SelfSignedDir, "self-signed.crt"), filepath.Join(dir, tlsconf.SelfSignedDir, "self-signed.key"))
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Fingerprint).To(Equal(made.Fingerprint))
			Expect(m.SelfSigned).To(BeFalse(), "a file the operator supplied is not ours")
		})

		It("refuses a missing file, a key that does not match and an expired certificate", func() {
			dir := GinkgoT().TempDir()
			_, err := tlsconf.Load(filepath.Join(dir, "no.crt"), filepath.Join(dir, "no.key"))
			Expect(err).To(HaveOccurred())

			a, b := GinkgoT().TempDir(), GinkgoT().TempDir()
			_, err = tlsconf.SelfSigned(a, time.Now())
			Expect(err).ToNot(HaveOccurred())
			_, err = tlsconf.SelfSigned(b, time.Now())
			Expect(err).ToNot(HaveOccurred())
			_, err = tlsconf.Load(filepath.Join(a, tlsconf.SelfSignedDir, "self-signed.crt"), filepath.Join(b, tlsconf.SelfSignedDir, "self-signed.key"))
			Expect(err).To(HaveOccurred())

			old := GinkgoT().TempDir()
			_, err = tlsconf.SelfSigned(old, time.Now().Add(-11*365*24*time.Hour))
			Expect(err).ToNot(HaveOccurred())
			_, err = tlsconf.Load(filepath.Join(old, tlsconf.SelfSignedDir, "self-signed.crt"), filepath.Join(old, tlsconf.SelfSignedDir, "self-signed.key"))
			Expect(err).To(MatchError(ContainSubstring("expired")))
		})
	})

	Describe("the server configuration", func() {
		It("serves TLS 1.3 and not older, and the certificate a client can pin", func() {
			m, err := tlsconf.SelfSigned("", time.Now())
			Expect(err).ToNot(HaveOccurred())
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
			srv.TLS = m.Config()
			srv.StartTLS()
			DeferCleanup(srv.Close)
			Expect(srv.TLS.MinVersion).To(Equal(uint16(tls.VersionTLS13)))

			addr := srv.Listener.Addr().String()
			old, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}) // #nosec G402 -- the test proves the refusal
			if err == nil {
				_ = old.Close()
			}
			Expect(err).To(HaveOccurred(), "TLS 1.2 is refused")

			conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}) // #nosec G402 -- pinned by fingerprint below
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			Expect(conn.ConnectionState().Version).To(Equal(uint16(tls.VersionTLS13)))
			Expect(tlsconf.FingerprintOf(conn.ConnectionState().PeerCertificates[0].Raw)).To(Equal(m.Fingerprint))
		})
	})
})
