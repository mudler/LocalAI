package studio_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken/oauthtokentest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

const (
	oauthAddon = "providerA"
	oauthKind  = "providera"
	// tokenAddon serves tokenKind, whose bindings hold a token.
	tokenAddon       = "tokenservice"
	tokenKind        = "tokenservice"
	oauthRedirect    = "https://localai.example.test/api/chats/connectors/oauth/callback"
	oauthReturn      = "/app/providerA"
	oauthCode        = "code-sentinel"
	oauthSecret      = "client-secret-sentinel"
	oauthAccountName = "someone@example.test"
)

// meTransport is a connector add-on that only answers "me": it refuses the
// tokens in refuse and names the account for any other.
type meTransport struct {
	mu     sync.Mutex
	refuse map[string]bool
	// label, when set, is the account name "me" answers.
	label string
	calls atomic.Int32
	conns *studio.Connectors
}

func (t *meTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	return []studio.ConnectorKind{
		{AddonID: oauthAddon, AuthKind: contract.CredentialKindOAuth2, ConnectorKind: contract.ConnectorKind{ID: oauthKind, Title: "Provider A", AgentOptional: true}},
		{AddonID: tokenAddon, ConnectorKind: contract.ConnectorKind{ID: tokenKind, Title: "Token service", AgentOptional: true}},
	}, nil
}

func (t *meTransport) Call(ctx context.Context, scope studio.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	t.calls.Add(1)
	// The gateway asks the host for the authority of every connector call.
	req, err := t.conns.AuthorizeConnector(ctx, scope, req)
	if err != nil {
		return contract.ConnectorResponse{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.refuse[req.Token] {
		return contract.ConnectorResponse{Error: &contract.ConnectorError{Code: contract.ConnectorUnauthorized}}, nil
	}
	if t.label != "" {
		return contract.ConnectorResponse{BotUsername: t.label}, nil
	}
	return contract.ConnectorResponse{BotUsername: oauthAccountName}, nil
}

type oauthRig struct {
	sh    *shared
	svc   *studio.Service
	st    connectors.Store
	srv   *oauthtokentest.Server
	tr    *meTransport
	logs  *syncBuffer
	conns *studio.Connectors
	// waits counts the waits of callers that lost the refresh lease.
	waits atomic.Int32

	dmu  sync.Mutex
	desc contract.OAuthDescriptor
}

func newOAuthRig(st connectors.Store) *oauthRig {
	sh := newShared()
	tr := sh.rig(connSay("x"), studio.TriggerOptions{})
	r := &oauthRig{sh: sh, svc: tr.svc, st: st, srv: oauthtokentest.New(), logs: &syncBuffer{}}
	DeferCleanup(r.srv.Close)
	r.srv.Set(func(s *oauthtokentest.Server) { s.ExpiresIn = 3600 })
	r.desc = r.srv.Descriptor(contract.OAuthClientAuthBasic)
	r.tr = &meTransport{refuse: map[string]bool{}}
	r.conns = r.instance("front-a")
	r.tr.conns = r.conns
	return r
}

// instance is another connector service on the same store and token
// endpoint, as a second frontend would run it.
func (r *oauthRig) instance(name string) *studio.Connectors {
	return r.instanceOn(name, r.st)
}

// instanceOn is an instance that reaches the store through st.
func (r *oauthRig) instanceOn(name string, st connectors.Store) *studio.Connectors {
	c, err := studio.NewConnectors(r.svc, studio.ConnectorOptions{
		Store: st, Transport: r.tr, Clock: r.sh.clock.Now, InstanceID: name, TokenClient: r.srv.Client(),
		OAuthDescriptor: func(context.Context, string) (contract.OAuthDescriptor, error) {
			r.dmu.Lock()
			defer r.dmu.Unlock()
			return r.desc, nil
		},
		Logger: slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		// A caller that lost the refresh lease counts its wait here, so a test
		// knows the caller is waiting without guessing a time.
		LeaseWait: func(ctx context.Context, d time.Duration) error {
			r.waits.Add(1)
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(c.Close)
	return c
}

func (r *oauthRig) setDescriptor(f func(d *contract.OAuthDescriptor)) {
	r.dmu.Lock()
	defer r.dmu.Unlock()
	f(&r.desc)
}

func (r *oauthRig) digest() string {
	r.dmu.Lock()
	defer r.dmu.Unlock()
	return r.desc.Digest()
}

func (r *oauthRig) approve() {
	secret := oauthSecret
	_, err := r.conns.SetClient(context.Background(), oauthAddon, studio.OAuthClientInput{Digest: r.digest(), ClientID: "client-1", Secret: &secret, ApprovedBy: "admin"})
	Expect(err).NotTo(HaveOccurred())
}

func (r *oauthRig) binding() connectors.Binding {
	got, err := r.conns.CreateConnector(context.Background(), alice, studio.ConnectorInput{Kind: oauthKind, Name: "Work", AuthKind: connectors.AuthOAuth2, Resources: []string{"acme/widgets"}})
	Expect(err).NotTo(HaveOccurred())
	return got.Binding
}

// start begins a login and returns it with the state from its URL.
func (r *oauthRig) start(b connectors.Binding, optional ...string) (studio.LoginStart, string) {
	ls, err := r.conns.StartLogin(context.Background(), alice, b.ID, optional, oauthRedirect, oauthReturn)
	Expect(err).NotTo(HaveOccurred())
	u, err := url.Parse(ls.AuthorizeURL)
	Expect(err).NotTo(HaveOccurred())
	return ls, u.Query().Get("state")
}

// connect makes an approved, connected binding.
func (r *oauthRig) connect() connectors.Binding {
	r.approve()
	b := r.binding()
	ls, state := r.start(b)
	res, err := r.conns.CompleteLogin(context.Background(), alice, ls.Nonce, state, oauthCode, "")
	Expect(err).NotTo(HaveOccurred())
	Expect(res.Outcome).To(Equal(studio.LoginOK))
	return b
}

func (r *oauthRig) grant(id string) connectors.Grant {
	g, err := r.st.GetGrant(context.Background(), id)
	Expect(err).NotTo(HaveOccurred())
	return g
}

// faultyStore is a store that fails the read of the sealing key, or every
// write of a grant, while the flag is set: an outage that starts after a
// refresh took its lease.
type faultyStore struct {
	connectors.Store
	keyDown    atomic.Bool
	writesDown atomic.Bool
}

var errStoreDown = errors.New("store unavailable")

func (f *faultyStore) EnsureKey(ctx context.Context, generate func() ([]byte, error)) ([]byte, error) {
	if f.keyDown.Load() {
		return nil, errStoreDown
	}
	return f.Store.EnsureKey(ctx, generate)
}

func (f *faultyStore) UpdateGrant(ctx context.Context, g connectors.Grant, version int64) (int64, error) {
	if f.writesDown.Load() {
		return 0, errStoreDown
	}
	return f.Store.UpdateGrant(ctx, g, version)
}

func sqliteConnStore() connectors.Store {
	path := filepath.Join(GinkgoT().TempDir(), "c.db")
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if d, err := db.DB(); err == nil {
			_ = d.Close()
		}
	})
	s, err := connectors.NewGORM(db)
	Expect(err).NotTo(HaveOccurred())
	return s
}

var _ = Describe("OAuth connections", func() {
	ctx := context.Background()

	for _, kind := range []string{"json", "sqlite"} {
		newStore := connStore
		if kind == "sqlite" {
			newStore = sqliteConnStore
		}
		Describe("on the "+kind+" store", func() {
			var r *oauthRig
			BeforeEach(func() { r = newOAuthRig(newStore()) })

			Describe("the login", func() {
				It("needs an approved descriptor before it starts", func() {
					b := r.binding()
					_, err := r.conns.StartLogin(ctx, alice, b.ID, nil, oauthRedirect, oauthReturn)
					Expect(err).To(MatchError(studio.ErrNeedsApproval))
					r.approve()
					r.setDescriptor(func(d *contract.OAuthDescriptor) { d.Scopes.Required = append(d.Scopes.Required, "admin") })
					_, err = r.conns.StartLogin(ctx, alice, b.ID, nil, oauthRedirect, oauthReturn)
					Expect(err).To(MatchError(studio.ErrNeedsApproval), "a changed descriptor voids the approval")
				})

				It("refuses a start while a binding has too many logins waiting, and limits how often a person starts one", func() {
					r.approve()
					b := r.binding()
					for range connectors.MaxLoginsPerBinding {
						_, err := r.conns.StartLogin(ctx, alice, b.ID, nil, oauthRedirect, oauthReturn)
						Expect(err).NotTo(HaveOccurred())
					}
					_, err := r.conns.StartLogin(ctx, alice, b.ID, nil, oauthRedirect, oauthReturn)
					Expect(err).To(MatchError(studio.ErrTooManyLogins))

					// More bindings do not lift the limit of the person's starts.
					more := []connectors.Binding{}
					for i := range 2 {
						got, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: oauthKind, Name: fmt.Sprintf("Other %d", i), AuthKind: connectors.AuthOAuth2})
						Expect(err).NotTo(HaveOccurred())
						more = append(more, got.Binding)
					}
					// The refused start counts too.
					started := connectors.MaxLoginsPerBinding + 1
					for started < studio.MaxLoginStartsPerMinute {
						_, err := r.conns.StartLogin(ctx, alice, more[started%2].ID, nil, oauthRedirect, oauthReturn)
						Expect(err).NotTo(HaveOccurred())
						started++
					}
					_, err = r.conns.StartLogin(ctx, alice, more[0].ID, nil, oauthRedirect, oauthReturn)
					Expect(err).To(MatchError(studio.ErrTooManyLogins), "the person started too many logins in a minute")
					theirs, err := r.conns.CreateConnector(ctx, "bob", studio.ConnectorInput{Kind: oauthKind, Name: "Work", AuthKind: connectors.AuthOAuth2})
					Expect(err).NotTo(HaveOccurred())
					_, err = r.conns.StartLogin(ctx, "bob", theirs.ID, nil, oauthRedirect, oauthReturn)
					Expect(err).NotTo(HaveOccurred(), "another person has a limit of their own")

					r.sh.clock.Advance(time.Minute)
					_, err = r.conns.StartLogin(ctx, alice, more[0].ID, nil, oauthRedirect, oauthReturn)
					Expect(err).NotTo(HaveOccurred(), "the limit is per minute")
					r.sh.clock.Advance(connectors.LoginStateTTL)
					_, err = r.conns.StartLogin(ctx, alice, b.ID, nil, oauthRedirect, oauthReturn)
					Expect(err).NotTo(HaveOccurred(), "ended logins free their place")
				})

				It("sends the person to the service with PKCE, the scopes and the stored redirect address", func() {
					r.approve()
					b := r.binding()
					ls, err := r.conns.StartLogin(ctx, alice, b.ID, []string{"write"}, oauthRedirect, oauthReturn)
					Expect(err).NotTo(HaveOccurred())
					u, _ := url.Parse(ls.AuthorizeURL)
					Expect(u.Scheme + "://" + u.Host + u.Path).To(Equal("https://" + oauthtokentest.Host + "/authorize"))
					q := u.Query()
					Expect(q.Get("response_type")).To(Equal("code"))
					Expect(q.Get("client_id")).To(Equal("client-1"))
					Expect(q.Get("redirect_uri")).To(Equal(oauthRedirect))
					Expect(q.Get("scope")).To(Equal("read write"))
					Expect(q.Get("code_challenge_method")).To(Equal("S256"))
					Expect(q.Get("code_challenge")).To(HaveLen(43))
					Expect(q.Get("state")).To(HaveLen(43))
					Expect(q.Has("client_secret")).To(BeFalse())
					Expect(ls.Nonce).NotTo(BeEmpty())
					Expect(ls.Expires).To(BeTemporally("==", r.sh.clock.Now().Add(connectors.LoginStateTTL)))

					_, err = r.conns.StartLogin(ctx, alice, b.ID, []string{"admin"}, oauthRedirect, oauthReturn)
					Expect(err).To(MatchError(connectors.ErrInvalid), "a scope that is not optional")
					_, err = r.conns.StartLogin(ctx, alice, b.ID, nil, oauthRedirect, "https://elsewhere.example.test/")
					Expect(err).To(MatchError(connectors.ErrInvalid), "a return path to another host")
					_, err = r.conns.StartLogin(ctx, "mallory", b.ID, nil, oauthRedirect, oauthReturn)
					Expect(err).To(MatchError(connectors.ErrNotFound))
				})

				It("keeps the hash of the state, never the state, and the code is traded with the stored verifier", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					sum := sha256.Sum256([]byte(state))
					// The hash is the key: the raw state finds nothing.
					_, ok, err := r.st.ConsumeLoginState(ctx, state, r.sh.clock.Now())
					Expect(err).NotTo(HaveOccurred())
					Expect(ok).To(BeFalse())
					res, err := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(res).To(Equal(studio.LoginResult{Outcome: studio.LoginOK, ReturnTo: oauthReturn, BindingID: b.ID}))
					_, ok, _ = r.st.ConsumeLoginState(ctx, hex.EncodeToString(sum[:]), r.sh.clock.Now())
					Expect(ok).To(BeFalse(), "the state was used up")

					f := r.srv.Forms()[0]
					Expect(f.Get("code")).To(Equal(oauthCode))
					Expect(f.Get("redirect_uri")).To(Equal(oauthRedirect))
					u, _ := url.Parse(ls.AuthorizeURL)
					challenge := sha256.Sum256([]byte(f.Get("code_verifier")))
					Expect(base64.RawURLEncoding.EncodeToString(challenge[:])).To(Equal(u.Query().Get("code_challenge")))
					Expect(r.srv.Auths()[0]).To(Equal(oauthtokentest.BasicAuth("client-1", oauthSecret)))

					g := r.grant(b.ID)
					Expect(g.State).To(Equal(connectors.GrantConnected))
					Expect(g.AccountLabel).To(Equal(oauthAccountName))
					Expect(g.Scopes).To(Equal([]string{"read"}))
					Expect(g.ExpiresAt).To(BeTemporally("==", r.sh.clock.Now().Add(time.Hour)))
					Expect(g.DescriptorDigest).To(Equal(r.digest()))

					grant, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
					Expect(err).NotTo(HaveOccurred())
					Expect(grant.Secret).To(HavePrefix("access-"))
					Expect(grant.Scopes).To(Equal([]string{"read"}))
					Expect(grant.Version).To(BeEquivalentTo(1))
					list, err := r.conns.Connections(ctx, alice, oauthAddon)
					Expect(err).NotTo(HaveOccurred())
					Expect(list).To(HaveLen(1))
				})

				It("keeps only the scopes the descriptor names when a provider grants more", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					// include_granted_scopes: the answer lists scopes of earlier
					// logins and of other products too.
					r.srv.Set(func(s *oauthtokentest.Server) { s.Scope = "read write https://other.example.test/scope" })
					res, err := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(res.Outcome).To(Equal(studio.LoginOK))
					Expect(r.grant(b.ID).Scopes).To(Equal([]string{"read", "write"}), "optional scopes the person did not tick are still named by the descriptor")
					grant, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
					Expect(err).NotTo(HaveOccurred())
					Expect(grant.Scopes).NotTo(ContainElement("https://other.example.test/scope"))
				})

				It("still fails a login that lacks a required scope among extra ones", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					r.srv.Set(func(s *oauthtokentest.Server) { s.Scope = "write https://other.example.test/scope" })
					res, _ := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginScopesMissing))
					_, err := r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound))
				})

				It("works once, also when the exchange fails", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					r.srv.Set(func(s *oauthtokentest.Server) { s.Fail = "invalid_grant" })
					res, _ := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginExchangeFailed))
					r.srv.Set(func(s *oauthtokentest.Server) { s.Fail = "" })
					res, _ = r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginInvalidState))
					Expect(res.ReturnTo).To(BeEmpty())
					_, err := r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound))
				})

				It("refuses an expired state, another person and another browser", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					r.sh.clock.Advance(connectors.LoginStateTTL + time.Second)
					res, _ := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginInvalidState))

					ls, state = r.start(b)
					res, _ = r.conns.CompleteLogin(ctx, "mallory", ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginInvalidState))
					ls, state = r.start(b)
					res, _ = r.conns.CompleteLogin(ctx, alice, "another-nonce", state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginInvalidState))
					Expect(r.srv.Tokens.Load()).To(BeZero(), "no code was traded")
				})

				It("reports a denial, a service error and missing scopes as fixed classes", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					res, _ := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, "", "access_denied")
					Expect(res.Outcome).To(Equal(studio.LoginDenied))
					Expect(res.ReturnTo).To(Equal(oauthReturn))
					ls, state = r.start(b)
					res, _ = r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, "", "server_error")
					Expect(res.Outcome).To(Equal(studio.LoginProviderError))
					ls, state = r.start(b)
					res, _ = r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, "", "")
					Expect(res.Outcome).To(Equal(studio.LoginProviderError))
					r.srv.Set(func(s *oauthtokentest.Server) { s.Scope = "write" })
					ls, state = r.start(b)
					res, _ = r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginScopesMissing))
					r.srv.Set(func(s *oauthtokentest.Server) {
						s.Scope, s.Body = "", `{"access_token":"`+strings.Repeat("a", contract.MaxCredentialBytes+1)+`","token_type":"bearer"}`
					})
					ls, state = r.start(b)
					res, _ = r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginTokenTooLarge))
					r.srv.Set(func(s *oauthtokentest.Server) { s.Body, s.Fail, s.FailStatus = "", "server_error", 503 })
					ls, state = r.start(b)
					res, _ = r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginUnavailable))
					_, err := r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound))
				})

				It("removes control and bidirectional characters from the account name and bounds it", func() {
					r.tr.mu.Lock()
					r.tr.label = "\u202eevil\u200b\x00 user\n\t\u2066name\u2069  " + strings.Repeat("é", 300)
					r.tr.mu.Unlock()
					b := r.connect()
					label := r.grant(b.ID).AccountLabel
					Expect(label).To(HavePrefix("evil user name "), "the stored label is the clean one")
					Expect([]rune(label)).To(HaveLen(studio.MaxAccountLabelRunes))
				})

				It("fails when the service refuses the new token at the connector's me, or the descriptor changed since the start", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					r.tr.mu.Lock()
					r.tr.refuse["access-2"] = true
					r.tr.mu.Unlock()
					res, _ := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginExchangeFailed))
					ls, state = r.start(b)
					r.setDescriptor(func(d *contract.OAuthDescriptor) { d.Scopes.Optional = nil })
					res, _ = r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginExchangeFailed))
					_, err := r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound))
				})
			})

			Describe("the refresh", func() {
				It("uses the token until two minutes before its end, then refreshes it once", func() {
					b := r.connect()
					first, _ := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					r.sh.clock.Advance(57 * time.Minute)
					again, _ := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(again.Secret).To(Equal(first.Secret))
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(1))
					r.sh.clock.Advance(time.Minute + time.Second)
					fresh, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(fresh.Secret).NotTo(Equal(first.Secret))
					Expect(fresh.Version).To(BeEquivalentTo(2))
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2))
					Expect(r.grant(b.ID).ExpiresAt).To(BeTemporally("==", r.sh.clock.Now().Add(time.Hour)))
				})

				It("uses a token shorter than the margin for the first half of its life", func() {
					r.srv.Set(func(s *oauthtokentest.Server) { s.ExpiresIn = 60 })
					b := r.connect()
					first, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).NotTo(HaveOccurred())
					r.sh.clock.Advance(29 * time.Second)
					again, _ := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(again.Secret).To(Equal(first.Secret))
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(1), "a one-minute token is not renewed on every call")
					r.sh.clock.Advance(2 * time.Second)
					fresh, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(fresh.Secret).NotTo(Equal(first.Secret))
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2))
					_, _ = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2), "the renewed token is fresh")
				})

				It("writes back a rotated refresh token before it returns the access token", func() {
					r.srv.Set(func(s *oauthtokentest.Server) { s.Rotate = true })
					b := r.connect()
					for range 3 {
						r.sh.clock.Advance(time.Hour)
						_, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
						Expect(err).NotTo(HaveOccurred(), "a rotating service refuses a used refresh token, so the new one was stored")
					}
					forms := r.srv.Forms()
					Expect(forms[1].Get("refresh_token")).NotTo(Equal(forms[2].Get("refresh_token")))
					Expect(r.grant(b.ID).Version).To(BeEquivalentTo(4))
				})

				It("makes one token call for 50 concurrent calls", func() {
					b := r.connect()
					r.sh.clock.Advance(time.Hour)
					gate := make(chan struct{})
					r.srv.Set(func(s *oauthtokentest.Server) { s.Rotate, s.Gate = true, gate })
					secrets := make(chan string, 50)
					var wg sync.WaitGroup
					for range 50 {
						wg.Go(func() {
							defer GinkgoRecover()
							g, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
							Expect(err).NotTo(HaveOccurred())
							secrets <- g.Secret
						})
					}
					// The one refresh waits at the service while the others join it.
					Eventually(r.srv.Tokens.Load).Should(BeEquivalentTo(2))
					close(gate)
					wg.Wait()
					close(secrets)
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2), "the exchange and one refresh")
					seen := map[string]bool{}
					for s := range secrets {
						seen[s] = true
					}
					Expect(seen).To(HaveLen(1))
				})

				It("makes one token call for two frontends on one store", func() {
					b := r.connect()
					other := r.instance("front-b")
					r.sh.clock.Advance(time.Hour)
					gate := make(chan struct{})
					r.srv.Set(func(s *oauthtokentest.Server) { s.Rotate, s.Gate = true, gate })
					var wg sync.WaitGroup
					errs := make(chan error, 40)
					for i := range 40 {
						c := r.conns
						if i%2 == 1 {
							c = other
						}
						wg.Go(func() {
							_, err := c.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
							errs <- err
						})
					}
					Eventually(r.srv.Tokens.Load).Should(BeEquivalentTo(2))
					Eventually(r.waits.Load).Should(BeNumerically(">", 0), "the other callers wait for the refresh")
					close(gate)
					wg.Wait()
					close(errs)
					for err := range errs {
						Expect(err).NotTo(HaveOccurred())
					}
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2), "the exchange and one refresh across both frontends")
					Expect(r.grant(b.ID).LeaseOwner).To(BeEmpty())
				})

				It("never sends a refresh token twice while a slow token request outlives the request timeout", func() {
					b := r.connect()
					other := r.instance("front-b")
					r.sh.clock.Advance(time.Hour)
					gate := make(chan struct{})
					r.srv.Set(func(s *oauthtokentest.Server) { s.Rotate, s.Gate = true, gate })
					first := make(chan error, 1)
					go func() {
						_, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
						first <- err
					}()
					Eventually(r.srv.Tokens.Load).Should(BeEquivalentTo(2), "the refresh of front-a is at the service")
					// Longer than the timeout of the token request: the lease of
					// front-a must still hold, or front-b, renewing after a
					// rejection, would send the same refresh token again.
					r.sh.clock.Advance(45 * time.Second)
					type renewal struct {
						grant studio.CredentialGrant
						err   error
					}
					second := make(chan renewal, 1)
					go func() {
						g, err := other.RenewCredential(ctx, alice, oauthAddon, b.ID, "", 1, false)
						second <- renewal{g, err}
					}()
					// front-b found the lease held and waits; only now does the
					// service answer front-a.
					Eventually(r.waits.Load).Should(BeNumerically(">", 0))
					close(gate)
					got := <-second
					renewed, err := got.grant, got.err
					Expect(err).NotTo(HaveOccurred())
					Expect(<-first).NotTo(HaveOccurred())
					Expect(renewed.Version).To(BeEquivalentTo(2), "front-b uses the token front-a wrote")
					refreshes := 0
					for _, f := range r.srv.Forms() {
						if f.Get("grant_type") == "refresh_token" {
							refreshes++
						}
					}
					Expect(refreshes).To(Equal(1))
					Expect(r.grant(b.ID).State).To(Equal(connectors.GrantConnected))
				})

				It("ends the grant when a refresh drops a required scope, and never widens the granted scopes", func() {
					r.srv.Set(func(s *oauthtokentest.Server) { s.Scope = "read write" })
					r.approve()
					b := r.binding()
					ls, state := r.start(b, "write")
					res, err := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(res.Outcome).To(Equal(studio.LoginOK))

					r.sh.clock.Advance(time.Hour)
					r.srv.Set(func(s *oauthtokentest.Server) { s.Scope = "read admin" })
					got, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(got.Scopes).To(Equal([]string{"read"}), "a scope the login never had is dropped; a dropped optional one stays dropped")
					Expect(r.grant(b.ID).Scopes).To(Equal([]string{"read"}))

					r.sh.clock.Advance(time.Hour)
					r.srv.Set(func(s *oauthtokentest.Server) { s.Scope = "write" })
					_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					g := r.grant(b.ID)
					Expect(g.Reason).To(Equal(string(studio.ReasonScopesMissing)))
					Expect(g.Sealed).To(BeEmpty())
				})

				It("ends the grant, not every later call, when a refresh grants a scope a request refuses", func() {
					b := r.connect()
					r.sh.clock.Advance(time.Hour)
					r.srv.Set(func(s *oauthtokentest.Server) { s.Scope = `read "x` })
					_, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					Expect(r.grant(b.ID).Reason).To(Equal(string(studio.ReasonRefused)))
				})

				DescribeTable("ends the grant when the service refuses it for good, and wipes its secrets",
					func(set func(s *oauthtokentest.Server), reason string) {
						b := r.connect()
						r.sh.clock.Advance(time.Hour)
						r.srv.Set(set)
						_, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
						Expect(err).To(MatchError(studio.ErrReconnectNeeded))
						g := r.grant(b.ID)
						Expect(g.State).To(Equal(connectors.GrantReconnectNeeded))
						Expect(g.Reason).To(Equal(reason))
						Expect(g.Sealed).To(BeEmpty())
						calls := r.srv.Tokens.Load()
						_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
						Expect(err).To(MatchError(studio.ErrReconnectNeeded))
						Expect(r.srv.Tokens.Load()).To(Equal(calls), "an ended grant is not refreshed again")
						list, _ := r.conns.Connections(ctx, alice, oauthAddon)
						Expect(list).To(HaveLen(1), "the connection stays, so a call is told to reconnect")
						st := r.grant(b.ID)
						Expect(st.State).To(Equal(connectors.GrantReconnectNeeded))
						Expect(st.Reason).To(Equal(reason))
					},
					Entry("invalid_grant", func(s *oauthtokentest.Server) { s.Fail = "invalid_grant" }, string(studio.ReasonRevoked)),
					Entry("invalid_client", func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "invalid_client", 401 }, string(studio.ReasonClientRejected)),
					Entry("another refusal", func(s *oauthtokentest.Server) { s.Fail = "invalid_scope" }, string(studio.ReasonRefused)),
					Entry("an access token too large for a request", func(s *oauthtokentest.Server) {
						s.Body = `{"access_token":"` + strings.Repeat("a", contract.MaxCredentialBytes+1) + `","token_type":"bearer"}`
					}, string(studio.ReasonTokenTooLarge)),
				)

				It("keeps the grant as it was when the service is unavailable or rate limits", func() {
					b := r.connect()
					r.sh.clock.Advance(time.Hour)
					before := r.grant(b.ID)
					r.srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus = "server_error", 503 })
					_, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrCredentialUnavailable))
					r.srv.Set(func(s *oauthtokentest.Server) { s.Fail, s.FailStatus, s.RetryAfter = "slow_down", 429, "9" })
					_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					var rl *contract.ErrRateLimited
					Expect(errors.As(err, &rl)).To(BeTrue(), "%v", err)
					Expect(rl.RetryAfter).To(Equal(9))
					after := r.grant(b.ID)
					Expect(after.Version).To(Equal(before.Version))
					Expect(after.Sealed).To(Equal(before.Sealed))
					Expect(after.State).To(Equal(connectors.GrantConnected))
					Expect(after.LeaseOwner).To(BeEmpty(), "a failed refresh frees the lease")
					r.srv.Set(func(s *oauthtokentest.Server) { s.Fail = "" })
					_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).NotTo(HaveOccurred())
				})

				// notBlocked shows that a refresh which failed on front-b left
				// the lease free: front-a refreshes at once instead of waiting
				// for the lease and then answering unavailable.
				notBlocked := func(b connectors.Binding) (studio.CredentialGrant, error) {
					Expect(r.grant(b.ID).LeaseOwner).To(BeEmpty(), "a failed refresh frees the lease")
					began := time.Now()
					got, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(time.Since(began)).To(BeNumerically("<", 5*time.Second), "front-a waited for the lease")
					return got, err
				}

				It("frees the lease when the sealer is not available to the refresh", func() {
					// A public client has no secret, so the approval does not
					// need the sealer and the refresh is the first to ask for it.
					r.setDescriptor(func(d *contract.OAuthDescriptor) { *d = r.srv.Descriptor(contract.OAuthClientAuthNone) })
					_, err := r.conns.SetClient(ctx, oauthAddon, studio.OAuthClientInput{Digest: r.digest(), ClientID: "public-1", ApprovedBy: "admin"})
					Expect(err).NotTo(HaveOccurred())
					b := r.binding()
					ls, state := r.start(b)
					res, err := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(res.Outcome).To(Equal(studio.LoginOK))
					faulty := &faultyStore{Store: r.st}
					faulty.keyDown.Store(true)
					other := r.instanceOn("front-b", faulty)
					r.sh.clock.Advance(time.Hour)
					_, err = other.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrCredentialUnavailable))
					Expect(r.grant(b.ID).Version).To(BeEquivalentTo(1))
					got, err := notBlocked(b)
					Expect(err).NotTo(HaveOccurred())
					Expect(got.Version).To(BeEquivalentTo(2))
				})

				It("frees the lease when the store cannot write a refreshed token", func() {
					b := r.connect()
					faulty := &faultyStore{Store: r.st}
					faulty.writesDown.Store(true)
					other := r.instanceOn("front-b", faulty)
					r.sh.clock.Advance(time.Hour)
					_, err := other.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrCredentialUnavailable))
					got, err := notBlocked(b)
					Expect(err).NotTo(HaveOccurred())
					Expect(got.Version).To(BeEquivalentTo(2))
				})

				It("frees the lease when the store cannot write an ended grant", func() {
					b := r.connect()
					faulty := &faultyStore{Store: r.st}
					faulty.writesDown.Store(true)
					other := r.instanceOn("front-b", faulty)
					r.sh.clock.Advance(time.Hour)
					r.srv.Set(func(s *oauthtokentest.Server) { s.Fail = "invalid_grant" })
					_, err := other.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					Expect(r.grant(b.ID).State).To(Equal(connectors.GrantConnected), "the write failed")
					_, err = notBlocked(b)
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					Expect(r.grant(b.ID).Reason).To(Equal(string(studio.ReasonRevoked)))
				})

				It("keeps the lease when a rotated refresh token could not be written back", func() {
					r.srv.Set(func(s *oauthtokentest.Server) { s.Rotate = true })
					b := r.connect()
					faulty := &faultyStore{Store: r.st}
					faulty.writesDown.Store(true)
					other := r.instanceOn("front-b", faulty)
					r.sh.clock.Advance(time.Hour)
					_, err := other.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					// The stored refresh token may be dead now: until the lease
					// ends, no frontend sends it again.
					g := r.grant(b.ID)
					Expect(g.LeaseOwner).To(Equal("front-b"))
					Expect(g.Version).To(BeEquivalentTo(1))
				})

				DescribeTable("holds a login whose descriptor changed until an administrator approves it again",
					func(upgrade func(d *contract.OAuthDescriptor), keeps bool) {
						b := r.connect()
						r.sh.clock.Advance(time.Hour)
						r.setDescriptor(upgrade)
						_, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
						Expect(err).To(MatchError(studio.ErrReconnectNeeded))
						Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(1), "no token call before the approval")
						held := r.grant(b.ID)
						Expect(held.State).To(Equal(connectors.GrantNeedsApproval))
						Expect(held.Sealed).NotTo(BeEmpty(), "the tokens stay")
						_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
						Expect(err).To(MatchError(studio.ErrReconnectNeeded))
						Expect(r.grant(b.ID).Sealed).NotTo(BeEmpty(), "still held, not wiped, on later calls")

						r.approve()
						after := r.grant(b.ID)
						if !keeps {
							// The refresh token never goes to another token endpoint.
							Expect(after.State).To(Equal(connectors.GrantReconnectNeeded))
							Expect(after.Reason).To(Equal(string(studio.ReasonEndpointChanged)))
							Expect(after.Sealed).To(BeEmpty())
							_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
							Expect(err).To(MatchError(studio.ErrReconnectNeeded))
							Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(1))
							return
						}
						Expect(after.State).To(Equal(connectors.GrantConnected), "the approval carries the login over")
						Expect(after.DescriptorDigest).To(Equal(r.digest()))
						got, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
						Expect(err).NotTo(HaveOccurred(), "the person does not reconnect")
						Expect(got.Secret).NotTo(BeEmpty())
						Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2), "the refresh went to the unchanged token endpoint")
					},
					Entry("an optional scope added, the token endpoint unchanged", func(d *contract.OAuthDescriptor) {
						d.Scopes.Optional = append(d.Scopes.Optional, "admin")
					}, true),
					Entry("the token endpoint moved to another host", func(d *contract.OAuthDescriptor) {
						d.Hosts = append(d.Hosts, "tokens.example.test")
						d.TokenURL = "https://tokens.example.test/token"
					}, false),
					Entry("the token endpoint moved on the same host", func(d *contract.OAuthDescriptor) {
						d.TokenURL = "https://" + oauthtokentest.Host + "/other"
					}, false),
				)

				It("carries a held login over at the next call when the approval came another way", func() {
					b := r.connect()
					r.setDescriptor(func(d *contract.OAuthDescriptor) { d.Scopes.Optional = append(d.Scopes.Optional, "admin") })
					_, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					cl, err := r.st.GetOAuthClient(ctx, oauthAddon)
					Expect(err).NotTo(HaveOccurred())
					cl.DescriptorDigest = r.digest()
					Expect(r.st.PutOAuthClient(ctx, cl)).To(Succeed())
					_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).NotTo(HaveOccurred())
					Expect(r.grant(b.ID).State).To(Equal(connectors.GrantConnected))
				})

				It("ends a held login whose new descriptor requires a scope it was not granted", func() {
					b := r.connect()
					r.setDescriptor(func(d *contract.OAuthDescriptor) { d.Scopes.Required = append(d.Scopes.Required, "admin") })
					r.approve()
					g := r.grant(b.ID)
					Expect(g.State).To(Equal(connectors.GrantReconnectNeeded))
					Expect(g.Reason).To(Equal(string(studio.ReasonScopesMissing)))
				})

				It("ends a grant whose token ended when there is no refresh token", func() {
					b := r.connect()
					g := r.grant(b.ID)
					s, _ := connectors.NewSealer(mustKey(r.st))
					g.Sealed, _ = s.SealGrant(connectors.GrantAAD(b.ID, b.Owner, b.AddonID), connectors.GrantSecrets{Access: "a", TokenType: "Bearer"})
					written, err := r.st.UpdateGrant(ctx, g, g.Version)
					Expect(err).NotTo(HaveOccurred())
					Expect(written).To(Equal(g.Version + 1))
					r.sh.clock.Advance(time.Hour)
					_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					Expect(r.grant(b.ID).Reason).To(Equal(string(studio.ReasonExpired)))
				})
			})

			Describe("a rejected credential", func() {
				It("refreshes once, then a second rejection ends the grant", func() {
					b := r.connect()
					first, _ := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
					renewed, err := r.conns.RenewCredential(ctx, alice, oauthAddon, b.ID, "acme/widgets", first.Version, false)
					Expect(err).NotTo(HaveOccurred())
					Expect(renewed.Secret).NotTo(Equal(first.Secret))
					Expect(renewed.Version).To(Equal(first.Version + 1))
					Expect(renewed.Resource.Key).To(Equal("acme/widgets"))
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2))
					_, err = r.conns.RenewCredential(ctx, alice, oauthAddon, b.ID, "acme/widgets", renewed.Version, true)
					Expect(err).To(MatchError(studio.ErrReconnectNeeded))
					Expect(r.grant(b.ID).Reason).To(Equal(string(studio.ReasonCredentialRejected)))
					Expect(r.srv.Tokens.Load()).To(BeEquivalentTo(2), "the second rejection does not refresh")
				})

				It("uses the newer token without a refresh when the grant changed since the call began", func() {
					b := r.connect()
					first, _ := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					newer, _ := r.conns.RenewCredential(ctx, alice, oauthAddon, b.ID, "", first.Version, false)
					calls := r.srv.Tokens.Load()
					again, err := r.conns.RenewCredential(ctx, alice, oauthAddon, b.ID, "", first.Version, false)
					Expect(err).NotTo(HaveOccurred())
					Expect(again.Secret).To(Equal(newer.Secret))
					Expect(r.srv.Tokens.Load()).To(Equal(calls))
					_, err = r.conns.RenewCredential(ctx, alice, oauthAddon, b.ID, "", first.Version, true)
					Expect(err).To(MatchError(studio.ErrCredentialUnavailable), "a stale final rejection does not end a newer grant")
					Expect(r.grant(b.ID).State).To(Equal(connectors.GrantConnected))
				})

				It("cannot renew a token binding or another person's binding", func() {
					b := r.connect()
					_, err := r.conns.RenewCredential(ctx, "mallory", oauthAddon, b.ID, "", 1, false)
					Expect(err).To(MatchError(connectors.ErrNoCredential))
					_, err = r.conns.RenewCredential(ctx, alice, "providerB", b.ID, "", 1, false)
					Expect(err).To(MatchError(connectors.ErrNoCredential))
				})
			})

			Describe("disconnect and the client", func() {
				It("revokes the refresh token when the service has a revocation endpoint, and deletes the grant in any case", func() {
					b := r.connect()
					told, err := r.conns.Disconnect(ctx, alice, b.ID)
					Expect(err).NotTo(HaveOccurred())
					Expect(told).To(BeTrue())
					Expect(r.srv.Revokes.Load()).To(BeEquivalentTo(1))
					forms := r.srv.Forms()
					Expect(forms[len(forms)-1].Get("token")).To(HavePrefix("refresh-"))
					Expect(forms[len(forms)-1].Get("token_type_hint")).To(Equal("refresh_token"))
					_, err = r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound))
					list, _ := r.conns.Connections(ctx, alice, oauthAddon)
					Expect(list).To(BeEmpty())

					ls, state := r.start(b)
					res, _ := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
					Expect(res.Outcome).To(Equal(studio.LoginOK))
					r.srv.Set(func(s *oauthtokentest.Server) { s.RevokeStatus = 503 })
					told, err = r.conns.Disconnect(ctx, alice, b.ID)
					Expect(err).NotTo(HaveOccurred())
					Expect(told).To(BeFalse(), "the service was not told")
					_, err = r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound))
				})

				It("revokes the login when the binding is deleted, and the grant goes with the binding", func() {
					b := r.connect()
					Expect(r.conns.DeleteConnector(ctx, alice, b.ID)).To(Succeed())
					Expect(r.srv.Revokes.Load()).To(BeEquivalentTo(1))
					_, err := r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound))
				})

				It("leaves no grant when the binding is deleted while its callback trades the code", func() {
					r.approve()
					b := r.binding()
					ls, state := r.start(b)
					gate := make(chan struct{})
					r.srv.Set(func(s *oauthtokentest.Server) { s.Gate = gate })
					done := make(chan studio.LoginResult, 1)
					go func() {
						res, _ := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
						done <- res
					}()
					Eventually(r.srv.Tokens.Load).Should(BeEquivalentTo(1), "the exchange is at the service")
					Expect(r.st.DeleteBinding(ctx, b.ID)).To(Succeed())
					close(gate)
					res := <-done
					Expect(res.Outcome).NotTo(Equal(studio.LoginOK))
					_, err := r.st.GetGrant(ctx, b.ID)
					Expect(err).To(MatchError(connectors.ErrNotFound), "an orphan grant with live tokens")
				})

				It("does not tell a service without a revocation endpoint", func() {
					r.setDescriptor(func(d *contract.OAuthDescriptor) { d.RevokeURL = "" })
					b := r.connect()
					told, err := r.conns.Disconnect(ctx, alice, b.ID)
					Expect(err).NotTo(HaveOccurred())
					Expect(told).To(BeFalse())
					Expect(r.srv.Revokes.Load()).To(BeZero())
				})

				It("approves only the digest the administrator saw and never returns the secret", func() {
					secret := oauthSecret
					_, err := r.conns.SetClient(ctx, oauthAddon, studio.OAuthClientInput{Digest: "stale", ClientID: "c", Secret: &secret, ApprovedBy: "admin"})
					Expect(err).To(MatchError(studio.ErrApprovalStale))
					_, err = r.conns.SetClient(ctx, oauthAddon, studio.OAuthClientInput{Digest: r.digest(), ClientID: "c", ApprovedBy: "admin"})
					Expect(err).To(MatchError(connectors.ErrInvalid), "a confidential client needs a secret")
					st, err := r.conns.GetClientStatus(ctx, oauthAddon)
					Expect(err).NotTo(HaveOccurred())
					Expect(st.Configured).To(BeFalse())
					Expect(st.CurrentDigest).To(Equal(r.digest()))
					r.approve()
					st, _ = r.conns.GetClientStatus(ctx, oauthAddon)
					Expect(st.Configured && st.Approved && st.HasSecret).To(BeTrue())
					Expect(fmt.Sprintf("%+v", st)).NotTo(ContainSubstring(oauthSecret))
					stored, _ := r.st.GetOAuthClient(ctx, oauthAddon)
					Expect(string(stored.Secret)).NotTo(ContainSubstring(oauthSecret))

					// A new approval may keep the stored secret.
					r.setDescriptor(func(d *contract.OAuthDescriptor) { d.Scopes.Optional = nil })
					st, _ = r.conns.GetClientStatus(ctx, oauthAddon)
					Expect(st.Approved).To(BeFalse(), "a changed descriptor is not approved")
					_, err = r.conns.SetClient(ctx, oauthAddon, studio.OAuthClientInput{Digest: r.digest(), ClientID: "client-2", ApprovedBy: "admin"})
					Expect(err).NotTo(HaveOccurred())
					st, _ = r.conns.GetClientStatus(ctx, oauthAddon)
					Expect(st.Approved && st.HasSecret).To(BeTrue())
					Expect(st.ClientID).To(Equal("client-2"))
				})
			})

			It("keeps a client ID and secret of any visible ASCII, spaces included, as RFC 6749 allows", func() {
				id, secret := "client 1@example.test", "a secret/with+marks and spaces"
				st, err := r.conns.SetClient(ctx, oauthAddon, studio.OAuthClientInput{Digest: r.digest(), ClientID: id, Secret: &secret, ApprovedBy: "admin"})
				Expect(err).NotTo(HaveOccurred())
				Expect(st.ClientID).To(Equal(id))
				b := r.binding()
				ls, state := r.start(b)
				res, err := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Outcome).To(Equal(studio.LoginOK))
				Expect(r.srv.Auths()).To(ContainElement(oauthtokentest.BasicAuth(id, secret)))
				for _, bad := range []string{"tab\tin", "new\nline", "é"} {
					_, err := r.conns.SetClient(ctx, oauthAddon, studio.OAuthClientInput{Digest: r.digest(), ClientID: "c", Secret: &bad, ApprovedBy: "admin"})
					Expect(err).To(MatchError(connectors.ErrInvalid), bad)
				}
			})

			Describe("an OAuth binding", func() {
				It("takes no token, does not listen and is not a credential before its login", func() {
					_, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: oauthKind, Name: "W", AuthKind: connectors.AuthOAuth2, Token: "t"})
					Expect(err).To(MatchError(connectors.ErrInvalid))
					b := r.binding()
					_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "acme/widgets")
					Expect(err).To(MatchError(connectors.ErrNoCredential))
					_, err = r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "W", Token: "t"})
					Expect(err).To(MatchError(connectors.ErrInvalid))
					list, _ := r.conns.Connections(ctx, alice, oauthAddon)
					Expect(list).To(BeEmpty())
				})

				It("must match the credential of its add-on: a login for an OAuth add-on, a token for any other", func() {
					_, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: oauthKind, Name: "W", Token: "t"})
					Expect(err).To(MatchError(connectors.ErrInvalid))
					Expect(err.Error()).To(ContainSubstring(`auth_kind "oauth2"`))
					_, err = r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: tokenKind, Name: "W", AuthKind: connectors.AuthOAuth2})
					Expect(err).To(MatchError(connectors.ErrInvalid))
					Expect(err.Error()).To(ContainSubstring("takes a token"))
					_, err = r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: tokenKind, Name: "W", Token: "t"})
					Expect(err).NotTo(HaveOccurred())
				})
			})

			It("never puts a token, the code, the state, the verifier or the client secret in a log, an error or a result", func() {
				var texts []string
				add := func(v ...any) {
					for _, x := range v {
						if err, ok := x.(error); ok && err != nil {
							texts = append(texts, err.Error())
						} else if x != nil {
							texts = append(texts, fmt.Sprintf("%+v", x))
						}
					}
				}
				r.srv.Set(func(s *oauthtokentest.Server) { s.Rotate = true })
				r.approve()
				b := r.binding()
				ls, state := r.start(b)
				res, err := r.conns.CompleteLogin(ctx, alice, ls.Nonce, state, oauthCode, "")
				add(res, err)
				r.sh.clock.Advance(time.Hour)
				g, err := r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
				Expect(err).NotTo(HaveOccurred())
				_, err = r.conns.RenewCredential(ctx, alice, oauthAddon, b.ID, "", g.Version, false)
				add(err)
				for _, fail := range []string{"invalid_grant", "server_error"} {
					r.srv.Set(func(s *oauthtokentest.Server) { s.Fail = fail })
					r.sh.clock.Advance(time.Hour)
					_, err = r.conns.CredentialFor(ctx, alice, oauthAddon, b.ID, "")
					add(err)
				}
				cs, err := r.conns.GetClientStatus(ctx, oauthAddon)
				add(cs, err)
				told, err := r.conns.Disconnect(ctx, alice, b.ID)
				add(told, err)
				ls2, state2 := r.start(b)
				r.srv.Set(func(s *oauthtokentest.Server) { s.Fail = "invalid_grant" })
				res, err = r.conns.CompleteLogin(ctx, alice, ls2.Nonce, state2, oauthCode, "")
				add(res, err)

				var verifiers []string
				for _, f := range r.srv.Forms() {
					if v := f.Get("code_verifier"); v != "" {
						verifiers = append(verifiers, v)
					}
				}
				Expect(verifiers).NotTo(BeEmpty())
				secrets := append([]string{"access-", "refresh-", oauthCode, oauthSecret, state, state2, ls.Nonce, ls2.Nonce}, verifiers...)
				texts = append(texts, r.logs.String())
				Expect(r.logs.String()).To(ContainSubstring("needs a reconnect"), "the log was written")
				for _, t := range texts {
					Expect(oauthtokentest.HasNoSentinel(t, secrets...)).To(BeTrue(), "a secret in %q", t)
				}
				Expect(strings.Count(r.logs.String(), g.Secret)).To(BeZero())
			})
		})
	}
})

func mustKey(st connectors.Store) []byte {
	k, err := st.EnsureKey(context.Background(), connectors.NewKey)
	Expect(err).NotTo(HaveOccurred())
	return k
}
