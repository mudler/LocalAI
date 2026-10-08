package messaging_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TLSFiles", func() {
	It("requires cert and key together", func() {
		Expect((messaging.TLSFiles{Cert: "/tmp/c.pem"}).Validate()).To(HaveOccurred())
		Expect((messaging.TLSFiles{Key: "/tmp/k.pem"}).Validate()).To(HaveOccurred())
	})

	It("validates files exist", func() {
		dir := GinkgoT().TempDir()
		ca := filepath.Join(dir, "ca.pem")
		Expect(os.WriteFile(ca, []byte("x"), 0600)).To(Succeed())
		Expect((messaging.TLSFiles{CA: ca}).Validate()).To(Succeed())
	})
})

// fakeTLSNATS speaks just enough of the NATS protocol for a client to connect
// over TLS: it sends INFO with tls_required, upgrades the connection, and
// answers CONNECT and PING. It lets a spec check which certificates a client
// trusts without a NATS server.
type fakeTLSNATS struct {
	addr string
	ca   []byte
}

func startFakeTLSNATS() *fakeTLSNATS {
	GinkgoHelper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "spec ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	Expect(err).ToNot(HaveOccurred())
	caCert, err := x509.ParseCertificate(caDER)
	Expect(err).ToNot(HaveOccurred())

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	Expect(err).ToNot(HaveOccurred())
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(lis.Close)
	go func() {
		for {
			raw, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = raw.Close() }()
				_, _ = raw.Write([]byte(`INFO {"server_id":"fake","version":"2.10.0","proto":1,"host":"127.0.0.1","max_payload":1048576,"tls_required":true}` + "\r\n"))
				conn := tls.Server(raw, tlsCfg)
				if conn.Handshake() != nil {
					return
				}
				rd := bufio.NewReader(conn)
				for {
					line, err := rd.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "PING"):
						_, _ = conn.Write([]byte("PONG\r\n"))
					case strings.HasPrefix(line, "CONNECT"):
						// The client sends PING next and waits for PONG.
					}
				}
			}()
		}
	}()
	return &fakeTLSNATS{
		addr: "tls://" + lis.Addr().String(),
		ca:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}
}

var _ = Describe("A CA given as PEM", func() {
	connects := func(url string, opts ...messaging.Option) bool {
		GinkgoHelper()
		c, err := messaging.New(url, opts...)
		if err != nil {
			return false
		}
		defer c.Close()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if c.IsConnected() {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	It("is trusted without a file on disk", func() {
		srv := startFakeTLSNATS()
		Expect(connects(srv.addr, messaging.WithTLS(messaging.TLSFiles{CAPEM: srv.ca}))).To(BeTrue())
	})

	It("is required: a server of a private CA is refused without it", func() {
		srv := startFakeTLSNATS()
		Expect(connects(srv.addr)).To(BeFalse())
	})

	It("counts as TLS configuration, and is refused when it holds no certificate", func() {
		Expect((messaging.TLSFiles{CAPEM: []byte("x")}).Enabled()).To(BeTrue())
		Expect((messaging.TLSFiles{CAPEM: []byte("not a certificate")}).Validate()).To(HaveOccurred())
	})
})
