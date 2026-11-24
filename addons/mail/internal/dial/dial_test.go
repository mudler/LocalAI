// SPDX-License-Identifier: MIT
package dial_test

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/mailtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDial(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail dialer")
}

func listen() (net.Listener, int) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = l.Close() })
	return l, l.Addr().(*net.TCPAddr).Port
}

var _ = Describe("the guarded dialer", func() {
	ctx := context.Background()
	loop := netip.MustParseAddr("127.0.0.1")

	It("refuses loopback, private, metadata and malformed hosts with the production policy", func() {
		d := dial.New(dial.Options{})
		for _, host := range []string{"127.0.0.1", "localhost", "10.1.2.3", "192.168.0.9", "169.254.169.254", "::1", "2130706433", "mail.example.com/x", "user@host"} {
			_, err := d.Open(ctx, account.Endpoint{Host: host, Port: 993, Security: account.TLS})
			Expect(err).To(MatchError(dial.ErrBlocked), host)
		}
	})

	It("refuses a port that is not a mail port", func() {
		d := dial.New(dial.Options{})
		for _, port := range []int{25, 22, 80, 443, 6379} {
			_, err := d.Open(ctx, account.Endpoint{Host: "203.0.113.5", Port: port, Security: account.TLS})
			Expect(err).To(MatchError(dial.ErrBlocked), strconv.Itoa(port))
		}
	})

	It("connects to the address it checked", func() {
		l, port := listen()
		go func() {
			c, _ := l.Accept()
			if c != nil {
				_ = c.Close()
			}
		}()
		d := dial.New(dial.Options{Ports: []int{port}, TreatAsPublic: []netip.Addr{loop}})
		c, err := d.Open(ctx, account.Endpoint{Host: "127.0.0.1", Port: port, Security: account.StartTLS})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Close()).To(Succeed())
	})

	It("reports a closed port as a connection failure", func() {
		l, port := listen()
		Expect(l.Close()).To(Succeed())
		d := dial.New(dial.Options{Ports: []int{port}, TreatAsPublic: []netip.Addr{loop}})
		_, err := d.Open(ctx, account.Endpoint{Host: "127.0.0.1", Port: port, Security: account.StartTLS})
		Expect(err).To(MatchError(dial.ErrConnect))
	})

	It("reports a server that never answers the TLS handshake as a timeout", func() {
		l, port := listen()
		go func() { c, _ := l.Accept(); _ = c; time.Sleep(2 * time.Second) }()
		d := dial.New(dial.Options{Ports: []int{port}, TreatAsPublic: []netip.Addr{loop}, Timeout: 200 * time.Millisecond})
		_, err := d.Open(ctx, account.Endpoint{Host: "127.0.0.1", Port: port, Security: account.TLS})
		Expect(err).To(MatchError(dial.ErrTimeout))
	})

	It("verifies the certificate: an unknown authority and a wrong name are refused", func() {
		ca := mailtest.NewCA()
		l, port := listen()
		tlsL := ca.Wrap(l, "other.example.com")
		go func() {
			for {
				c, err := tlsL.Accept()
				if err != nil {
					return
				}
				go func() { buf := make([]byte, 1); _, _ = c.Read(buf); _ = c.Close() }()
			}
		}()
		ep := account.Endpoint{Host: "127.0.0.1", Port: port, Security: account.TLS}
		// The system roots do not know the test authority.
		d := dial.New(dial.Options{Ports: []int{port}, TreatAsPublic: []netip.Addr{loop}})
		_, err := d.Open(ctx, ep)
		Expect(err).To(MatchError(dial.ErrTLS))
		// With the authority trusted, the name still has to match.
		d = dial.New(dial.Options{Ports: []int{port}, TreatAsPublic: []netip.Addr{loop}, RootCAs: ca.Pool()})
		_, err = d.Open(ctx, ep)
		Expect(err).To(MatchError(dial.ErrTLS))
	})
})
