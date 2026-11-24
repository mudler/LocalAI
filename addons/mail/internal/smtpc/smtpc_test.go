// SPDX-License-Identifier: MIT
package smtpc_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/smtpc"
	"github.com/mudler/LocalAI/addons/mail/mailtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSmtpc(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail SMTP")
}

type rig struct {
	ca   *mailtest.CA
	srv  *mailtest.SMTP
	acct account.Account
	d    *dial.Dialer
}

func newRig(mode mailtest.Mode) *rig {
	r := &rig{ca: mailtest.NewCA()}
	r.srv = mailtest.NewSMTP(r.ca, mode)
	DeferCleanup(r.srv.Close)
	r.srv.AddUser("ann", "pw")
	r.acct = account.Account{Version: 1, Address: "ann@example.com", Username: "ann", Password: "pw",
		IMAP: account.Endpoint{Host: "127.0.0.1", Port: 993, Security: account.TLS},
		SMTP: account.Endpoint{Host: "127.0.0.1", Port: r.srv.Port, Security: account.Security(mode)}}
	r.d = dial.New(dial.Options{Ports: []int{r.srv.Port}, RootCAs: r.ca.Pool(), TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})
	return r
}

func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	DeferCleanup(cancel)
	return c
}

const msg = "From: ann@example.com\r\nTo: bob@example.org\r\nSubject: hi\r\n\r\n.leading dot line\r\nbody\r\n"

var _ = Describe("sending", func() {
	It("submits over implicit TLS and over STARTTLS, with AUTH PLAIN, and keeps dots", func() {
		for _, mode := range []mailtest.Mode{mailtest.ModeTLS, mailtest.ModeStartTLS} {
			r := newRig(mode)
			Expect(smtpc.Send(ctx(), r.d, r.acct, "ann@example.com", []string{"bob@example.org"}, []byte(msg))).To(Succeed())
			sent := r.srv.Sent()
			Expect(sent).To(HaveLen(1))
			Expect(sent[0].User).To(Equal("ann"))
			Expect(sent[0].From).To(Equal("ann@example.com"))
			Expect(sent[0].To).To(Equal([]string{"bob@example.org"}))
			Expect(strings.ReplaceAll(string(sent[0].Data), "\r\n", "\n")).To(ContainSubstring("\n.leading dot line\n"))
		}
	})

	It("falls to AUTH LOGIN when the server offers only that", func() {
		r := newRig(mailtest.ModeTLS)
		r.srv.OnlyLogin()
		Expect(smtpc.Send(ctx(), r.d, r.acct, "ann@example.com", []string{"bob@example.org"}, []byte(msg))).To(Succeed())
		Expect(r.srv.Sent()).To(HaveLen(1))
	})

	It("sends in clear text only when the connection says none", func() {
		r := newRig(mailtest.ModePlain)
		Expect(smtpc.Check(ctx(), r.d, r.acct)).To(Succeed())
	})

	It("never sends the password when STARTTLS is asked for and the server offers none", func() {
		r := newRig(mailtest.ModePlain)
		r.acct.SMTP.Security = account.StartTLS
		err := smtpc.Send(ctx(), r.d, r.acct, "ann@example.com", []string{"bob@example.org"}, []byte(msg))
		Expect(err).To(HaveOccurred())
		Expect(r.srv.Transcript()).NotTo(MatchRegexp(`(?m)^AUTH `))
		Expect(r.srv.Sent()).To(BeEmpty())
	})

	It("answers a wrong password with a fixed sentence", func() {
		r := newRig(mailtest.ModeTLS)
		r.acct.Password = "nope-secret"
		err := smtpc.Check(ctx(), r.d, r.acct)
		Expect(err).To(MatchError(smtpc.ErrAuth))
		Expect(err.Error()).NotTo(ContainSubstring("nope-secret"))
		Expect(smtpc.Definite(err)).To(BeTrue())
	})

	It("tells a refused recipient from a refused message and from a lost answer", func() {
		r := newRig(mailtest.ModeTLS)
		r.srv.RejectRecipient("bad@example.org")
		err := smtpc.Send(ctx(), r.d, r.acct, "ann@example.com", []string{"bad@example.org"}, []byte(msg))
		Expect(err).To(MatchError(smtpc.ErrRecipient))
		Expect(smtpc.Definite(err)).To(BeTrue())

		r2 := newRig(mailtest.ModeTLS)
		r2.srv.FailData(554, "spam")
		err = smtpc.Send(ctx(), r2.d, r2.acct, "ann@example.com", []string{"bob@example.org"}, []byte(msg))
		Expect(err).To(MatchError(smtpc.ErrRejected))

		r3 := newRig(mailtest.ModeTLS)
		r3.srv.DropAfterData()
		err = smtpc.Send(ctx(), r3.d, r3.acct, "ann@example.com", []string{"bob@example.org"}, []byte(msg))
		Expect(err).To(MatchError(smtpc.ErrUnknown))
		Expect(smtpc.Definite(err)).To(BeFalse())
	})

	It("refuses an untrusted certificate before it sends the password", func() {
		r := newRig(mailtest.ModeStartTLS)
		r.d = dial.New(dial.Options{Ports: []int{r.srv.Port}, TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})
		err := smtpc.Check(ctx(), r.d, r.acct)
		Expect(err).To(HaveOccurred())
		Expect(r.srv.Transcript()).NotTo(MatchRegexp(`(?m)^AUTH `))
	})
})
