package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	nethttp "net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/tlsconf"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the TLS flags of run", func() {
	It("are off by default and parse to the three fields", func() {
		r := RunCMD{}
		Expect(r.TLSCert).To(BeEmpty())
		Expect(r.TLSKey).To(BeEmpty())
		Expect(r.TLSSelfSigned).To(BeFalse())
		m, err := tlsconf.Resolve(r.TLSCert, r.TLSKey, r.TLSSelfSigned, GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(BeNil(), "off means plain HTTP, as before")
	})

	newServer := func() *echo.Echo {
		e := echo.New()
		e.HideBanner = true
		e.HidePort = true
		e.GET("/ping", func(c echo.Context) error { return c.String(nethttp.StatusOK, "pong") })
		return e
	}
	run := func(e *echo.Echo, serve func() error) chan error {
		done := make(chan error, 1)
		go func() { done <- serve() }()
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = e.Shutdown(ctx)
			Eventually(done).Should(Receive())
		})
		return done
	}
	get := func(url string, cfg *tls.Config) (string, error) {
		c := &nethttp.Client{Timeout: 3 * time.Second, Transport: &nethttp.Transport{TLSClientConfig: cfg}}
		defer c.CloseIdleConnections()
		resp, err := c.Get(url)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	// pinned verifies the server certificate by its fingerprint and nothing else,
	// as a paired client does.
	pinned := func(fingerprint string) *tls.Config {
		return &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true, // #nosec G402 -- the pin below is the check
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if tlsconf.FingerprintOf(raw[0]) != fingerprint {
					return errors.New("pin mismatch")
				}
				return nil
			},
		}
	}

	It("serves TLS 1.3 on the activated listener with the certificate the client pins", func() {
		m, err := tlsconf.SelfSigned("", time.Now())
		Expect(err).ToNot(HaveOccurred())
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		e := newServer()
		run(e, httpServe(e, l.Addr().String(), m, l))

		url := "https://" + l.Addr().String() + "/ping"
		Eventually(func() (string, error) { return get(url, pinned(m.Fingerprint)) }).Should(Equal("pong"))
		_, err = get(url, pinned(tlsconf.FingerprintOf([]byte("another certificate"))))
		Expect(err).To(HaveOccurred(), "a certificate with another fingerprint is refused")
		_, err = get(url, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}) // #nosec G402 -- the test proves the refusal
		Expect(err).To(HaveOccurred(), "TLS 1.2 is refused")
		body, _ := get("http://"+l.Addr().String()+"/ping", nil)
		Expect(body).ToNot(Equal("pong"), "plain HTTP is not served on a TLS port")
	})

	It("serves TLS on the address it is given and keeps a loopback address on loopback", func() {
		m, err := tlsconf.SelfSigned("", time.Now())
		Expect(err).ToNot(HaveOccurred())
		// A fixed port: take a free one and release it, so the test can tell
		// the configured port from a random one.
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		address := probe.Addr().String()
		Expect(probe.Close()).To(Succeed())

		e := newServer()
		run(e, httpServe(e, address, m, nil))
		var addr net.Addr
		Eventually(func() net.Addr { addr = e.TLSListenerAddr(); return addr }).ShouldNot(BeNil())
		Expect(addr.String()).To(Equal(address), "the TLS listener binds the address that was asked for")
		tcp, ok := addr.(*net.TCPAddr)
		Expect(ok).To(BeTrue())
		Expect(tcp.IP.IsLoopback()).To(BeTrue(), "a loopback address must not widen to every interface")
		Eventually(func() (string, error) { return get("https://"+address+"/ping", pinned(m.Fingerprint)) }).Should(Equal("pong"))
	})

	It("serves plain HTTP when no certificate is set", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		e := newServer()
		run(e, httpServe(e, l.Addr().String(), nil, l))
		Eventually(func() (string, error) { return get("http://"+l.Addr().String()+"/ping", nil) }).Should(Equal("pong"))
	})
})
