// SPDX-License-Identifier: MIT
package compose_test

import (
	"io"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/mail/internal/compose"
	"github.com/mudler/LocalAI/addons/mail/internal/mimetext"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCompose(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail compose")
}

func good() compose.Message {
	return compose.Message{FromName: "Ann Ångström", FromAddress: "ann@example.com", To: []string{"bob@example.org"}, Subject: "Café plans", Body: "Hello Bob,\n\nSee you at nine.\n",
		MessageID: "op1@example.com", Date: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)}
}

var _ = Describe("building a message", func() {
	It("makes a plain-text, quoted-printable UTF-8 message that parses back", func() {
		m := good()
		m.Cc = []string{"carl@example.org"}
		m.InReplyTo = "orig@example.org"
		m.References = []string{"root@example.org", "orig@example.org"}
		raw, err := compose.Build(m)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(string(raw), "\n")).To(Equal(strings.Count(string(raw), "\r\n")), "CRLF only")
		msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
		Expect(err).NotTo(HaveOccurred())
		Expect(mimetext.DecodeWords(msg.Header.Get("Subject"))).To(Equal("Café plans"))
		Expect(msg.Header.Get("In-Reply-To")).To(Equal("<orig@example.org>"))
		Expect(msg.Header.Get("References")).To(Equal("<root@example.org> <orig@example.org>"))
		Expect(msg.Header.Get("Message-ID")).To(Equal("<op1@example.com>"))
		Expect(msg.Header.Get("Content-Type")).To(Equal("text/plain; charset=utf-8"))
		from, err := msg.Header.AddressList("From")
		Expect(err).NotTo(HaveOccurred())
		Expect(from[0].Address).To(Equal("ann@example.com"))
		Expect(from[0].Name).To(Equal("Ann Ångström"))
		body, _ := io.ReadAll(msg.Body)
		Expect(string(mimetext.DecodeTransfer("quoted-printable", body))).To(Equal("Hello Bob,\r\n\r\nSee you at nine.\r\n"))
		Expect(string(raw)).NotTo(ContainSubstring("text/html"))
	})

	It("keeps a long non-ASCII line within the line limit", func() {
		m := good()
		m.Body = strings.Repeat("è", 200)
		raw, err := compose.Build(m)
		Expect(err).NotTo(HaveOccurred())
		for _, l := range strings.Split(string(raw), "\r\n") {
			Expect(len(l)).To(BeNumerically("<=", 78))
		}
	})

	DescribeTable("refuses header injection and bad values",
		func(mutate func(*compose.Message)) {
			m := good()
			mutate(&m)
			_, err := compose.Build(m)
			Expect(err).To(MatchError(compose.ErrInvalid))
		},
		Entry("line break in the subject", func(m *compose.Message) { m.Subject = "Hi\r\nBcc: evil@example.net" }),
		Entry("line feed in the subject", func(m *compose.Message) { m.Subject = "Hi\nBcc: evil@example.net" }),
		Entry("unicode line separator in the subject", func(m *compose.Message) { m.Subject = "Hi Bcc: evil@example.net" }),
		Entry("line break in a to address", func(m *compose.Message) { m.To = []string{"bob@example.org\r\nBcc: evil@example.net"} }),
		Entry("two addresses in one", func(m *compose.Message) { m.To = []string{"bob@example.org,evil@example.net"} }),
		Entry("a display name in to", func(m *compose.Message) { m.To = []string{"Bob <bob@example.org>"} }),
		Entry("a line break in cc", func(m *compose.Message) { m.Cc = []string{"x@example.org\nBcc: y@example.net"} }),
		Entry("a line break in the sender name", func(m *compose.Message) { m.FromName = "Ann\r\nBcc: e@e.org" }),
		Entry("a line break in in-reply-to", func(m *compose.Message) { m.InReplyTo = "a@b.c>\r\nBcc: e@e.org" }),
		Entry("a bad reference", func(m *compose.Message) { m.References = []string{"a b@c.d"} }),
		Entry("no recipient", func(m *compose.Message) { m.To = nil }),
		Entry("21 recipients", func(m *compose.Message) {
			m.To = nil
			for i := 0; i < 21; i++ {
				m.To = append(m.To, "u"+string(rune('a'+i))+"@example.org")
			}
		}),
		Entry("a repeated recipient", func(m *compose.Message) { m.Cc = []string{"BOB@example.org"} }),
		Entry("an empty subject", func(m *compose.Message) { m.Subject = "  " }),
		Entry("an empty body", func(m *compose.Message) { m.Body = "\n" }),
		Entry("a body over the limit", func(m *compose.Message) { m.Body = strings.Repeat("a", compose.MaxBodyBytes+1) }),
		Entry("a NUL in the body", func(m *compose.Message) { m.Body = "a\x00b" }),
	)

	It("allows a line break in the body and puts nothing of it in the headers", func() {
		m := good()
		m.Body = "Bcc: evil@example.net\r\nstill body"
		raw, err := compose.Build(m)
		Expect(err).NotTo(HaveOccurred())
		msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
		Expect(msg.Header.Get("Bcc")).To(BeEmpty())
	})
})
