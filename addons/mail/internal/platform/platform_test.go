// SPDX-License-Identifier: MIT
package platform_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/platform"
	"github.com/mudler/LocalAI/addons/mail/mailtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlatform(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail connector route")
}

var _ = Describe("the connector route", func() {
	var (
		p    platform.Platform
		acct account.Account
		smtp *mailtest.SMTP
	)
	BeforeEach(func() {
		ca := mailtest.NewCA()
		imap := mailtest.NewIMAP(ca, mailtest.ModeTLS)
		smtp = mailtest.NewSMTP(ca, mailtest.ModeTLS)
		DeferCleanup(imap.Close)
		DeferCleanup(smtp.Close)
		imap.AddUser("ann", "longpassword")
		smtp.AddUser("ann", "longpassword")
		acct = account.Account{Version: 1, Address: "ann@example.com", Username: "ann", Password: "longpassword",
			IMAP: account.Endpoint{Host: "127.0.0.1", Port: imap.Port, Security: account.TLS},
			SMTP: account.Endpoint{Host: "127.0.0.1", Port: smtp.Port, Security: account.TLS}}
		p = platform.Platform{Dialer: dial.New(dial.Options{Ports: []int{imap.Port, smtp.Port}, RootCAs: ca.Pool(), TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})}
	})
	me := func() (contract.ConnectorResponse, error) {
		tok, err := acct.Encode()
		Expect(err).NotTo(HaveOccurred())
		return connector.Serve(context.Background(), p, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "mail", Token: tok})
	}

	It("answers me with the address after both logins work", func() {
		r, err := me()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Error).To(BeNil())
		Expect(r.BotUsername).To(Equal("ann@example.com"))
	})
	It("refuses the credential when the IMAP password, the SMTP password or the record is wrong", func() {
		acct.Password = "wrongpassword"
		r, _ := me()
		Expect(r.Error).NotTo(BeNil())
		Expect(r.Error.Code).To(Equal(contract.ConnectorUnauthorized))

		_, err := connector.Serve(context.Background(), p, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "mail", Token: "{broken"})
		Expect(err).NotTo(HaveOccurred())
	})
	It("does not refuse the credential when only the network fails", func() {
		smtp.Close()
		r, _ := me()
		Expect(r.Error).NotTo(BeNil())
		Expect(r.Error.Code).NotTo(Equal(contract.ConnectorUnauthorized))
	})
	It("carries no chat messages", func() {
		_, err := p.Send(context.Background(), contract.ConnectorRequest{})
		Expect(err).To(MatchError(platform.ErrNoMessages))
	})
})
