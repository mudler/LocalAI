// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"io"
	"net/mail"
	"strings"

	"github.com/mudler/LocalAI/addons/mail/internal/mimetext"
	"github.com/mudler/LocalAI/addons/mail/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sendArgs() map[string]any {
	return map[string]any{"to": []string{"bob@example.org"}, "cc": []string{"carl@example.org"}, "subject": "Plans for Friday", "body": "Hello Bob,\nlunch at noon?\n"}
}

func preview(r *rig, tool string, args map[string]any, op string) (contract.Preview, error) {
	return r.rt.Preview(context.Background(), r.env(tool, args, op))
}

var _ = Describe("the write tools", func() {
	var r *rig
	BeforeEach(func() { r = newRig() })

	Describe("preview", func() {
		It("describes the send with facts from the arguments and opens no connection", func() {
			r.imap.Close()
			r.smtp.Close()
			p, err := preview(r, tools.ToolSendMessage, sendArgs(), "op-1")
			Expect(err).NotTo(HaveOccurred())
			s := string(p.Summary)
			Expect(s).To(ContainSubstring("Send an email"))
			Expect(s).To(ContainSubstring("bob@example.org"))
			Expect(s).To(ContainSubstring("carl@example.org"))
			Expect(s).To(ContainSubstring("Plans for Friday"))
			Expect(s).NotTo(ContainSubstring(password))
			p, err = preview(r, tools.ToolSaveDraft, sendArgs(), "op-2")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(p.Summary)).To(ContainSubstring("Save an email draft"))
		})

		It("refuses header injection and bad addresses", func() {
			for name, mutate := range map[string]func(map[string]any){
				"subject":   func(a map[string]any) { a["subject"] = "Hi\r\nBcc: evil@example.net" },
				"to":        func(a map[string]any) { a["to"] = []string{"bob@example.org\r\nBcc: evil@example.net"} },
				"cc":        func(a map[string]any) { a["cc"] = []string{"a@example.org\nBcc: e@example.net"} },
				"reply":     func(a map[string]any) { a["inReplyTo"] = "x@y.z>\r\nBcc: e@e.net" },
				"reference": func(a map[string]any) { a["references"] = []string{"a b"} },
				"no to":     func(a map[string]any) { a["to"] = []string{} },
				"too many": func(a map[string]any) {
					var l []string
					for i := 0; i < 21; i++ {
						l = append(l, "u"+strings.Repeat("a", i+1)+"@example.org")
					}
					a["to"] = l
				},
				"huge body": func(a map[string]any) { a["body"] = strings.Repeat("a", 64<<10+1) },
			} {
				a := sendArgs()
				mutate(a)
				_, err := preview(r, tools.ToolSendMessage, a, "op-"+name)
				Expect(err).To(MatchError(ContainSubstring("invalid request")), name)
			}
		})

		It("refuses a mailbox that is not the connection's and text that holds the password", func() {
			a := sendArgs()
			a["mailbox"] = "eve@example.org"
			_, err := preview(r, tools.ToolSendMessage, a, "op")
			refused(err)
			a = sendArgs()
			a["body"] = "my password is " + password
			_, err = preview(r, tools.ToolSendMessage, a, "op")
			Expect(refused(err)).NotTo(ContainSubstring(password))
		})
	})

	Describe("apply of a send", func() {
		apply := func(op string, args map[string]any) (contract.Receipt, error) {
			p, err := preview(r, tools.ToolSendMessage, args, op)
			Expect(err).NotTo(HaveOccurred())
			return r.rt.Apply(context.Background(), r.env(tools.ToolSendMessage, args, op), p.Revision)
		}

		It("sends over SMTP from the connection's address and records a receipt", func() {
			rc, err := apply("op-send", sendArgs())
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			sent := r.smtp.Sent()
			Expect(sent).To(HaveLen(1))
			Expect(sent[0].From).To(Equal(address))
			Expect(sent[0].To).To(ConsistOf("bob@example.org", "carl@example.org"))
			msg, err := mail.ReadMessage(strings.NewReader(string(sent[0].Data)))
			Expect(err).NotTo(HaveOccurred())
			Expect(msg.Header.Get("Subject")).To(Equal("Plans for Friday"))
			Expect(msg.Header.Get("Bcc")).To(BeEmpty())
			body, _ := io.ReadAll(msg.Body)
			Expect(strings.TrimSpace(string(mimetext.DecodeTransfer("quoted-printable", body)))).To(Equal("Hello Bob,\r\nlunch at noon?"))
			Expect(string(r.smtp.Transcript())).NotTo(ContainSubstring(password), "the password travels only inside AUTH, encoded")
			got, err := r.rt.Receipt(context.Background(), "op-send")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(contract.ReceiptCommitted))
		})

		It("does not send twice when the host applies the same operation again", func() {
			a := sendArgs()
			_, err := apply("op-twice", a)
			Expect(err).NotTo(HaveOccurred())
			p, _ := preview(r, tools.ToolSendMessage, a, "op-twice")
			rc, err := r.rt.Apply(context.Background(), r.env(tools.ToolSendMessage, a, "op-twice"), p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(r.smtp.Sent()).To(HaveLen(1))
		})

		It("rejects a changed message without sending", func() {
			a := sendArgs()
			p, _ := preview(r, tools.ToolSendMessage, a, "op-stale")
			a["subject"] = "Something else"
			rc, err := r.rt.Apply(context.Background(), r.env(tools.ToolSendMessage, a, "op-stale"), p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptRejected))
			Expect(r.smtp.Sent()).To(BeEmpty())
		})

		It("refuses with a fixed sentence when the server refuses, and allows a new try", func() {
			r.smtp.RejectRecipient("bob@example.org")
			_, err := apply("op-rej", sendArgs())
			Expect(refused(err)).To(ContainSubstring("refused a recipient"))
			Expect(r.smtp.Sent()).To(BeEmpty())
			rc, _ := r.rt.Receipt(context.Background(), "op-rej")
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		})

		It("reports a wrong SMTP password without the secret", func() {
			r.acct.Password = "not-the-password-1"
			_, err := apply("op-auth", sendArgs())
			msg := refused(err)
			Expect(msg).To(ContainSubstring("username or password for sending"))
			Expect(msg).NotTo(ContainSubstring("not-the-password-1"))
		})

		It("does not repeat a send whose outcome is unknown", func() {
			r.smtp.DropAfterData()
			_, err := apply("op-lost", sendArgs())
			Expect(err).To(HaveOccurred())
			Expect(err).NotTo(MatchError(ContainSubstring("invalid request")), "an uncertain outcome is not a refusal")
			rc, rerr := r.rt.Receipt(context.Background(), "op-lost")
			Expect(rerr).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
			Expect(r.smtp.Sent()).To(HaveLen(1))
			_, err = apply("op-lost", sendArgs())
			Expect(err).To(HaveOccurred())
			Expect(r.smtp.Sent()).To(HaveLen(1), "the second try did not send again")
		})

		It("sends a reply with the thread headers", func() {
			a := sendArgs()
			a["inReplyTo"] = "<orig@example.org>"
			a["references"] = []string{"root@example.org", "orig@example.org"}
			_, err := apply("op-reply", a)
			Expect(err).NotTo(HaveOccurred())
			msg, _ := mail.ReadMessage(strings.NewReader(string(r.smtp.Sent()[0].Data)))
			Expect(msg.Header.Get("In-Reply-To")).To(Equal("<orig@example.org>"))
			Expect(msg.Header.Get("References")).To(Equal("<root@example.org> <orig@example.org>"))
		})
	})

	Describe("apply of a draft", func() {
		apply := func(op string, args map[string]any) (contract.Receipt, error) {
			p, err := preview(r, tools.ToolSaveDraft, args, op)
			Expect(err).NotTo(HaveOccurred())
			return r.rt.Apply(context.Background(), r.env(tools.ToolSaveDraft, args, op), p.Revision)
		}

		It("appends to the folder the server marks as Drafts and sends nothing", func() {
			rc, err := apply("op-draft", sendArgs())
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			got := r.imap.Appended()
			Expect(got).To(HaveLen(1))
			Expect(got[0].Mailbox).To(Equal("Drafts"))
			Expect(got[0].Flags).To(ContainElement(`\Draft`))
			Expect(string(got[0].Raw)).To(ContainSubstring("Subject: Plans for Friday"))
			Expect(r.smtp.Sent()).To(BeEmpty())
		})

		It("does not duplicate a draft when the operation is applied again", func() {
			_, err := apply("op-d2", sendArgs())
			Expect(err).NotTo(HaveOccurred())
			_, err = apply("op-d2", sendArgs())
			Expect(err).NotTo(HaveOccurred())
			Expect(r.imap.Appended()).To(HaveLen(1))
		})

		It("says so when there is no Drafts folder", func() {
			r2 := newRig()
			r2.imap.Close()
			r = r2
			r.imap = newRigNoDrafts(r)
			_, err := apply("op-d3", sendArgs())
			Expect(refused(err)).To(ContainSubstring("Drafts folder"))
		})
	})
})
