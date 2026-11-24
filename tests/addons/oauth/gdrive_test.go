// SPDX-License-Identifier: MIT
//go:build auth

package oauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/gdrive/gdrivetest"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	driveRead  = "https://www.googleapis.com/auth/drive.readonly"
	driveWrite = "https://www.googleapis.com/auth/drive.file"
	driveEmail = "person@example.test"
)

// driveService is the shipped Google Drive add-on as a service of the suite:
// its own manifest, its own executable, and a fake login service under the
// addresses of its descriptor. The host has no code for it; it sees only the
// descriptor.
var driveService = service{
	AddonID: "gdrive", Kind: "gdrive", Title: "Google Drive",
	Hosts: []string{"accounts.google.com", "oauth2.googleapis.com"}, AuthorizeHost: "accounts.google.com", TokenHost: "oauth2.googleapis.com",
	AuthorizePath:  "/o/oauth2/v2/auth",
	MovedTokenHost: "oauth2.googleapis.com",
	ClientAuth:     contract.OAuthClientAuthPost, ClientID: "drive-client.apps.example.test", ClientSecret: "drive-secret-sentinel",
	Required: []string{driveRead}, Optional: []string{driveWrite},
	Params:     []contract.OAuthParam{{Name: "access_type", Value: "offline"}, {Name: "include_granted_scopes", Value: "true"}},
	Manifest:   driveManifest,
	Executable: func() (string, []byte) { return "gdrive", driveExecutable },
}

// driveManifest is the manifest the add-on ships, for the platform of the
// test and with its page and cards left out: the suite installs the
// executable, and the interface has its own tests.
func driveManifest(version string, _ bool) []byte {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "addons", "gdrive", "manifest.json")) // #nosec G304 -- fixed path in the repository
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

// tool calls one tool of the add-on in the person's chat, as a model would.
func (u *person) tool(n *node, name string, args map[string]any) answer {
	raw, _ := json.Marshal(args)
	text, err := n.app.StudioAddonTools().Call(context.Background(), "alice", u.chat, "addon__"+u.s.AddonID+"__"+name, raw)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	u.mu.Lock()
	u.keep(text)
	u.mu.Unlock()
	a := answer{text: text}
	if strings.HasPrefix(text, "The add-on ") {
		// A proposal is told to the model in words.
		return a
	}
	ExpectWithOffset(1, json.Unmarshal([]byte(text), &a.result)).To(Succeed(), text)
	if len(a.result.Result) > 0 {
		var wrapper struct {
			Untrusted bool           `json:"untrusted"`
			Data      map[string]any `json:"data"`
		}
		ExpectWithOffset(1, json.Unmarshal(a.result.Result, &wrapper)).To(Succeed(), text)
		a.payload = wrapper.Data
		ExpectWithOffset(1, wrapper.Untrusted).To(BeTrue(), "every result of this add-on says it is data")
	}
	return a
}

// setFolders gives the binding the folders it may use and a write mode.
func (u *person) setFolders(folders []string, mode string) {
	rec := u.n.do("alice", http.MethodPut, "/api/chats/connectors/"+u.binding, map[string]any{"kind": u.s.Kind, "name": "Work", "enabled": false,
		"accept_any": false, "resources": folders, "write_mode": mode})
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
}

var proposalIn = regexp.MustCompile(`proposal ([A-Za-z0-9_-]+)\)`)

// proposed reads the ID of the proposal that the model was told about and the
// digest of what the person is shown, from the approvals of the person.
func (u *person) proposed(n *node, text string) (id, digest string) {
	m := proposalIn.FindStringSubmatch(text)
	ExpectWithOffset(1, m).NotTo(BeNil(), text)
	rec := n.do("alice", http.MethodGet, "/api/addons/proposals/"+m[1], nil)
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	return m[1], decode(rec)["digest"].(string)
}

// confirm confirms a proposal as the person's own browser session does.
func (u *person) confirm(n *node, id, digest string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"digest": digest})
	req := httptest.NewRequest(http.MethodPost, "/api/addons/proposals/"+id+"/confirm", bytes.NewReader(body))
	req.Host = "localai.example.test"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "same-origin")
	req.Header.Set("X-LocalAI-Confirm", id)
	req.AddCookie(&http.Cookie{Name: "session", Value: n.sessions["alice"]})
	rec := httptest.NewRecorder()
	n.e.ServeHTTP(rec, req)
	return rec
}

// driveCalls returns the calls of the fake Drive that are not reads of the
// tree for the check of a folder.
func driveWrites() []gdrivetest.Call {
	var out []gdrivetest.Call
	for _, c := range drive.Calls() {
		if c.Method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

var _ = Describe("the shipped Google Drive add-on, the same host", func() {
	var (
		c *cluster
		u *person
	)
	BeforeEach(func() {
		drive.Reset()
		c = newClusterFor([]service{driveService})
		u = c.person(driveService, c.owner)
		// The fake Drive accepts the access tokens of the fake login service
		// and no others, with the scopes the login holds.
		drive.SetAuthenticator(func(tok string) (string, []string, bool) {
			if !strings.Contains(tok, "-access-") || !slices.Contains(u.p.Issued(), tok) || slices.Contains(revokedAccess(u), tok) {
				return "", nil, false
			}
			return driveEmail, []string{driveRead, driveWrite}, true
		})
		drive.AddFolder("proj", "Projects", "")
		drive.AddFolder("other", "Other", "")
		drive.AddFile(gdrivetest.File{ID: "notes", Name: "notes.txt", Parents: []string{"proj"}, Content: []byte("hello from drive")})
		drive.AddFile(gdrivetest.File{ID: "plan", Name: "Plan", MimeType: gdrivetest.DocumentType, Parents: []string{"proj"}, Content: []byte("# Plan\nbody")})
		drive.AddFile(gdrivetest.File{ID: "secret", Name: "secret.txt", Parents: []string{"other"}, Content: []byte("outside")})
		drive.AddFile(gdrivetest.File{ID: "big", Name: "big.txt", Parents: []string{"proj"}, Content: bytes.Repeat([]byte("0123456789abcdef\n"), 12<<10)})
		u.approve()
		u.connection()
		u.setFolders([]string{"proj"}, "propose")
	})

	It("connects, reads inside the folder, writes only with approval, renews, and revokes", func() {
		Expect(u.oauth(c.owner)["state"]).To(Equal("not_connected"))
		By("starting a login with the descriptor of the add-on")
		authorize, nonce := u.start(c.owner, []string{driveWrite})
		q := authorize.Query()
		Expect(authorize.Host).To(Equal("accounts.google.com"))
		Expect(authorize.Path).To(Equal("/o/oauth2/v2/auth"))
		Expect(strings.Fields(q.Get("scope"))).To(ConsistOf(driveRead, driveWrite))
		Expect(q.Get("access_type")).To(Equal("offline"))
		Expect(q.Get("include_granted_scopes")).To(Equal("true"))
		Expect(q.Get("code_challenge_method")).To(Equal("S256"))
		page := u.arrive(c.owner, u.visit(authorize), nonce)
		Expect(page.Query().Get("oauth")).To(Equal("ok"), page.String())
		o := u.oauth(c.owner)
		Expect(o["state"]).To(Equal("connected"))
		Expect(o["account_label"]).To(Equal(driveEmail), "the add-on named the account from the access token")

		By("reading with the access token and nothing else")
		a := u.tool(c.owner, "search", map[string]any{"folder": "proj", "name": "notes"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(a.payload["items"]).To(HaveLen(1))
		for _, call := range drive.Calls() {
			Expect(call.Token).To(Equal(u.p.LastAccess()), "Drive got the access token of the login")
		}
		a = u.tool(c.owner, "read-file", map[string]any{"folder": "proj", "fileId": "plan"})
		Expect(a.payload["content"]).To(Equal("# Plan\nbody"))

		By("cutting a file that is longer than a result, and reading the rest by offset")
		a = u.tool(c.owner, "read-file", map[string]any{"folder": "proj", "fileId": "big"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(a.payload["truncated"]).To(BeTrue())
		Expect(a.payload["totalBytes"]).To(BeNumerically("==", 17*(12<<10)))
		next := a.payload["nextOffset"].(float64)
		Expect(next).To(BeNumerically(">", 0))
		a = u.tool(c.owner, "read-file", map[string]any{"folder": "proj", "fileId": "big", "offset": next})
		Expect(a.payload["offset"]).To(BeNumerically("==", next))
		Expect(a.payload["content"]).NotTo(BeEmpty())

		By("refusing a folder that is not on the list before the add-on runs, and a file outside the folder in the add-on")
		before := len(drive.Calls())
		a = u.tool(c.owner, "search", map[string]any{"folder": "other", "name": "secret"})
		Expect(a.result.Kind).To(Equal(tools.ResultError))
		Expect(a.result.Code).To(Equal(tools.CodeResourceNotAllowed))
		Expect(drive.Calls()[before:]).To(BeEmpty())
		a = u.tool(c.owner, "get-metadata", map[string]any{"folder": "proj", "fileId": "secret"})
		Expect(a.result.Kind).To(Equal(tools.ResultError), a.text)
		Expect(a.result.Code).To(Equal(tools.CodeInvalidRequest))
		Expect(a.text).NotTo(ContainSubstring("secret.txt"))

		By("storing a write as a proposal that changes nothing until the person confirms")
		a = u.tool(c.owner, "create-file", map[string]any{"folder": "proj", "name": "draft.md", "content": "# draft", "mimeType": "text/markdown"})
		Expect(a.text).To(ContainSubstring("proposed a change"))
		Expect(a.text).To(ContainSubstring("Nothing has been changed"))
		Expect(driveWrites()).To(BeEmpty())
		Expect(drive.Find("draft.md")).To(BeEmpty())
		id, digest := u.proposed(c.owner, a.text)
		rec := u.confirm(c.owner, id, digest)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		made := drive.Find("draft.md")
		Expect(made).To(HaveLen(1))
		Expect(string(made[0].Content)).To(Equal("# draft"))
		Expect(made[0].Parents).To(Equal([]string{"proj"}))
		Expect(driveWrites()).NotTo(BeEmpty())
		for _, call := range driveWrites() {
			Expect(call.Token).To(Equal(u.p.LastAccess()))
		}

		By("renewing the token once the clock passes its end")
		tokenCalls := len(u.tokenCalls(0))
		c.clock.Advance(2 * time.Hour)
		a = u.tool(c.owner, "get-metadata", map[string]any{"folder": "proj", "fileId": "notes"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(u.tokenCalls(0)).To(HaveLen(tokenCalls+1), "one refresh")
		Expect(u.tokenCalls(0)[tokenCalls].Form.Get("grant_type")).To(Equal("refresh_token"))
		Expect(drive.Calls()[len(drive.Calls())-1].Token).To(Equal(u.p.LastAccess()), "the new token reached Drive")

		By("renewing once when Drive rejects the token, and retrying the call")
		rejected := u.p.LastAccess()
		revoke(u, rejected)
		tokenCalls = len(u.tokenCalls(0))
		a = u.tool(c.owner, "get-metadata", map[string]any{"folder": "proj", "fileId": "notes"})
		Expect(a.result.Kind).To(Equal(tools.ResultData), a.text)
		Expect(u.tokenCalls(0)).To(HaveLen(tokenCalls + 1))
		Expect(u.p.LastAccess()).NotTo(Equal(rejected))

		By("telling the model to ask for a reconnect when the renewed token is rejected too")
		revokeAll(u)
		a = u.tool(c.owner, "get-metadata", map[string]any{"folder": "proj", "fileId": "notes"})
		Expect(a.result.Code).To(Equal(tools.CodeReconnectNeeded), a.text)
		Expect(u.oauth(c.owner)["state"]).To(Equal("reconnect_needed"))
		clearRevoked(u)
		u.connect(nil)
		Expect(u.oauth(c.owner)["scopes"]).To(ConsistOf(driveRead), "the second login asked for the required scope only")

		By("degrading the write tools when only the read scope was granted")
		before = len(drive.Calls())
		a = u.tool(c.owner, "create-file", map[string]any{"folder": "proj", "name": "nope.txt"})
		Expect(a.result.Kind).To(Equal(tools.ResultError), a.text)
		Expect(a.text).To(ContainSubstring("drive.file"), "the refusal names the missing scope")
		Expect(a.text).NotTo(ContainSubstring("403"))
		for _, call := range drive.Calls()[before:] {
			Expect(call.Method).To(Equal(http.MethodGet), "no write was tried: %s %s", call.Method, call.Path)
		}
		Expect(drive.Find("nope.txt")).To(BeEmpty())
		u.expectNoSecret(a.text)

		By("disconnecting, which revokes at the service")
		before = len(u.p.Calls())
		rec = c.owner.do("alice", http.MethodPost, "/api/chats/connectors/"+u.binding+"/oauth/disconnect", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decode(rec)).To(Equal(map[string]any{"revoked": true}))
		Expect(u.p.Calls()[before:]).To(HaveLen(1))
		Expect(u.p.Calls()[before].Endpoint).To(Equal("revoke"))
		a = u.tool(c.owner, "search", map[string]any{"folder": "proj", "name": "notes"})
		Expect(a.result.Code).To(Equal(tools.CodeNoCredential))

		By("having kept every secret out of what the model, the person and the logs saw")
		u.expectClientAuth()
		for _, t := range append(u.texts, c.logText()) {
			u.expectNoSecret(t)
			Expect(t).NotTo(ContainSubstring("hello from drive"), "no file content in a log")
		}
	})

	It("tells the model how long to wait when Drive limits the calls", func() {
		u.connect([]string{driveWrite})
		drive.Inject("/drive/v3/files/notes", http.StatusTooManyRequests, "rateLimitExceeded", "9", 1)
		a := u.tool(c.owner, "get-metadata", map[string]any{"folder": "proj", "fileId": "notes"})
		Expect(a.result.Kind).To(Equal(tools.ResultError), a.text)
		Expect(a.result.Code).To(Equal(tools.CodeRateLimited))
		Expect(a.result.RetryAfter).To(Equal(9))
		Expect(u.oauth(c.owner)["state"]).To(Equal("connected"), "a limit does not end the login")
		Expect(u.tool(c.owner, "get-metadata", map[string]any{"folder": "proj", "fileId": "notes"}).result.Kind).To(Equal(tools.ResultData))
	})
})

// The access tokens that the fake Drive rejects, per person.
var rejectedAccess = map[*person]map[string]bool{}

func revokedAccess(u *person) []string {
	var out []string
	for t := range rejectedAccess[u] {
		out = append(out, t)
	}
	return out
}

func revoke(u *person, tok string) {
	if rejectedAccess[u] == nil {
		rejectedAccess[u] = map[string]bool{}
	}
	rejectedAccess[u][tok] = true
}

// revokeAll makes Drive reject every access token issued so far, and the
// login service refuse every refresh token.
func revokeAll(u *person) {
	for _, t := range u.p.Issued() {
		if strings.Contains(t, "-access-") {
			revoke(u, t)
		}
	}
	u.p.RevokeAll()
}

func clearRevoked(u *person) { delete(rejectedAccess, u) }
