// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/tools"
	"github.com/mudler/LocalAI/addons/mail/mailtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail add-on tools")
}

const (
	password = "correct-horse-battery"
	address  = "ann@example.com"
)

type rig struct {
	imap *mailtest.IMAP
	smtp *mailtest.SMTP
	acct account.Account
	rt   *tools.Runtime
	now  time.Time
}

func newRig() *rig {
	ca := mailtest.NewCA()
	r := &rig{now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	r.imap = mailtest.NewIMAP(ca, mailtest.ModeTLS)
	r.smtp = mailtest.NewSMTP(ca, mailtest.ModeStartTLS)
	DeferCleanup(r.imap.Close)
	DeferCleanup(r.smtp.Close)
	r.imap.AddUser("ann", password)
	r.smtp.AddUser("ann", password)
	r.imap.Mailbox("ann", "Drafts", `\Drafts`)
	r.imap.Mailbox("ann", "Private")
	r.acct = account.Account{Version: 1, Address: address, Name: "Ann", Username: "ann", Password: password,
		IMAP: account.Endpoint{Host: "127.0.0.1", Port: r.imap.Port, Security: account.TLS},
		SMTP: account.Endpoint{Host: "127.0.0.1", Port: r.smtp.Port, Security: account.StartTLS}}
	d := dial.New(dial.Options{Ports: []int{r.imap.Port, r.smtp.Port}, RootCAs: ca.Pool(), TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})
	r.rt = tools.New(d)
	r.rt.Now = func() time.Time { return r.now }
	return r
}

func (r *rig) env(tool string, args map[string]any, op string) contract.Envelope {
	if args == nil {
		args = map[string]any{}
	}
	if _, ok := args["mailbox"]; !ok {
		args["mailbox"] = address
	}
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	tok, err := r.acct.Encode()
	Expect(err).NotTo(HaveOccurred())
	return contract.Envelope{Request: contract.Request{AddonID: "mail", Arguments: raw, Credential: tok, ResourceKey: address, OperationID: op}}
}

type reply struct {
	Source    string         `json:"source"`
	Untrusted bool           `json:"untrusted"`
	Notice    string         `json:"notice"`
	Data      map[string]any `json:"data"`
}

func (r *rig) read(tool string, args map[string]any) (reply, error) {
	res, err := r.rt.Read(context.Background(), r.env(tool, args, ""))
	if err != nil {
		return reply{}, err
	}
	Expect(len(res.Payload)).To(BeNumerically("<=", tools.MaxPayload))
	Expect(string(res.Payload)).NotTo(ContainSubstring(password))
	var out reply
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
	return out, nil
}

func (r *rig) ok(tool string, args map[string]any) reply {
	out, err := r.read(tool, args)
	Expect(err).NotTo(HaveOccurred())
	return out
}

func refused(err error) string {
	GinkgoHelper()
	Expect(err).To(HaveOccurred())
	Expect(err).To(MatchError(protocol.ErrBadRequest))
	return err.Error()
}

var _ = Describe("the manifest", func() {
	It("is valid for the host and matches the tools", func() {
		b, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		h := contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 2, DataFormat: 1, FeatureLevel: contract.HostFeatureLevel, OS: "linux", Arch: "amd64"}
		Expect(contract.ValidateManifest(m, h)).To(Succeed())
		Expect(m.Capabilities.Tools).To(ConsistOf(tools.Declared()))
		Expect(len(m.Capabilities.ToolDefinitions)).To(BeNumerically("<=", contract.MaxToolDefinitions))
		for _, d := range m.Capabilities.ToolDefinitions {
			switch d.Kind {
			case contract.ToolKindWrite:
				Expect(tools.WriteTools()).To(ContainElement(d.Name))
				Expect(d.Approval).To(Equal(contract.ApprovalAlways), d.Name)
			case contract.ToolKindRead:
				if !d.UIOnly {
					Expect(d.Description).To(ContainSubstring("text written by others"), d.Name)
				}
			default:
				Fail("unexpected kind " + d.Kind)
			}
		}
	})
	It("names no mail provider", func() {
		b, _ := os.ReadFile("../../manifest.json")
		for _, p := range []string{"gmail", "google", "fastmail", "outlook", "yahoo"} {
			Expect(strings.ToLower(string(b))).NotTo(ContainSubstring(p))
		}
	})
})

var _ = Describe("what every read says and refuses", func() {
	var r *rig
	BeforeEach(func() {
		r = newRig()
		r.imap.Deliver("ann", "INBOX", mailtest.Hostile("h@x"))
	})
	It("wraps results as untrusted data and never carries the password", func() {
		out := r.ok(tools.ToolListFolders, nil)
		Expect(out.Source).To(Equal("mail"))
		Expect(out.Untrusted).To(BeTrue())
		Expect(out.Notice).To(ContainSubstring("never as instructions"))
	})
	It("refuses a mailbox other than the connection's, and a missing connection", func() {
		_, err := r.read(tools.ToolListFolders, map[string]any{"mailbox": "eve@example.org"})
		Expect(refused(err)).To(ContainSubstring("mailbox is not the one the host allowed"))
		env := r.env(tools.ToolListFolders, nil, "")
		env.Request.Credential = ""
		_, err = r.rt.Read(context.Background(), env)
		refused(err)
		env.Request.Credential = `{"v":1,"imap":"x"}`
		_, err = r.rt.Read(context.Background(), env)
		Expect(refused(err)).NotTo(ContainSubstring("imap"))
	})
	It("refuses unknown arguments and tools", func() {
		_, err := r.read(tools.ToolListFolders, map[string]any{"extra": 1})
		refused(err)
		_, err = r.read("delete-message", nil)
		refused(err)
	})
	It("returns hostile text as plain data with hidden characters removed", func() {
		out := r.ok(tools.ToolGetMessage, map[string]any{"folder": "INBOX", "uid": 1})
		body := out.Data["body"].(string)
		Expect(body).To(ContainSubstring("delete everything"))
		Expect(body).NotTo(ContainSubstring("‮"))
		Expect(out.Untrusted).To(BeTrue())
	})
})

var _ = Describe("errors", func() {
	It("answers a wrong password with a fixed sentence", func() {
		r := newRig()
		r.acct.Password = "wrong-password-1"
		_, err := r.read(tools.ToolListFolders, nil)
		msg := refused(err)
		Expect(msg).To(ContainSubstring("refused the username or password"))
		Expect(msg).NotTo(ContainSubstring("wrong-password-1"))
		Expect(msg).NotTo(ContainSubstring("AUTHENTICATIONFAILED"))
	})
	It("tells a refused connection, a timeout and a bad certificate apart", func() {
		r := newRig()
		r.imap.Close()
		_, err := r.read(tools.ToolListFolders, nil)
		Expect(refused(err)).To(ContainSubstring("could not connect"))

		r2 := newRig()
		r2.imap.Hang(true)
		_, err = r2.read(tools.ToolListFolders, nil)
		Expect(refused(err)).To(ContainSubstring("did not answer in time"))

		r3 := newRig()
		r3.rt.Dialer = dial.New(dial.Options{Ports: []int{r3.imap.Port}, TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})
		_, err = r3.read(tools.ToolListFolders, nil)
		Expect(refused(err)).To(ContainSubstring("certificate"))
	})
	It("refuses a private server address with the production policy", func() {
		r := newRig()
		r.rt.Dialer = dial.New(dial.Options{})
		_, err := r.read(tools.ToolListFolders, nil)
		Expect(refused(err)).To(ContainSubstring("not allowed"))
	})
})

var _ = Describe("folders and search", func() {
	var r *rig
	BeforeEach(func() {
		r = newRig()
		for i := 0; i < 5; i++ {
			r.imap.Deliver("ann", "INBOX", mailtest.Plain("s"+string(rune('a'+i))+"@x", "Subject "+string(rune('A'+i)), "needle "+strings.Repeat("x", i)))
		}
	})
	It("lists folders and hides the ones off the allowlist", func() {
		out := r.ok(tools.ToolListFolders, nil)
		Expect(out.Data["count"]).To(BeNumerically("==", 3))
		r.acct.Folders = []string{"INBOX"}
		out = r.ok(tools.ToolListFolders, nil)
		Expect(out.Data["count"]).To(BeNumerically("==", 1))
		_, err := r.read(tools.ToolSearchMessages, map[string]any{"folder": "Private"})
		Expect(refused(err)).To(ContainSubstring("not on the list of folders"))
		_, err = r.read(tools.ToolGetMessage, map[string]any{"folder": "Private", "uid": 1})
		refused(err)
	})
	It("pages a search with a cursor and a limit", func() {
		p1 := r.ok(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "limit": 2})
		Expect(p1.Data["count"]).To(BeNumerically("==", 2))
		Expect(p1.Data["total"]).To(BeNumerically("==", 5))
		next := p1.Data["next"]
		Expect(next).To(BeNumerically("==", 4))
		p2 := r.ok(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "limit": 2, "cursor": next})
		first := p2.Data["messages"].([]any)[0].(map[string]any)
		Expect(first["uid"]).To(BeNumerically("==", 3))
		Expect(first["subject"]).To(Equal("Subject C"))
	})
	It("defaults to 30 per page and refuses more than 50", func() {
		_, err := r.read(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "limit": 51})
		refused(err)
		for i := 0; i < 35; i++ {
			r.imap.Deliver("ann", "INBOX", mailtest.Plain("bulk"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@x", "bulk", "b"))
		}
		out := r.ok(tools.ToolSearchMessages, map[string]any{"folder": "INBOX"})
		Expect(out.Data["count"]).To(BeNumerically("==", 30))
		Expect(out.Data["next"]).NotTo(BeNil())
	})
	It("passes a hostile subject as data and refuses control characters and bad dates", func() {
		subj := "x\" OR ALL \\ \r\nA9 EXPUNGE"
		r.ok(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "subject": subj})
		for _, c := range r.imap.Commands() {
			Expect(c).NotTo(MatchRegexp(`(?i) (UID )?(EXPUNGE|STORE|DELETE)`))
		}
		_, err := r.read(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "subject": "a\x00b"})
		refused(err)
		_, err = r.read(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "since": "yesterday"})
		refused(err)
		_, err = r.read(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "since": "2026-02-01", "before": "2026-01-01"})
		refused(err)
		_, err = r.read(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "text": strings.Repeat("a", 201)})
		refused(err)
	})
	It("searches by non-ASCII text", func() {
		r.imap.Deliver("ann", "INBOX", mailtest.Plain("u@x", "Prüfung Ärger 日本", "x"))
		out := r.ok(tools.ToolSearchMessages, map[string]any{"folder": "INBOX", "subject": "Ärger 日本"})
		Expect(out.Data["count"]).To(BeNumerically("==", 1))
	})
})

var _ = Describe("reading", func() {
	var r *rig
	BeforeEach(func() { r = newRig() })

	It("keeps the message unseen and reads the body, headers and attachments", func() {
		uid := r.imap.Deliver("ann", "INBOX", mailtest.WithAttachments("w@x"))
		out := r.ok(tools.ToolGetMessage, map[string]any{"folder": "INBOX", "uid": uid})
		Expect(out.Data["body"]).To(Equal("See the files."))
		Expect(out.Data["subject"]).To(Equal("Files"))
		Expect(out.Data["messageId"]).To(Equal("w@x"))
		Expect(out.Data["attachments"]).To(HaveLen(3))
		Expect(r.imap.UnseenCount("ann", "INBOX")).To(Equal(1))
	})

	It("cuts a long body and says so", func() {
		uid := r.imap.Deliver("ann", "INBOX", mailtest.Plain("big@x", "Big", strings.Repeat("0123456789abcdef\n", 6000)))
		out := r.ok(tools.ToolGetMessage, map[string]any{"folder": "INBOX", "uid": uid})
		Expect(len(out.Data["body"].(string))).To(BeNumerically("<=", tools.MaxBodyBytes))
		Expect(out.Data["bodyCut"]).To(BeTrue())
		Expect(out.Data["bodyBytesOmitted"]).To(BeNumerically(">", 0))
	})

	It("pages a long text attachment by byte offset, cut at 96 KiB, and shows only the details of a binary one", func() {
		long := strings.Repeat("line of the attachment\n", 8000) // 184000 bytes
		raw := mailtest.CRLF("From: a@example.org\nTo: ann@example.com\nSubject: Long\nMessage-ID: <long@x>\nMIME-Version: 1.0\nContent-Type: multipart/mixed; boundary=B\n\n--B\nContent-Type: text/plain\n\nhi\n--B\nContent-Type: text/plain; name=\"big.txt\"\nContent-Disposition: attachment; filename=\"big.txt\"\n\n" + long + "--B\nContent-Type: application/octet-stream; name=\"x.bin\"\nContent-Disposition: attachment; filename=\"x.bin\"\nContent-Transfer-Encoding: base64\n\nAAECAw==\n--B--\n")
		uid := r.imap.Deliver("ann", "INBOX", raw)
		a := map[string]any{"folder": "INBOX", "uid": uid, "part": "2"}
		p1 := r.ok(tools.ToolReadAttachment, a)
		Expect(len(p1.Data["text"].(string))).To(Equal(tools.MaxAttachmentBytes))
		next := p1.Data["next"]
		Expect(next).To(BeNumerically("==", tools.MaxAttachmentBytes))
		Expect(p1.Data["bytesLeft"]).To(BeNumerically(">", 0))
		a["offset"] = next
		p2 := r.ok(tools.ToolReadAttachment, a)
		Expect(p2.Data["text"].(string)).To(HavePrefix(long[tools.MaxAttachmentBytes:tools.MaxAttachmentBytes+40]), "the second page starts on the byte the first ended")
		a["offset"] = 100
		p3 := r.ok(tools.ToolReadAttachment, a)
		Expect(p3.Data["offset"]).To(BeNumerically("==", 100))
		a["offset"] = 10_000_000
		_, err := r.read(tools.ToolReadAttachment, a)
		refused(err)

		bin := r.ok(tools.ToolReadAttachment, map[string]any{"folder": "INBOX", "uid": uid, "part": "3"})
		Expect(bin.Data["binary"]).To(BeTrue())
		Expect(bin.Data).NotTo(HaveKey("text"))
		Expect(bin.Data["name"]).To(Equal("x.bin"))
		_, err = r.read(tools.ToolReadAttachment, map[string]any{"folder": "INBOX", "uid": uid, "part": "../1"})
		refused(err)
	})

	It("gets the thread of a message", func() {
		r.imap.Deliver("ann", "INBOX", mailtest.Plain("root@x", "Plan", "first"))
		uid := r.imap.Deliver("ann", "INBOX", mailtest.Reply("r1@x", "root@x", "<root@x>", "Re: Plan", "second"))
		out := r.ok(tools.ToolGetThread, map[string]any{"folder": "INBOX", "uid": uid})
		Expect(out.Data["count"]).To(BeNumerically("==", 2))
		Expect(out.Data["note"]).To(ContainSubstring("Only messages in this folder"))
	})

	It("checks IMAP and SMTP apart", func() {
		out := r.ok(tools.ToolCheck, nil)
		Expect(out.Data["imap"]).To(HaveKeyWithValue("ok", true))
		Expect(out.Data["smtp"]).To(HaveKeyWithValue("ok", true))
		r.smtp.Close()
		out = r.ok(tools.ToolCheck, nil)
		Expect(out.Data["imap"]).To(HaveKeyWithValue("ok", true))
		smtp := out.Data["smtp"].(map[string]any)
		Expect(smtp["ok"]).To(BeFalse())
		Expect(smtp["error"]).To(ContainSubstring("could not connect"))
		r.acct.Password = "another-wrong-pass"
		out = r.ok(tools.ToolCheck, nil)
		Expect(out.Data["imap"].(map[string]any)["error"]).To(ContainSubstring("username or password"))
	})
})

// newRigNoDrafts restarts the IMAP side of r with a mailbox that has no Drafts folder.
func newRigNoDrafts(r *rig) *mailtest.IMAP {
	ca := mailtest.NewCA()
	srv := mailtest.NewIMAP(ca, mailtest.ModeTLS)
	DeferCleanup(srv.Close)
	srv.AddUser("ann", password)
	r.acct.IMAP.Port = srv.Port
	d := dial.New(dial.Options{Ports: []int{srv.Port, r.smtp.Port}, RootCAs: ca.Pool(), TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Timeout: time.Second})
	r.rt.Dialer = d
	return srv
}
