// SPDX-License-Identifier: MIT
//go:build auth

package oauth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/gmail/gmailtest"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	mailRead    = "https://www.googleapis.com/auth/gmail.readonly"
	mailCompose = "https://www.googleapis.com/auth/gmail.compose"
	mailAddress = "person@example.test"
)

// gmailService is the shipped Gmail add-on as a service of the suite: its own
// manifest, its own executable, and a fake login service under the addresses
// of its descriptor. The host has no code for it; it sees only the descriptor.
var gmailService = service{
	AddonID: "gmail", Kind: "gmail", Title: "Gmail",
	Hosts: []string{"accounts.google.com", "oauth2.googleapis.com"}, AuthorizeHost: "accounts.google.com", TokenHost: "oauth2.googleapis.com",
	AuthorizePath:  "/o/oauth2/v2/auth",
	MovedTokenHost: "oauth2.googleapis.com",
	ClientAuth:     contract.OAuthClientAuthPost, ClientID: "mail-client.apps.example.test", ClientSecret: "mail-secret-sentinel",
	Required: []string{mailRead}, Optional: []string{mailCompose},
	Params:     []contract.OAuthParam{{Name: "access_type", Value: "offline"}, {Name: "include_granted_scopes", Value: "true"}},
	Manifest:   gmailManifest,
	Executable: func() (string, []byte) { return "gmail", gmailExecutable },
}

// gmailManifest is the manifest the add-on ships, for the platform of the
// test and with its page and cards left out: the suite installs the
// executable, and the interface has its own tests.
func gmailManifest(version string, _ bool) []byte {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "addons", "gmail", "manifest.json")) // #nosec G304 -- fixed path in the repository
	Expect(err).NotTo(HaveOccurred())
	var m map[string]any
	Expect(json.Unmarshal(raw, &m)).To(Succeed())
	m["version"], m["os"], m["arch"] = version, goruntime.GOOS, goruntime.GOARCH
	caps := m["capabilities"].(map[string]any)
	delete(caps, "ui")
	for _, k := range caps["connector"].(map[string]any)["kinds"].([]any) {
		delete(k.(map[string]any), "page")
	}
	b, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	return b
}

func mailWrites() []gmailtest.Call {
	var out []gmailtest.Call
	for _, c := range mailbox.Calls() {
		if c.Method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

var _ = Describe("the shipped Gmail add-on, the same host", func() {
	var (
		c *cluster
		u *person
	)
	BeforeEach(func() {
		mailbox.Reset()
		c = newClusterFor([]service{gmailService})
		u = c.person(gmailService, c.owner)
		// The fake Gmail accepts the access tokens of the fake login service
		// and no others, with the scopes the login holds.
		mailbox.SetAuthenticator(func(tok string) (string, []string, bool) {
			if !strings.Contains(tok, "-access-") || !slices.Contains(u.p.Issued(), tok) || slices.Contains(revokedAccess(u), tok) {
				return "", nil, false
			}
			return mailAddress, []string{mailRead, mailCompose}, true
		})
		mailbox.AddLabel("Label_1", "Work")
		mailbox.AddLabel("Label_2", "Private")
		mail := func(id, subject, body string, labels ...string) gmailtest.Mail {
			return gmailtest.Mail{ID: id, From: "Alice <alice@example.test>", To: mailAddress, Subject: subject, Body: body, Labels: labels}
		}
		mailbox.AddMessage(gmailtest.Plain(mail("hello", "Hello from the mailbox", "mail body text", "INBOX", "Label_1")))
		mailbox.AddMessage(gmailtest.Plain(mail("secret", "Private matter", "outside the label", "INBOX", "Label_2")))
		mailbox.AddMessage(gmailtest.WithAttachments(mail("files", "Files", "see attached", "INBOX", "Label_1"), "<p>see attached</p>",
			gmailtest.Attachment("big.txt", "text/plain", "att-big", bytes.Repeat([]byte("0123456789abcdef\n"), 12<<10))))
		u.approve()
		u.connection()
		u.setFolders([]string{"Work"}, "propose")
	})

	It("connects, reads inside the label, writes only with approval, renews, and revokes", func() {
		Expect(u.oauth(c.owner)["state"]).To(Equal("not_connected"))

		By("starting a login with the descriptor of the add-on")
		authorize, nonce := u.start(c.owner, []string{mailCompose})
		q := authorize.Query()
		Expect(authorize.Host).To(Equal("accounts.google.com"))
		Expect(strings.Fields(q.Get("scope"))).To(ConsistOf(mailRead, mailCompose))
		Expect(q.Get("access_type")).To(Equal("offline"))
		Expect(q.Get("code_challenge_method")).To(Equal("S256"))
		page := u.arrive(c.owner, u.visit(authorize), nonce)
		Expect(page.Query().Get("oauth")).To(Equal("ok"), page.String())
		o := u.oauth(c.owner)
		Expect(o["state"]).To(Equal("connected"))
		Expect(o["account_label"]).To(Equal(mailAddress), "the add-on named the account from the access token")

		By("reading with the access token and nothing else")
		a := u.tool(c.owner, "search-messages", map[string]any{"label": "Work", "text": "mailbox"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(a.payload["items"]).To(HaveLen(1))
		for _, call := range mailbox.Calls() {
			Expect(call.Token).To(Equal(u.p.LastAccess()), "Gmail got the access token of the login")
		}
		a = u.tool(c.owner, "get-message", map[string]any{"label": "Work", "messageId": "hello"})
		Expect(a.payload["body"].(map[string]any)["text"]).To(Equal("mail body text"))

		By("cutting an attachment that is longer than a result, and reading the rest by offset")
		a = u.tool(c.owner, "read-attachment", map[string]any{"label": "Work", "messageId": "files", "attachmentId": "att-big"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(a.payload["truncated"]).To(BeTrue())
		Expect(a.payload["totalBytes"]).To(BeNumerically("==", 17*(12<<10)))
		next := a.payload["nextOffset"].(float64)
		a = u.tool(c.owner, "read-attachment", map[string]any{"label": "Work", "messageId": "files", "attachmentId": "att-big", "offset": next})
		Expect(a.payload["offset"]).To(BeNumerically("==", next))
		Expect(a.payload["content"]).NotTo(BeEmpty())

		By("refusing a label that is not on the list before the add-on runs, and a message outside the label in the add-on")
		before := len(mailbox.Calls())
		a = u.tool(c.owner, "search-messages", map[string]any{"label": "Private"})
		Expect(a.result.Kind).To(Equal(tools.ResultError))
		Expect(a.result.Code).To(Equal(tools.CodeResourceNotAllowed))
		Expect(mailbox.Calls()[before:]).To(BeEmpty())
		a = u.tool(c.owner, "get-message", map[string]any{"label": "Work", "messageId": "secret"})
		Expect(a.result.Kind).To(Equal(tools.ResultError), a.text)
		Expect(a.result.Code).To(Equal(tools.CodeInvalidRequest))
		Expect(a.text).NotTo(ContainSubstring("Private matter"))

		By("storing a draft as a proposal that changes nothing until the person confirms")
		a = u.tool(c.owner, "create-draft", map[string]any{"label": "Work", "to": []string{"bob@example.test"}, "subject": "Hi Bob", "body": "see you at noon"})
		Expect(a.text).To(ContainSubstring("proposed a change"))
		Expect(a.text).To(ContainSubstring("Nothing has been changed"))
		Expect(mailWrites()).To(BeEmpty())
		Expect(mailbox.Drafts()).To(BeEmpty())
		id, digest := u.proposed(c.owner, a.text)
		rec := u.confirm(c.owner, id, digest)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		drafts := mailbox.Drafts()
		Expect(drafts).To(HaveLen(1))
		Expect(drafts[0].Raw).To(ContainSubstring("To: bob@example.test"))
		Expect(mailbox.Sent()).To(BeEmpty(), "saving a draft sends nothing")

		By("sending the draft only after a second approval")
		a = u.tool(c.owner, "send-draft", map[string]any{"label": "Work", "draftId": drafts[0].ID})
		Expect(a.text).To(ContainSubstring("proposed a change"))
		Expect(mailbox.Sent()).To(BeEmpty())
		id, digest = u.proposed(c.owner, a.text)
		rec = u.confirm(c.owner, id, digest)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(mailbox.Sent()).To(HaveLen(1))
		for _, call := range mailWrites() {
			Expect(call.Token).To(Equal(u.p.LastAccess()))
		}

		By("renewing the token once the clock passes its end")
		tokenCalls := len(u.tokenCalls(0))
		c.clock.Advance(2 * time.Hour)
		a = u.tool(c.owner, "get-message", map[string]any{"label": "Work", "messageId": "hello"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(u.tokenCalls(0)).To(HaveLen(tokenCalls+1), "one refresh")
		Expect(u.tokenCalls(0)[tokenCalls].Form.Get("grant_type")).To(Equal("refresh_token"))
		Expect(mailbox.Calls()[len(mailbox.Calls())-1].Token).To(Equal(u.p.LastAccess()), "the new token reached Gmail")

		By("renewing once when Gmail rejects the token, and retrying the call")
		rejected := u.p.LastAccess()
		revoke(u, rejected)
		tokenCalls = len(u.tokenCalls(0))
		a = u.tool(c.owner, "get-message", map[string]any{"label": "Work", "messageId": "hello"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(u.tokenCalls(0)).To(HaveLen(tokenCalls + 1))
		Expect(u.p.LastAccess()).NotTo(Equal(rejected))

		By("telling the model to ask for a reconnect when the renewed token is rejected too")
		revokeAll(u)
		a = u.tool(c.owner, "get-message", map[string]any{"label": "Work", "messageId": "hello"})
		Expect(a.result.Code).To(Equal(tools.CodeReconnectNeeded), a.text)
		Expect(u.oauth(c.owner)["state"]).To(Equal("reconnect_needed"))
		clearRevoked(u)
		u.connect(nil)
		Expect(u.oauth(c.owner)["scopes"]).To(ConsistOf(mailRead), "the second login asked for the required scope only")

		By("degrading the write tools when only the read scope was granted")
		before = len(mailbox.Calls())
		a = u.tool(c.owner, "create-draft", map[string]any{"label": "Work", "to": []string{"bob@example.test"}, "subject": "Nope", "body": "x"})
		Expect(a.result.Kind).To(Equal(tools.ResultError), a.text)
		Expect(a.text).To(ContainSubstring("gmail.compose"), "the refusal names the missing scope")
		Expect(a.text).NotTo(ContainSubstring("403"))
		for _, call := range mailbox.Calls()[before:] {
			Expect(call.Method).To(Equal(http.MethodGet), "no write was tried: %s %s", call.Method, call.Path)
		}
		Expect(mailbox.Drafts()).To(BeEmpty())
		u.expectNoSecret(a.text)

		By("disconnecting, which revokes at the service")
		before = len(u.p.Calls())
		rec = c.owner.do("alice", http.MethodPost, "/api/chats/connectors/"+u.binding+"/oauth/disconnect", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decode(rec)).To(Equal(map[string]any{"revoked": true}))
		Expect(u.p.Calls()[before:]).To(HaveLen(1))
		Expect(u.p.Calls()[before].Endpoint).To(Equal("revoke"))
		a = u.tool(c.owner, "search-messages", map[string]any{"label": "Work"})
		Expect(a.result.Code).To(Equal(tools.CodeNoCredential))

		By("having kept every secret out of what the model, the person and the logs saw")
		u.expectClientAuth()
		for _, t := range append(u.texts, c.logText()) {
			u.expectNoSecret(t)
		}
		// The model reads mail by design; a log must not hold any of it.
		log := c.logText()
		Expect(log).NotTo(ContainSubstring("mail body text"), "no mail content in a log")
		Expect(log).NotTo(ContainSubstring("bob@example.test"), "no address in a log")
		Expect(log).NotTo(ContainSubstring("Hello from the mailbox"), "no subject in a log")
	})

	It("tells the model how long to wait when Gmail limits the calls", func() {
		u.connect([]string{mailCompose})
		mailbox.Inject("/gmail/v1/users/me/messages/hello", http.StatusTooManyRequests, "rateLimitExceeded", "9", 1)
		a := u.tool(c.owner, "get-message", map[string]any{"label": "Work", "messageId": "hello"})
		Expect(a.result.Kind).To(Equal(tools.ResultError), a.text)
		Expect(a.result.Code).To(Equal(tools.CodeRateLimited))
		Expect(a.result.RetryAfter).To(Equal(9))
		Expect(u.oauth(c.owner)["state"]).To(Equal("connected"), "a limit does not end the login")
		Expect(u.tool(c.owner, "get-message", map[string]any{"label": "Work", "messageId": "hello"}).result.Kind).To(Equal(tools.ResultData))
	})
})
