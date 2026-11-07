// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"errors"
	"mime"
	"net/mail"
	"strings"

	"github.com/mudler/LocalAI/addons/gmail/gmailtest"
	"github.com/mudler/LocalAI/addons/gmail/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the write tools", func() {
	var w *world
	BeforeEach(func() {
		w = newWorld()
		w.srv.AddDraft("r-person", "To: bob@example.test\r\nSubject: Lunch\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nShall we meet at noon?")
	})

	wenv := func(op, tool string, args map[string]any, scopes ...string) contract.Envelope {
		a := map[string]any{"label": "Work"}
		for k, v := range args {
			a[k] = v
		}
		e := env(tool, a, scopes...)
		e.Request.OperationID = op
		e.Request.Action = "write"
		return e
	}
	// apply previews and applies, as the host does after a person approved.
	apply := func(op, tool string, args map[string]any) (contract.Receipt, error) {
		e := wenv(op, tool, args, bothScopes...)
		p, err := w.rt.Preview(ctx(), e)
		if err != nil {
			return contract.Receipt{}, err
		}
		return w.rt.Apply(ctx(), e, p.Revision)
	}
	postCalls := func() []gmailtest.Call {
		var out []gmailtest.Call
		for _, c := range w.srv.Calls() {
			if c.Method != "GET" {
				out = append(out, c)
			}
		}
		return out
	}
	draftArgs := map[string]any{"to": []string{"bob@example.test"}, "subject": "Hello", "body": "Hi Bob,\nsee you.\n"}

	It("degrades without the optional scope: a clear refusal that names the scope, and nothing is sent", func() {
		for tool, args := range map[string]map[string]any{"create-draft": draftArgs, "send-draft": {"draftId": "r-person"}} {
			e := wenv("op-"+tool, tool, args, tools.ScopeRead)
			_, err := w.rt.Preview(ctx(), e)
			Expect(err).To(MatchError(protocol.ErrBadRequest), tool)
			Expect(err.Error()).To(ContainSubstring(tools.ScopeWriteName), tool)
			Expect(err.Error()).To(ContainSubstring("Reconnect"), tool)
			_, err = w.rt.Apply(ctx(), e, contract.Revision{Schema: 1, Data: 1})
			Expect(err).To(MatchError(ContainSubstring(tools.ScopeWriteName)), tool)
		}
		_, err := w.rt.Preview(ctx(), wenv("op-x", "create-draft", draftArgs))
		Expect(err).To(MatchError(ContainSubstring(tools.ScopeWriteName)), "no granted scopes at all")
		Expect(w.srv.Calls()).To(BeEmpty(), "the service was never asked, so it never answered 403")
	})

	It("keeps the read tools working with the read scope alone", func() {
		_, _, err := w.read("get-message", map[string]any{"messageId": "plain"}, tools.ScopeRead)
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("create-draft", func() {
		It("previews with the recipients and the text, and changes nothing", func() {
			p, err := w.rt.Preview(ctx(), wenv("op1", "create-draft", draftArgs, bothScopes...))
			Expect(err).NotTo(HaveOccurred())
			Expect(postCalls()).To(BeEmpty())
			var s struct {
				Count  int            `json:"count"`
				Sample map[string]any `json:"sample"`
			}
			Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
			Expect(s.Sample).To(HaveKeyWithValue("action", "Save a draft"))
			Expect(s.Sample["to"]).To(ConsistOf("bob@example.test"))
			Expect(s.Sample).To(HaveKeyWithValue("subject", "Hello"))
			Expect(s.Sample).To(HaveKeyWithValue("body", "Hi Bob,\nsee you.\n"))
			Expect(s.Sample["notice"]).To(ContainSubstring("does not send"))
			Expect(p.OperationID).To(Equal("op1"))
		})

		It("saves one draft with plain headers and an encoded body, and sends nothing", func() {
			args := map[string]any{"to": []string{"bob@example.test", "eve@example.test"}, "cc": []string{"cc@example.test"}, "subject": "Grüße ✓", "body": "Zeile eins\nZeile zwei ✓\n"}
			rc, err := apply("op-draft", "create-draft", args)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			ds := w.srv.Drafts()
			var mine gmailtest.Draft
			for _, d := range ds {
				if d.ID != "r-person" {
					mine = d
				}
			}
			Expect(mine.ID).NotTo(BeEmpty())
			m, err := mail.ReadMessage(strings.NewReader(mine.Raw))
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Header.Get("To")).To(Equal("bob@example.test, eve@example.test"))
			Expect(m.Header.Get("Cc")).To(Equal("cc@example.test"))
			subj, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
			Expect(err).NotTo(HaveOccurred())
			Expect(subj).To(Equal("Grüße ✓"))
			Expect(m.Header.Get("Content-Transfer-Encoding")).To(Equal("base64"))
			Expect(m.Header).NotTo(HaveKey("Bcc"))
			Expect(string(mine.Message.Payload.Data)).To(Equal("Zeile eins\r\nZeile zwei ✓\r\n"))
			Expect(w.srv.Sent()).To(BeEmpty())
			Expect(string(rc.Result.Payload)).To(ContainSubstring(mine.ID))
			for _, c := range postCalls() {
				Expect(c.Path).To(Equal("/gmail/v1/users/me/drafts"), "only drafts.create")
			}
		})

		It("replies in the thread with In-Reply-To and References, and takes the subject from the message", func() {
			w.srv.AddMessage(gmailtest.Plain(gmailtest.Mail{ID: "r1", ThreadID: "th9", From: "dan@example.test", To: "person@example.test", Subject: "Plan", MessageID: "<r1@mail.example.test>", Labels: []string{"Label_1"}}))
			args := map[string]any{"to": []string{"dan@example.test"}, "body": "ok", "replyToMessageId": "t1a"}
			rc, err := apply("op-reply", "create-draft", args)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			var raw string
			var thread string
			for _, d := range w.srv.Drafts() {
				if d.ID != "r-person" {
					raw, thread = d.Raw, d.Message.ThreadID
				}
			}
			m, err := mail.ReadMessage(strings.NewReader(raw))
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Header.Get("In-Reply-To")).To(Equal("<t1a@mail.example.test>"))
			Expect(m.Header.Get("References")).To(Equal("<t1a@mail.example.test>"))
			Expect(m.Header.Get("Subject")).To(Equal("Re: Plan"))
			Expect(thread).To(Equal("thread1"))
		})

		It("chains the References of the message it answers", func() {
			w.srv.AddMessage(gmailtest.Message{ID: "chain", ThreadID: "thc", Labels: []string{"Label_1"}, Payload: &gmailtest.Part{MimeType: "text/plain", Headers: []gmailtest.Header{
				{Name: "Subject", Value: "Re: x"}, {Name: "Message-ID", Value: "<c3@x.test>"}, {Name: "References", Value: "<c1@x.test> junk <c2@x.test>"}}}})
			_, err := apply("op-chain", "create-draft", map[string]any{"to": []string{"a@example.test"}, "body": "ok", "replyToMessageId": "chain"})
			Expect(err).NotTo(HaveOccurred())
			for _, d := range w.srv.Drafts() {
				if d.ID != "r-person" {
					m, _ := mail.ReadMessage(strings.NewReader(d.Raw))
					Expect(m.Header.Get("References")).To(Equal("<c1@x.test> <c2@x.test> <c3@x.test>"))
					Expect(m.Header.Get("Subject")).To(Equal("Re: x"))
				}
			}
		})

		It("refuses a reply to a message outside the label or without an ID", func() {
			_, err := apply("op-o", "create-draft", map[string]any{"to": []string{"a@example.test"}, "body": "ok", "replyToMessageId": "secret"})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			w.srv.AddMessage(gmailtest.Message{ID: "noid", Labels: []string{"Label_1"}, Payload: &gmailtest.Part{MimeType: "text/plain"}})
			_, err = apply("op-n", "create-draft", map[string]any{"to": []string{"a@example.test"}, "body": "ok", "replyToMessageId": "noid"})
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			Expect(postCalls()).To(BeEmpty())
		})

		DescribeTable("refuses what could break a header or exceed a bound", func(args map[string]any) {
			_, err := w.rt.Preview(ctx(), wenv("op", "create-draft", args, bothScopes...))
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			Expect(w.srv.Calls()).To(BeEmpty())
		},
			Entry("a line break in an address", map[string]any{"to": []string{"a@example.test\r\nBcc: spy@example.test"}, "subject": "s", "body": "b"}),
			Entry("a second address in one entry", map[string]any{"to": []string{"a@example.test, b@example.test"}, "subject": "s", "body": "b"}),
			Entry("a name with an address", map[string]any{"to": []string{"Al <a@example.test>"}, "subject": "s", "body": "b"}),
			Entry("no recipient", map[string]any{"to": []string{}, "subject": "s", "body": "b"}),
			Entry("a line feed in the subject", map[string]any{"to": []string{"a@example.test"}, "subject": "s\nBcc: spy@example.test", "body": "b"}),
			Entry("a carriage return in the subject", map[string]any{"to": []string{"a@example.test"}, "subject": "s\rX: y", "body": "b"}),
			Entry("no subject on a new mail", map[string]any{"to": []string{"a@example.test"}, "body": "b"}),
			Entry("a long subject", map[string]any{"to": []string{"a@example.test"}, "subject": strings.Repeat("s", 201), "body": "b"}),
			Entry("no body", map[string]any{"to": []string{"a@example.test"}, "subject": "s"}),
			Entry("a body that is too large", map[string]any{"to": []string{"a@example.test"}, "subject": "s", "body": strings.Repeat("b", tools.MaxWriteBody+1)}),
			Entry("a control character in the body", map[string]any{"to": []string{"a@example.test"}, "subject": "s", "body": "b\x00"}),
			Entry("the token in the body", map[string]any{"to": []string{"a@example.test"}, "subject": "s", "body": "key " + token}),
			Entry("a draft ID", map[string]any{"to": []string{"a@example.test"}, "subject": "s", "body": "b", "draftId": "x"}),
			Entry("an unknown field", map[string]any{"to": []string{"a@example.test"}, "subject": "s", "body": "b", "bcc": []string{"x@example.test"}}),
		)

		It("caps the recipients at 20 together", func() {
			var to, cc []string
			for i := range 11 {
				to = append(to, "u"+string(rune('a'+i))+"@example.test")
			}
			for i := range 10 {
				cc = append(cc, "c"+string(rune('a'+i))+"@example.test")
			}
			_, err := w.rt.Preview(ctx(), wenv("op", "create-draft", map[string]any{"to": to, "cc": cc, "subject": "s", "body": "b"}, bothScopes...))
			Expect(err).To(MatchError(ContainSubstring("at most 20")))
			_, err = w.rt.Preview(ctx(), wenv("op", "create-draft", map[string]any{"to": to[:10], "cc": cc, "subject": "s", "body": "b"}, bothScopes...))
			Expect(err).NotTo(HaveOccurred())
		})

		It("keeps a line of the body that looks like a header inside the body", func() {
			_, err := apply("op-inj", "create-draft", map[string]any{"to": []string{"a@example.test"}, "subject": "s", "body": "hi\r\n\r\nBcc: spy@example.test\nTo: other@example.test"})
			Expect(err).NotTo(HaveOccurred())
			for _, d := range w.srv.Drafts() {
				if d.ID == "r-person" {
					continue
				}
				m, _ := mail.ReadMessage(strings.NewReader(d.Raw))
				Expect(m.Header).NotTo(HaveKey("Bcc"))
				Expect(m.Header.Get("To")).To(Equal("a@example.test"))
			}
		})

		It("does not save twice when the answer was lost, and leaves the decision to the person", func() {
			w.srv.DropWrites(true)
			e := wenv("op-lost", "create-draft", draftArgs, bothScopes...)
			p, err := w.rt.Preview(ctx(), e)
			Expect(err).NotTo(HaveOccurred())
			_, err = w.rt.Apply(ctx(), e, p.Revision)
			Expect(err).To(HaveOccurred())
			Expect(w.srv.Drafts()).To(HaveLen(2), "the service made it")
			rc, err := w.rt.Receipt(ctx(), "op-lost")
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
			w.srv.DropWrites(false)
			_, err = w.rt.Apply(ctx(), e, p.Revision)
			Expect(err).To(MatchError(ContainSubstring("Check Drafts and Sent")))
			Expect(w.srv.Drafts()).To(HaveLen(2), "not made again")
		})

		It("answers the same receipt when applied twice", func() {
			e := wenv("op-twice", "create-draft", draftArgs, bothScopes...)
			p, _ := w.rt.Preview(ctx(), e)
			a, err := w.rt.Apply(ctx(), e, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			b, err := w.rt.Apply(ctx(), e, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(b.Result.Payload).To(Equal(a.Result.Payload))
			Expect(w.srv.Drafts()).To(HaveLen(2))
		})
	})

	Describe("send-draft", func() {
		send := map[string]any{"draftId": "r-person"}

		It("previews the recipients, the subject and the text of the draft as it is now", func() {
			p, err := w.rt.Preview(ctx(), wenv("op1", "send-draft", send, bothScopes...))
			Expect(err).NotTo(HaveOccurred())
			var s struct {
				Sample map[string]any `json:"sample"`
			}
			Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
			Expect(s.Sample).To(HaveKeyWithValue("action", "Send an e-mail"))
			Expect(s.Sample).To(HaveKeyWithValue("to", "bob@example.test"))
			Expect(s.Sample).To(HaveKeyWithValue("subject", "Lunch"))
			Expect(s.Sample).To(HaveKeyWithValue("body", "Shall we meet at noon?"))
			Expect(s.Sample["notice"]).To(ContainSubstring("cannot be taken back"))
			Expect(w.srv.Sent()).To(BeEmpty())
			Expect(postCalls()).To(BeEmpty())
		})

		It("sends an existing draft once, and only that", func() {
			rc, err := apply("op-send", "send-draft", send)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(w.srv.Sent()).To(HaveLen(1))
			Expect(w.srv.Sent()[0].ID).To(Equal("r-person"))
			Expect(w.srv.Drafts()).To(BeEmpty())
			again, err := w.rt.Apply(ctx(), wenv("op-send", "send-draft", send, bothScopes...), rc.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(again.State).To(Equal(contract.ReceiptCommitted))
			Expect(w.srv.Sent()).To(HaveLen(1))
			for _, c := range postCalls() {
				Expect(c.Path).To(Equal("/gmail/v1/users/me/drafts/send"))
			}
		})

		It("sends what was created by create-draft after the person approved both", func() {
			_, err := apply("op-c", "create-draft", draftArgs)
			Expect(err).NotTo(HaveOccurred())
			var id string
			for _, d := range w.srv.Drafts() {
				if d.ID != "r-person" {
					id = d.ID
				}
			}
			_, err = apply("op-s", "send-draft", map[string]any{"draftId": id})
			Expect(err).NotTo(HaveOccurred())
			Expect(w.srv.Sent()).To(HaveLen(1))
			m, _ := mail.ReadMessage(strings.NewReader(w.srv.Sent()[0].Raw))
			Expect(m.Header.Get("Subject")).To(Equal("Hello"))
		})

		It("refuses the arguments of a draft, a missing draft and a draft without a recipient", func() {
			_, err := w.rt.Preview(ctx(), wenv("op", "send-draft", map[string]any{"draftId": "r-person", "to": []string{"x@example.test"}}, bothScopes...))
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			_, err = w.rt.Preview(ctx(), wenv("op", "send-draft", map[string]any{"draftId": "nope"}, bothScopes...))
			Expect(err).To(MatchError(ContainSubstring("no longer exists")))
			w.srv.AddDraft("r-empty", "Subject: none\r\n\r\ntext")
			_, err = w.rt.Preview(ctx(), wenv("op", "send-draft", map[string]any{"draftId": "r-empty"}, bothScopes...))
			Expect(err).To(MatchError(ContainSubstring("no recipient")))
			var many []string
			for i := range 21 {
				many = append(many, "u"+string(rune('a'+i))+"@example.test")
			}
			w.srv.AddDraft("r-many", "To: "+strings.Join(many, ", ")+"\r\nSubject: x\r\n\r\ntext")
			_, err = w.rt.Preview(ctx(), wenv("op", "send-draft", map[string]any{"draftId": "r-many"}, bothScopes...))
			Expect(err).To(MatchError(ContainSubstring("more than 20")))
			Expect(w.srv.Sent()).To(BeEmpty())
		})

		It("rejects an approval of a draft that changed after the preview", func() {
			e := wenv("op-stale", "send-draft", send, bothScopes...)
			p, err := w.rt.Preview(ctx(), e)
			Expect(err).NotTo(HaveOccurred())
			w.srv.AddDraft("r-person", "To: attacker@example.test\r\nSubject: Lunch\r\n\r\nShall we meet at noon?")
			rc, err := w.rt.Apply(ctx(), e, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptRejected))
			Expect(w.srv.Sent()).To(BeEmpty())
		})

		It("treats a draft that is gone after an unknown attempt as sent, and not as a new send", func() {
			w.srv.DropWrites(true)
			e := wenv("op-lost", "send-draft", send, bothScopes...)
			p, _ := w.rt.Preview(ctx(), e)
			_, err := w.rt.Apply(ctx(), e, p.Revision)
			Expect(err).To(HaveOccurred())
			Expect(w.srv.Sent()).To(HaveLen(1))
			w.srv.DropWrites(false)
			rc, err := w.rt.Apply(ctx(), e, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(string(rc.Result.Payload)).To(ContainSubstring("earlier attempt sent it"))
			Expect(w.srv.Sent()).To(HaveLen(1))
		})

		It("tells the model how long to wait on a limit, and sends nothing", func() {
			w.srv.Inject("/gmail/v1/users/me/drafts/send", 429, "rateLimitExceeded", "11", 1)
			_, err := apply("op-rl", "send-draft", send)
			Expect(err).To(MatchError(protocol.ErrBadRequest))
			Expect(err.Error()).To(ContainSubstring("Retry in 11 s"))
			Expect(w.srv.Sent()).To(BeEmpty())
		})

		It("turns a 401 into credential_rejected", func() {
			e := wenv("op-401", "send-draft", send, bothScopes...)
			p, _ := w.rt.Preview(ctx(), e)
			w.srv.RevokeToken(token)
			_, err := w.rt.Apply(ctx(), e, p.Revision)
			Expect(errors.Is(err, contract.ErrCredentialRejected)).To(BeTrue())
		})
	})

	It("asks for nothing that changes, labels or deletes mail", func() {
		_, _ = apply("op-a", "create-draft", draftArgs)
		_, _ = apply("op-b", "send-draft", map[string]any{"draftId": "r-person"})
		for _, c := range w.srv.Calls() {
			Expect(c.Method).NotTo(BeElementOf("DELETE", "PUT", "PATCH"))
			for _, bad := range []string{"trash", "modify", "delete", "settings", "filters", "forwarding", "batch"} {
				Expect(c.Path).NotTo(ContainSubstring(bad))
			}
		}
	})
})
