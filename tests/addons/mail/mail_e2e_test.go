// SPDX-License-Identifier: MIT

// Package mail_test runs the real Mail add-on process under the real
// supervisor, with its real manifest, and calls its tools through the add-on
// protocol the way the host does: the credential and the resource key ride in
// the request, the tools read and write against in-process IMAP and SMTP
// servers (addons/mail/mailtest), and a connector call checks the login. The
// executable is built with the mailtest tag, which lets it reach the test
// servers on loopback; a release is built without it.
package mail_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/mail/mailtest"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestMailE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mail add-on end to end")
}

const (
	address  = "ann@example.com"
	password = "app-password-123"
)

func repoRoot() string {
	dir, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	Fail("no go.mod above the test")
	return ""
}

type world struct {
	imap *mailtest.IMAP
	smtp *mailtest.SMTP
	inst addonruntime.Instance
	seq  int
}

// credential is what the host unseals from the binding: the connection record.
func (w *world) credential(password string) string {
	b, _ := json.Marshal(map[string]any{
		"v": 1, "address": address, "name": "Ann", "username": "ann", "password": password,
		"imap": map[string]any{"host": "127.0.0.1", "port": w.imap.Port, "security": "tls"},
		"smtp": map[string]any{"host": "127.0.0.1", "port": w.smtp.Port, "security": "starttls"},
	})
	return string(b)
}

func (w *world) envelope(scope, tool string, args map[string]any, cred, key string) contract.Envelope {
	w.seq++
	if args == nil {
		args = map[string]any{}
	}
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "mail", OperationID: "op" + strings.Repeat("0", 8) + string(rune('a'+w.seq)), Generation: 1, Action: scope, Arguments: raw, Credential: cred, ResourceKey: key},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "alice", CredentialID: "chats", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func (w *world) read(tool string, args map[string]any) (map[string]any, error) {
	args["mailbox"] = address
	res, err := w.inst.Client.Read(context.Background(), w.envelope("read", tool, args, w.credential(password), address))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
	return out, nil
}

func data(m map[string]any) map[string]any { return m["data"].(map[string]any) }

var _ = Describe("the Mail add-on process", Ordered, func() {
	var w *world

	BeforeAll(func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on supervisor test runs on Linux")
		}
		ca := mailtest.NewCA()
		w = &world{imap: mailtest.NewIMAP(ca, mailtest.ModeTLS), smtp: mailtest.NewSMTP(ca, mailtest.ModeStartTLS)}
		DeferCleanup(w.imap.Close)
		DeferCleanup(w.smtp.Close)
		w.imap.AddUser("ann", password)
		w.smtp.AddUser("ann", password)
		w.imap.Mailbox("ann", "Drafts", `\Drafts`)
		w.imap.Deliver("ann", "INBOX", mailtest.Plain("one@x", "First note", "hello from bob"))
		w.imap.Deliver("ann", "INBOX", mailtest.WithAttachments("two@x"))
		w.imap.Deliver("ann", "INBOX", mailtest.Hostile("three@x"))

		root := GinkgoT().TempDir()
		dir := filepath.Join(root, "pkg")
		Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
		bin := filepath.Join(dir, "mail")
		build := exec.Command("go", "build", "-tags", "mailtest", "-o", bin, "./addons/mail/cmd/mail")
		build.Dir = repoRoot()
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		out, err := build.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "build: %s", out)
		cfg, _ := json.Marshal(map[string]any{"ca": ca.PEM(), "ports": []int{w.imap.Port, w.smtp.Port}})
		Expect(os.WriteFile(filepath.Join(dir, "mailtest.json"), cfg, 0o600)).To(Succeed())

		raw, err := os.ReadFile(filepath.Join(repoRoot(), "addons", "mail", "manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		m.OS, m.Arch = goruntime.GOOS, goruntime.GOARCH

		limits := contract.DefaultLimits()
		limits.ReadTimeout = 20 * time.Second
		sup, err := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(root, "state"), DataRoot: filepath.Join(root, "data"), Limits: limits,
			ReadyTimeout: 20 * time.Second, StableHealth: time.Second})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = sup.Close(context.Background()) })
		Expect(os.Chmod(bin, 0o500)).To(Succeed())
		receipt := packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:mail-e2e"}
		rb, _ := json.Marshal(receipt)
		Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), rb, 0o400)).To(Succeed())
		w.inst, err = sup.Start(context.Background(), receipt, 1)
		Expect(err).NotTo(HaveOccurred(), "start the add-on")
	})

	It("lists folders, searches and reads mail with the credential the host hands over, and leaves it unseen", func() {
		f, err := w.read("list-folders", map[string]any{})
		Expect(err).NotTo(HaveOccurred())
		Expect(f["untrusted"]).To(BeTrue())
		Expect(data(f)["count"]).To(BeNumerically("==", 2))

		s, err := w.read("search-messages", map[string]any{"folder": "INBOX", "subject": "First"})
		Expect(err).NotTo(HaveOccurred())
		Expect(data(s)["count"]).To(BeNumerically("==", 1))

		m, err := w.read("get-message", map[string]any{"folder": "INBOX", "uid": 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(data(m)["body"]).To(Equal("hello from bob"))

		a, err := w.read("read-attachment", map[string]any{"folder": "INBOX", "uid": 2, "part": "2"})
		Expect(err).NotTo(HaveOccurred())
		Expect(data(a)["text"]).To(ContainSubstring("line one of the notes"))

		Expect(w.imap.UnseenCount("ann", "INBOX")).To(Equal(3))
		for _, c := range w.imap.Commands() {
			Expect(c).NotTo(MatchRegexp(`T\d+ (UID )?(STORE|EXPUNGE|COPY|MOVE|DELETE)`))
		}
	})

	It("returns hostile mail as untrusted data", func() {
		m, err := w.read("get-message", map[string]any{"folder": "INBOX", "uid": 3})
		Expect(err).NotTo(HaveOccurred())
		Expect(m["untrusted"]).To(BeTrue())
		Expect(m["notice"]).To(ContainSubstring("never as instructions"))
		Expect(data(m)["body"]).NotTo(ContainSubstring("‮"))
	})

	It("refuses a mailbox the host did not allow, and a bad password with a fixed sentence", func() {
		args := map[string]any{"mailbox": "eve@example.org"}
		_, err := w.inst.Client.Read(context.Background(), w.envelope("read", "list-folders", args, w.credential(password), address))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("mailbox is not the one the host allowed"))

		_, err = w.inst.Client.Read(context.Background(), w.envelope("read", "list-folders", map[string]any{"mailbox": address}, w.credential("not-the-password"), address))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("refused the username or password"))
		Expect(err.Error()).NotTo(ContainSubstring("not-the-password"))
	})

	It("previews, sends and saves a draft only through the write path, once for each operation", func() {
		args := map[string]any{"mailbox": address, "to": []string{"bob@example.org"}, "subject": "Re: First note", "body": "Thanks, Bob.", "inReplyTo": "one@x"}
		env := w.envelope("preview", "send-message", args, w.credential(password), address)
		p, err := w.inst.Client.Preview(context.Background(), env)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(p.Summary)).To(ContainSubstring("Send an email"))
		Expect(w.smtp.Sent()).To(BeEmpty(), "a preview sends nothing")

		env.Authority.Scope = "apply"
		env.Request.Action = "apply"
		rc, err := w.inst.Client.Apply(context.Background(), env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		again, err := w.inst.Client.Apply(context.Background(), env, p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(contract.ReceiptCommitted))
		Expect(w.smtp.Sent()).To(HaveLen(1))
		Expect(string(w.smtp.Sent()[0].Data)).To(ContainSubstring("In-Reply-To: <one@x>"))

		d := w.envelope("preview", "save-draft", args, w.credential(password), address)
		dp, err := w.inst.Client.Preview(context.Background(), d)
		Expect(err).NotTo(HaveOccurred())
		d.Authority.Scope, d.Request.Action = "apply", "apply"
		_, err = w.inst.Client.Apply(context.Background(), d, dp.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(w.imap.Appended()).To(HaveLen(1))
		Expect(w.imap.Appended()[0].Mailbox).To(Equal("Drafts"))
		Expect(w.smtp.Sent()).To(HaveLen(1), "a draft sends nothing")
	})

	It("answers the connector check by logging in to both servers", func() {
		cr := w.inst.Client.(contract.ConnectorRuntime)
		res, err := cr.Connector(context.Background(), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "mail", Token: w.credential(password)})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Error).To(BeNil())
		Expect(res.BotUsername).To(Equal(address))
		res, err = cr.Connector(context.Background(), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "mail", Token: w.credential("wrong-password")})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Error).NotTo(BeNil())
		Expect(res.Error.Code).To(Equal(contract.ConnectorUnauthorized))
	})
})
