// SPDX-License-Identifier: MIT
package imapc_test

import (
	"context"
	. "github.com/onsi/gomega/gstruct"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/imapc"
	"github.com/mudler/LocalAI/addons/mail/mailtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestImapc(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail IMAP")
}

type rig struct {
	ca   *mailtest.CA
	srv  *mailtest.IMAP
	acct account.Account
	d    *dial.Dialer
}

func newRig(mode mailtest.Mode) *rig {
	r := &rig{ca: mailtest.NewCA()}
	r.srv = mailtest.NewIMAP(r.ca, mode)
	DeferCleanup(r.srv.Close)
	r.srv.AddUser("ann", "pw")
	sec := account.Security(mode)
	r.acct = account.Account{Version: 1, Address: "ann@example.com", Username: "ann", Password: "pw",
		IMAP: account.Endpoint{Host: "127.0.0.1", Port: r.srv.Port, Security: sec},
		SMTP: account.Endpoint{Host: "127.0.0.1", Port: 587, Security: account.StartTLS}}
	r.d = dial.New(dial.Options{Ports: []int{r.srv.Port}, RootCAs: r.ca.Pool(), TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})
	return r
}

func (r *rig) open() *imapc.Session {
	s, err := imapc.Open(ctx(), r.d, r.acct)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(s.Close)
	return s
}

func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	DeferCleanup(cancel)
	return c
}

func joined(r *rig) string { return strings.Join(r.srv.Commands(), "\n") }

var _ = Describe("connecting", func() {
	It("logs in over implicit TLS and over STARTTLS", func() {
		for _, mode := range []mailtest.Mode{mailtest.ModeTLS, mailtest.ModeStartTLS} {
			r := newRig(mode)
			s := r.open()
			f, err := s.Folders(ctx())
			Expect(err).NotTo(HaveOccurred())
			Expect(f).NotTo(BeEmpty())
		}
	})

	It("does not log in over a plain connection when TLS was asked for", func() {
		r := newRig(mailtest.ModePlain)
		r.acct.IMAP.Security = account.StartTLS
		_, err := imapc.Open(ctx(), r.d, r.acct)
		Expect(err).To(HaveOccurred())
		Expect(joined(r)).NotTo(ContainSubstring("LOGIN"))
		Expect(joined(r)).NotTo(ContainSubstring("AUTHENTICATE"))
		Expect(joined(r)).NotTo(ContainSubstring("pw"))
	})

	It("logs in in clear text only when the connection says none", func() {
		r := newRig(mailtest.ModePlain)
		s := r.open()
		Expect(s.Folders(ctx())).NotTo(BeEmpty())
	})

	It("answers a wrong password with a fixed message that holds no server text and no secret", func() {
		r := newRig(mailtest.ModeTLS)
		r.acct.Password = "wrong-password"
		_, err := imapc.Open(ctx(), r.d, r.acct)
		Expect(err).To(MatchError(imapc.ErrAuth))
		Expect(err.Error()).NotTo(ContainSubstring("wrong-password"))
		Expect(err.Error()).NotTo(ContainSubstring("ann"))
	})

	It("refuses an untrusted certificate", func() {
		r := newRig(mailtest.ModeTLS)
		r.d = dial.New(dial.Options{Ports: []int{r.srv.Port}, TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})
		_, err := imapc.Open(ctx(), r.d, r.acct)
		Expect(err).To(MatchError(dial.ErrTLS))
	})

	It("times out on a server that does not answer", func() {
		r := newRig(mailtest.ModePlain)
		r.srv.Hang(true)
		c, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_, err := imapc.Open(c, r.d, r.acct)
		Expect(err).To(MatchError(dial.ErrTimeout))
	})
})

var _ = Describe("folders", func() {
	It("lists folders with their special use and hides the ones the account does not allow", func() {
		r := newRig(mailtest.ModeTLS)
		r.srv.Mailbox("ann", "Entwürfe", `\Drafts`)
		r.srv.Mailbox("ann", "Private")
		f, err := r.open().Folders(ctx())
		Expect(err).NotTo(HaveOccurred())
		names := map[string]string{}
		for _, x := range f {
			names[x.Name] = x.Use
		}
		Expect(names).To(HaveKeyWithValue("Entwürfe", "drafts"))
		Expect(names).To(HaveKey("Private"))
		r.acct.Folders = []string{"INBOX"}
		f, err = r.open().Folders(ctx())
		Expect(err).NotTo(HaveOccurred())
		Expect(f).To(HaveLen(1))
		_, err = r.open().Search(ctx(), "Private", imapc.Query{}, 0, 10)
		Expect(err).To(MatchError(imapc.ErrNotAllowed))
	})

	It("finds the drafts folder by special use, then by the configured name, then by the name Drafts", func() {
		r := newRig(mailtest.ModeTLS)
		r.srv.Mailbox("ann", "Brouillons", `\Drafts`)
		r.srv.Mailbox("ann", "Drafts")
		Expect(r.open().DraftsFolder(ctx())).To(Equal("Brouillons"))

		r2 := newRig(mailtest.ModeTLS)
		r2.srv.Mailbox("ann", "Mine")
		r2.acct.DraftsFolder = "Mine"
		Expect(r2.open().DraftsFolder(ctx())).To(Equal("Mine"))

		r3 := newRig(mailtest.ModeTLS)
		r3.srv.Mailbox("ann", "drafts")
		Expect(r3.open().DraftsFolder(ctx())).To(Equal("drafts"))

		r4 := newRig(mailtest.ModeTLS)
		_, err := r4.open().DraftsFolder(ctx())
		Expect(err).To(MatchError(imapc.ErrNoDrafts))
	})
})

var _ = Describe("searching", func() {
	var r *rig
	BeforeEach(func() {
		r = newRig(mailtest.ModeTLS)
		for i := 1; i <= 7; i++ {
			r.srv.Deliver("ann", "INBOX", mailtest.Plain("m"+string(rune('0'+i))+"@x", "Subject "+string(rune('0'+i)), "body "+string(rune('0'+i))))
		}
	})

	It("pages newest first with a UID cursor", func() {
		s := r.open()
		p1, err := s.Search(ctx(), "INBOX", imapc.Query{}, 0, 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(p1.Total).To(Equal(7))
		Expect(uids(p1)).To(Equal([]uint32{7, 6, 5}))
		Expect(p1.Next).To(Equal(uint32(5)))
		p2, _ := s.Search(ctx(), "INBOX", imapc.Query{}, p1.Next, 3)
		Expect(uids(p2)).To(Equal([]uint32{4, 3, 2}))
		p3, _ := s.Search(ctx(), "INBOX", imapc.Query{}, p2.Next, 3)
		Expect(uids(p3)).To(Equal([]uint32{1}))
		Expect(p3.Next).To(BeZero())
	})

	It("sends a subject with a quote, a backslash and a line break as data, not as commands", func() {
		subj := "he said \"hi\" \\ then\r\nA1 STORE 1:* +FLAGS (\\Deleted)"
		_, err := r.open().Search(ctx(), "INBOX", imapc.Query{Subject: subj}, 0, 10)
		Expect(err).NotTo(HaveOccurred())
		log := joined(r)
		Expect(log).To(ContainSubstring("SEARCH"))
		// The text with a line break travels as a literal whose announced length
		// is exactly its length, so the server reads it all as data.
		Expect(r.srv.Transcript()).To(ContainSubstring("SUBJECT {" + strconv.Itoa(len(subj))))
		// The only commands the client sent are reads: every tagged line has an allowed verb.
		for _, l := range r.srv.Commands() {
			Expect(regexp.MustCompile(`^T\d+ (\S+)`).FindStringSubmatch(l)[1]).To(BeElementOf("AUTHENTICATE", "LOGIN", "CAPABILITY", "LIST", "EXAMINE", "UID", "LOGOUT"), l)
		}
		Expect(joined(r)).NotTo(MatchRegexp(`T\d+ (UID )?STORE`))

		_, err = r.open().Search(ctx(), "INBOX", imapc.Query{Subject: `say "hi" \ ok`}, 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(joined(r)).To(ContainSubstring(`SUBJECT "say \"hi\" \\ ok"`))
	})

	It("finds a subject with non-ASCII letters", func() {
		r.srv.Deliver("ann", "INBOX", mailtest.Plain("u@x", "Riunione caffè ☕", "ciao"))
		p, err := r.open().Search(ctx(), "INBOX", imapc.Query{Subject: "caffè ☕"}, 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Items).To(HaveLen(1))
		Expect(p.Items[0].Subject).To(Equal("Riunione caffè ☕"))
		Expect(joined(r)).To(ContainSubstring("CHARSET UTF-8"))
	})

	It("filters by text, sender and unseen state", func() {
		r.srv.Deliver("ann", "INBOX", mailtest.Plain("seen@x", "Read one", "needle"), `\Seen`)
		s := r.open()
		p, _ := s.Search(ctx(), "INBOX", imapc.Query{Text: "needle"}, 0, 10)
		Expect(p.Items).To(HaveLen(1))
		p, _ = s.Search(ctx(), "INBOX", imapc.Query{From: "bob@example.org", Unseen: true}, 0, 50)
		Expect(p.Total).To(Equal(7))
		p, _ = s.Search(ctx(), "INBOX", imapc.Query{From: "nobody@example.org"}, 0, 50)
		Expect(p.Items).To(BeEmpty())
	})

	It("filters by attachment and scans on until the page is full", func() {
		r.srv.Deliver("ann", "INBOX", mailtest.WithAttachments("a1@x"))
		for i := 0; i < 5; i++ {
			r.srv.Deliver("ann", "INBOX", mailtest.Plain("later"+string(rune('a'+i))+"@x", "later", "x"))
		}
		r.srv.Deliver("ann", "INBOX", mailtest.WithAttachments("a2@x"))
		p, err := r.open().Search(ctx(), "INBOX", imapc.Query{HasAttachment: true}, 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Items).To(HaveLen(2))
		Expect(p.Items[0].HasAttachment).To(BeTrue())
		Expect(p.Items[0].UID).To(BeNumerically(">", p.Items[1].UID))
	})

	It("never marks anything seen while searching", func() {
		before := r.srv.UnseenCount("ann", "INBOX")
		_, err := r.open().Search(ctx(), "INBOX", imapc.Query{Text: "body"}, 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.srv.UnseenCount("ann", "INBOX")).To(Equal(before))
	})
})

func uids(p imapc.Page) []uint32 {
	var out []uint32
	for _, i := range p.Items {
		out = append(out, i.UID)
	}
	return out
}

var _ = Describe("reading a message", func() {
	It("leaves the message unseen: it reads with PEEK and opens the folder read-only", func() {
		r := newRig(mailtest.ModeTLS)
		uid := r.srv.Deliver("ann", "INBOX", mailtest.Plain("p@x", "Hello", "the body"))
		m, err := r.open().Get(ctx(), "INBOX", uid)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Body).To(Equal("the body"))
		Expect(m.Unseen).To(BeTrue())
		Expect(r.srv.UnseenCount("ann", "INBOX")).To(Equal(1))
		log := joined(r)
		Expect(log).To(ContainSubstring("EXAMINE"))
		Expect(log).NotTo(MatchRegexp(`T\d+ SELECT`))
		Expect(log).To(ContainSubstring("BODY.PEEK["))
		Expect(log).NotTo(MatchRegexp(`BODY\[`))
		for _, verb := range []string{"STORE", "EXPUNGE", "DELETE", "COPY", "MOVE", "RENAME", "CREATE"} {
			Expect(log).NotTo(MatchRegexp(` (UID )?` + verb + ` `))
		}
	})

	It("prefers the text part, converts HTML-only mail without images, links or hidden text", func() {
		r := newRig(mailtest.ModeTLS)
		s := r.open()
		alt := r.srv.Deliver("ann", "INBOX", mailtest.Alternative("alt@x"))
		m, err := s.Get(ctx(), "INBOX", alt)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Body).To(Equal("plain version"))
		Expect(m.Converted).To(BeFalse())

		h := r.srv.Deliver("ann", "INBOX", mailtest.HTMLOnly("html@x"))
		m, err = s.Get(ctx(), "INBOX", h)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Converted).To(BeTrue())
		Expect(m.Body).To(ContainSubstring("Quarterly numbers attached."))
		for _, banned := range []string{"steal()", "tracker.example.net", "phish.example.net", "SYSTEM:", "<"} {
			Expect(m.Body).NotTo(ContainSubstring(banned))
		}
	})

	It("decodes quoted-printable UTF-8, base64 Latin-1 and encoded subjects", func() {
		r := newRig(mailtest.ModeTLS)
		s := r.open()
		q := r.srv.Deliver("ann", "INBOX", mailtest.QuotedPrintable("qp@x"))
		m, err := s.Get(ctx(), "INBOX", q)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Subject).To(Equal("Café menu"))
		Expect(m.Body).To(Equal("Un café crème, et une ligne très longue qui continue sur la ligne suivante."))
		b := r.srv.Deliver("ann", "INBOX", mailtest.Base64Latin1("b64@x"))
		m, err = s.Get(ctx(), "INBOX", b)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Body).To(Equal("café"))
	})

	It("lists nested attachments with name, type and size and never their bytes", func() {
		r := newRig(mailtest.ModeTLS)
		uid := r.srv.Deliver("ann", "INBOX", mailtest.WithAttachments("att@x"))
		m, err := r.open().Get(ctx(), "INBOX", uid)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Body).To(Equal("See the files."))
		Expect(m.Attachments).To(HaveLen(3))
		Expect(m.Attachments[0]).To(MatchFields(IgnoreExtras, Fields{"Name": Equal("notes.txt"), "Type": Equal("text/plain"), "Text": BeTrue(), "Part": Equal("2")}))
		Expect(m.Attachments[1]).To(MatchFields(IgnoreExtras, Fields{"Name": Equal("report.pdf"), "Type": Equal("application/pdf"), "Text": BeFalse()}))
		Expect(m.Attachments[2].Type).To(Equal("message/rfc822"))
		Expect(joined(r)).NotTo(MatchRegexp(`BODY\.PEEK\[[34]\]`))
	})

	It("strips hidden and control characters from hostile text", func() {
		r := newRig(mailtest.ModeTLS)
		uid := r.srv.Deliver("ann", "INBOX", mailtest.Hostile("h@x"))
		m, err := r.open().Get(ctx(), "INBOX", uid)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Body).NotTo(ContainSubstring("​"))
		Expect(m.Body).NotTo(ContainSubstring("‮"))
		Expect(m.Body).NotTo(ContainSubstring("\x07"))
	})

	It("reports an unknown UID", func() {
		r := newRig(mailtest.ModeTLS)
		_, err := r.open().Get(ctx(), "INBOX", 99)
		Expect(err).To(MatchError(imapc.ErrNoMessage))
	})

	It("reads a text attachment and gives only the details of a binary one", func() {
		r := newRig(mailtest.ModeTLS)
		uid := r.srv.Deliver("ann", "INBOX", mailtest.WithAttachments("att@x"))
		s := r.open()
		t, err := s.ReadPart(ctx(), "INBOX", uid, "2")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Text).To(Equal("line one of the notes\nline two"))
		b, err := s.ReadPart(ctx(), "INBOX", uid, "3")
		Expect(err).NotTo(HaveOccurred())
		Expect(b.Binary).To(BeTrue())
		Expect(b.Text).To(BeEmpty())
		Expect(b.Name).To(Equal("report.pdf"))
		_, err = s.ReadPart(ctx(), "INBOX", uid, "9")
		Expect(err).To(MatchError(imapc.ErrNoPart))
		_, err = s.ReadPart(ctx(), "INBOX", uid, "1.1")
		Expect(err).To(MatchError(imapc.ErrNoPart), "a body part is not an attachment")
		Expect(r.srv.UnseenCount("ann", "INBOX")).To(Equal(1))
	})

	It("finds the messages of a conversation in the folder", func() {
		r := newRig(mailtest.ModeTLS)
		r.srv.Deliver("ann", "INBOX", mailtest.Plain("root@x", "Plan", "first"))
		mid := r.srv.Deliver("ann", "INBOX", mailtest.Reply("r1@x", "root@x", "<root@x>", "Re: Plan", "second"))
		r.srv.Deliver("ann", "INBOX", mailtest.Reply("r2@x", "r1@x", "<root@x> <r1@x>", "Re: Plan", "third"))
		r.srv.Deliver("ann", "INBOX", mailtest.Plain("other@x", "Unrelated", "x"))
		th, err := r.open().Thread(ctx(), "INBOX", mid, 50)
		Expect(err).NotTo(HaveOccurred())
		var subjects []string
		for _, m := range th {
			subjects = append(subjects, m.Subject)
		}
		Expect(subjects).To(Equal([]string{"Plan", "Re: Plan", "Re: Plan"}))
	})
})

var _ = Describe("saving a draft", func() {
	It("appends with the Draft flag to the folder and can find it again by Message-ID", func() {
		r := newRig(mailtest.ModeTLS)
		r.srv.Mailbox("ann", "Drafts", `\Drafts`)
		s := r.open()
		raw := mailtest.Plain("d1@x", "Draft", "text")
		uid, err := s.AppendDraft(ctx(), "Drafts", []byte(raw))
		Expect(err).NotTo(HaveOccurred())
		Expect(uid).To(BeNumerically(">", 0))
		got := r.srv.Appended()
		Expect(got).To(HaveLen(1))
		Expect(got[0].Mailbox).To(Equal("Drafts"))
		Expect(got[0].Flags).To(ContainElement(`\Draft`))
		Expect(string(got[0].Raw)).To(Equal(raw))
		Expect(s.FindByMessageID(ctx(), "Drafts", "<d1@x>")).To(Equal(uid))
		Expect(s.FindByMessageID(ctx(), "Drafts", "nope@x")).To(BeZero())
	})
})
