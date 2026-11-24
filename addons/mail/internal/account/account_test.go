// SPDX-License-Identifier: MIT
package account_test

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAccount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail account")
}

func good() account.Account {
	return account.Account{
		Version: 1, Address: "ann@example.com", Username: "ann", Password: "s3cret-pass",
		IMAP: account.Endpoint{Host: "imap.example.com", Port: 993, Security: account.TLS},
		SMTP: account.Endpoint{Host: "smtp.example.com", Port: 587, Security: account.StartTLS},
	}
}

var _ = Describe("the saved connection", func() {
	It("round-trips through the sealed record", func() {
		a := good()
		a.Folders = []string{"INBOX", "Archive/2026"}
		tok, err := a.Encode()
		Expect(err).NotTo(HaveOccurred())
		got, err := account.Parse(tok)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(a))
	})

	It("refuses an empty, oversized, unknown or trailing record without echoing it", func() {
		for _, tok := range []string{"", strings.Repeat("x", account.MaxBytes+1), `{"v":1,"extra":true}`, `not json`, `{"v":2}`} {
			_, err := account.Parse(tok)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("extra"))
		}
		tok, _ := good().Encode()
		_, err := account.Parse(tok + " {}")
		Expect(err).To(MatchError(account.ErrMalformed))
	})

	DescribeTable("refuses what cannot be a connection",
		func(mutate func(*account.Account), field string) {
			a := good()
			mutate(&a)
			err := a.Validate()
			Expect(err).To(MatchError(account.ErrInvalid))
			Expect(err.Error()).To(ContainSubstring(field))
			Expect(err.Error()).NotTo(ContainSubstring("s3cret-pass"))
		},
		Entry("address with a name", func(a *account.Account) { a.Address = "Ann <ann@example.com>" }, "address"),
		Entry("address with a line break", func(a *account.Account) { a.Address = "ann@example.com\r\nBcc: x@y.z" }, "address"),
		Entry("address without a domain dot", func(a *account.Account) { a.Address = "ann@localhost" }, "address"),
		Entry("empty password", func(a *account.Account) { a.Password = "" }, "password"),
		Entry("control character in the username", func(a *account.Account) { a.Username = "ann\x00" }, "username"),
		Entry("imap host with a path", func(a *account.Account) { a.IMAP.Host = "imap.example.com/x" }, "imap host"),
		Entry("imap host with a port", func(a *account.Account) { a.IMAP.Host = "imap.example.com:993" }, "imap host"),
		Entry("imap host with a space", func(a *account.Account) { a.IMAP.Host = "imap example.com" }, "imap host"),
		Entry("imap port 0", func(a *account.Account) { a.IMAP.Port = 0 }, "imap port"),
		Entry("smtp port above 65535", func(a *account.Account) { a.SMTP.Port = 70000 }, "smtp port"),
		Entry("unknown security", func(a *account.Account) { a.SMTP.Security = "ssl" }, "smtp security"),
		Entry("too many folders", func(a *account.Account) { a.Folders = make([]string, 21) }, "folders"),
		Entry("folder with a line break", func(a *account.Account) { a.Folders = []string{"A\r\nB"} }, "folders"),
		Entry("drafts folder with a control character", func(a *account.Account) { a.DraftsFolder = "D\x01" }, "draftsFolder"),
	)

	It("accepts an IP literal host and a plain choice of no encryption only when named", func() {
		a := good()
		a.IMAP = account.Endpoint{Host: "203.0.113.7", Port: 143, Security: account.None}
		Expect(a.Validate()).To(Succeed())
		a.IMAP.Security = ""
		Expect(a.Validate()).To(HaveOccurred())
	})

	It("checks a folder against the allowlist, INBOX without regard to case", func() {
		a := good()
		Expect(a.AllowsFolder("Anything")).To(BeTrue())
		a.Folders = []string{"INBOX", "Receipts"}
		Expect(a.AllowsFolder("inbox")).To(BeTrue())
		Expect(a.AllowsFolder("Receipts")).To(BeTrue())
		Expect(a.AllowsFolder("receipts")).To(BeFalse())
		Expect(a.AllowsFolder("Private")).To(BeFalse())
	})
})
