// SPDX-License-Identifier: MIT
//go:build auth

package addons_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/policy/sharetest"
	"github.com/mudler/LocalAI/core/services/addons/resources"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

const shareSecret = "share-route-secret"

// shareRig is the generic sharing API over the real chain: real
// authentication and sessions, the routes as the host registers them, the
// gateway, the resource service and a policy with the example kind.
type shareRig struct {
	e        *echo.Echo
	db       *gorm.DB
	p        *policy.Policy
	sessions map[string]string
	keys     map[string]string
	actors   map[string]contract.Actor
}

func newShareRig() *shareRig { return newShareRigWith(nil) }

// newShareRigWith is newShareRig with options of the resource service.
func newShareRigWith(opts func(*resources.Options)) *shareRig {
	dir := GinkgoT().TempDir()
	models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
	Expect(os.Mkdir(models, 0o750)).To(Succeed())
	Expect(os.Mkdir(backends, 0o750)).To(Succeed())
	state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	app, err := application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(shareSecret))
	Expect(err).NotTo(HaveOccurred())
	r := &shareRig{db: app.AuthDB(), sessions: map[string]string{}, keys: map[string]string{}, actors: map[string]contract.Actor{}}
	// eve may use add-ons, but the example kind does not accept her;
	// dave may not use add-ons at all.
	for id, name := range map[string]string{"owner": "Olivia Owner", "bob": "Bob", "carol": "Carol", "eve": "Eve", "dave": "Dave"} {
		Expect(r.db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: name, Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		Expect(r.db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: id, Permissions: auth.PermissionMap{auth.FeatureAddons: id != "dave"}}).Error).To(Succeed())
		token, key, err := auth.CreateAPIKey(r.db, id, "test", auth.RoleUser, shareSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		r.keys[id] = token
		r.actors[id] = contract.Actor{UserID: id, CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}
		session, err := auth.CreateSession(r.db, id, shareSecret)
		Expect(err).NotTo(HaveOccurred())
		r.sessions[id] = session
	}
	store, err := policy.OpenStore(filepath.Join(dir, "policy"), policy.ModeNamedUser)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = store.Close() })
	revalidate := auth.AddonRevalidate(r.db, auth.AddonModeNamedUser)
	account := auth.AccountRevalidate(r.db, auth.AddonModeNamedUser)
	r.p = policy.New(store, policy.Options{Revalidate: revalidate, Account: account, DrainTimeout: 5 * time.Second})
	users := auth.NewAddonUsers(r.db)
	Expect(policy.RegisterKinds(r.p, &sharetest.Provider{Eligible: func(ctx context.Context, _ contract.Actor, id string) (bool, error) {
		if id == "eve" {
			return false, nil
		}
		return users.Eligible(ctx, id)
	}})).To(Succeed())
	ro := resources.Options{Policy: r.p, Users: users}
	if opts != nil {
		opts(&ro)
	}
	svc := resources.New(ro)
	gw := &gateway.Gateway{Resources: svc, Sharing: svc, Revalidate: revalidate, RevalidateAccount: func(ctx context.Context, a contract.Actor) error { return account(ctx, a, "") }}
	r.e = echo.New()
	r.e.Use(auth.Middleware(r.db, app.ApplicationConfig()))
	routes.RegisterAddonRoutesFor(r.e, gw, r.db, auth.AddonModeNamedUser)
	return r
}

// create makes a resource of the example kind owned by id.
func (r *shareRig) create(id string) policy.Ref {
	ref, err := r.p.CreateRef(context.Background(), r.actors[id], sharetest.Kind)
	Expect(err).NotTo(HaveOccurred())
	return ref
}

type shareReply struct {
	code int
	body []byte
}

func (s shareReply) json(v any) {
	ExpectWithOffset(1, json.Unmarshal(s.body, v)).To(Succeed(), string(s.body))
}

func (s shareReply) errType() string {
	var out struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(s.body, &out)
	return out.Error.Type
}

// browser is a same-origin fetch of the person's own tab, confirming id.
func (r *shareRig) browser(user, confirm string) func(*http.Request) {
	return func(q *http.Request) {
		q.AddCookie(&http.Cookie{Name: "session", Value: r.sessions[user]})
		q.Header.Set("Sec-Fetch-Site", "same-origin")
		q.Header.Set("Sec-Fetch-Mode", "cors")
		q.Header.Set("Sec-Fetch-Dest", "empty")
		if confirm != "" {
			q.Header.Set("X-LocalAI-Confirm", confirm)
		}
	}
}

func (r *shareRig) bearer(user string) func(*http.Request) {
	return func(q *http.Request) { q.Header.Set("Authorization", "Bearer "+r.keys[user]) }
}

func (r *shareRig) do(method, path, body string, opts ...func(*http.Request)) shareReply {
	q := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		q.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(q)
	}
	w := httptest.NewRecorder()
	r.e.ServeHTTP(w, q)
	return shareReply{w.Code, w.Body.Bytes()}
}

func grantsPath(ref policy.Ref) string {
	return "/api/shares/" + ref.Kind + "/" + ref.ResourceID + "/grants"
}

func grantedRoles(s shareReply) map[string]string {
	var list gateway.ShareList
	s.json(&list)
	out := map[string]string{}
	for _, g := range list.Grants {
		out[g.UserID] = g.Role
	}
	return out
}

var _ = Describe("generic sharing routes", Label("sharing"), func() {
	bg := context.Background()
	var r *shareRig
	BeforeEach(func() { r = newShareRig() })

	It("lists, shares, lowers, revokes and searches a kind that is not an add-on", func() {
		ref := r.create("owner")
		list := r.do("GET", "/api/shares/"+sharetest.Kind, "", r.browser("owner", ""))
		Expect(list.code).To(Equal(200), string(list.body))
		var idx gateway.ShareIndex
		list.json(&idx)
		Expect(idx.Roles).To(Equal([]string{"use", "manage"}))
		Expect(idx.Everyone).To(BeTrue())
		Expect(idx.Owned).To(HaveLen(1))
		Expect(idx.Owned[0].ID).To(Equal(ref.ResourceID))
		Expect(idx.Owned[0].Role).To(Equal("owner"))
		Expect(*idx.Owned[0].SharedWith).To(Equal(0))
		Expect(idx.Shared).To(BeEmpty())

		set := r.do("PUT", grantsPath(ref)+"/bob", `{"role":"manage"}`, r.browser("owner", ref.ResourceID))
		Expect(set.code).To(Equal(200), string(set.body))
		var after gateway.ShareList
		set.json(&after)
		Expect(after.Kind).To(Equal(sharetest.Kind))
		Expect(after.Grants).To(Equal([]gateway.Grant{{UserID: "bob", DisplayName: "Bob", Role: "manage"}}))
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], ref, policy.ActionWrite)).To(Succeed())

		// What bob sees: the resource, his role and the owner's name.
		mine := r.do("GET", "/api/shares/"+sharetest.Kind, "", r.bearer("bob"))
		Expect(mine.code).To(Equal(200), string(mine.body))
		mine.json(&idx)
		Expect(idx.Owned).To(BeEmpty())
		Expect(idx.Shared).To(Equal([]gateway.SharedResource{{ID: ref.ResourceID, Role: "manage", Owner: "Olivia Owner"}}))

		// Lowering takes the write away.
		Expect(r.do("PUT", grantsPath(ref)+"/bob", `{"role":"use"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(200))
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], ref, policy.ActionWrite)).To(MatchError(policy.ErrForbidden))
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], ref, policy.ActionRead)).To(Succeed())
		got := r.do("GET", grantsPath(ref), "", r.browser("owner", ref.ResourceID))
		Expect(got.code).To(Equal(200))
		Expect(grantedRoles(got)).To(Equal(map[string]string{"bob": "use"}))

		// The picker lists only accounts the kind accepts, never the owner.
		users := r.do("GET", "/api/shares/"+sharetest.Kind+"/"+ref.ResourceID+"/users?q=ca", "", r.browser("owner", ref.ResourceID))
		Expect(users.code).To(Equal(400))
		for q, want := range map[string][]string{"car": {"carol"}, "eve": {}, "dav": {}, "oli": {}} {
			users = r.do("GET", "/api/shares/"+sharetest.Kind+"/"+ref.ResourceID+"/users?q="+q, "", r.browser("owner", ref.ResourceID))
			Expect(users.code).To(Equal(200), string(users.body))
			var found gateway.UserList
			users.json(&found)
			ids := []string{}
			for _, u := range found.Users {
				ids = append(ids, u.ID)
			}
			Expect(ids).To(Equal(want), q)
		}

		// A role the kind does not grant, and people it does not accept.
		bad := r.do("PUT", grantsPath(ref)+"/carol", `{"role":"viewer"}`, r.browser("owner", ref.ResourceID))
		Expect(bad.code).To(Equal(400))
		Expect(bad.errType()).To(Equal("invalid_request"))
		Expect(r.do("PUT", grantsPath(ref)+"/eve", `{"role":"use"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(403))
		Expect(r.do("PUT", grantsPath(ref)+"/dave", `{"role":"use"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(403))
		Expect(r.do("PUT", grantsPath(ref)+"/owner", `{"role":"use"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(403))

		// Revoking ends it, and bob no longer sees the resource.
		rev := r.do("DELETE", grantsPath(ref)+"/bob", "", r.browser("owner", ref.ResourceID))
		Expect(rev.code).To(Equal(200), string(rev.body))
		Expect(grantedRoles(rev)).To(BeEmpty())
		mine = r.do("GET", "/api/shares/"+sharetest.Kind, "", r.bearer("bob"))
		mine.json(&idx)
		Expect(idx.Shared).To(BeEmpty())
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], ref, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
	})

	It("finds people for a kind by three characters of the display name, never by an address, at a bounded rate", func() {
		r = newShareRigWith(func(o *resources.Options) { o.UserSearchBurst, o.UserSearchEvery = 3, time.Hour })
		Expect(r.db.Create(&auth.User{ID: "nameless", Email: "ceo@example.test", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		Expect(r.db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: "nameless", Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error).To(Succeed())
		ref := r.create("owner")
		search := func(user, q string) (shareReply, []gateway.UserRef) {
			got := r.do("GET", "/api/shares/"+sharetest.Kind+"/"+ref.ResourceID+"/users?q="+q, "", r.browser(user, ref.ResourceID))
			var found gateway.UserList
			if got.code == 200 {
				got.json(&found)
			}
			return got, found.Users
		}
		got, _ := search("owner", "bo")
		Expect(got.code).To(Equal(400), "two characters are too few")
		got, found := search("owner", "ceo%40exa")
		Expect(got.code).To(Equal(200), string(got.body))
		Expect(found).To(BeEmpty(), "an address prefix is no oracle")
		got, found = search("owner", "ceo%40example.test")
		Expect(got.code).To(Equal(200), string(got.body))
		Expect(found).To(Equal([]gateway.UserRef{{ID: "nameless", DisplayName: ""}}), "the whole address finds an account without a name, shown without one")
		got, found = search("owner", "bob")
		Expect(got.code).To(Equal(200))
		Expect(found).To(Equal([]gateway.UserRef{{ID: "bob", DisplayName: "Bob"}}))
		// The owner used the 3 searches of the burst; the 4th waits.
		got, _ = search("owner", "car")
		Expect(got.code).To(Equal(429), string(got.body))
		Expect(got.errType()).To(Equal("rate_limited"))
		// Another person has a bucket of their own.
		theirs := r.create("carol")
		got = r.do("GET", "/api/shares/"+sharetest.Kind+"/"+theirs.ResourceID+"/users?q=bob", "", r.browser("carol", theirs.ResourceID))
		Expect(got.code).To(Equal(200), string(got.body))
	})

	It("shares with everyone beside a per-user grant, lowers it and revokes it", func() {
		ref := r.create("owner")
		Expect(r.do("PUT", grantsPath(ref)+"/@everyone", `{"role":"manage"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(200))
		set := r.do("PUT", grantsPath(ref)+"/bob", `{"role":"manage"}`, r.browser("owner", ref.ResourceID))
		Expect(set.code).To(Equal(200))
		Expect(grantedRoles(set)).To(Equal(map[string]string{"@everyone": "manage", "bob": "manage"}))

		// carol was never named and still sees it, through everyone.
		var idx gateway.ShareIndex
		r.do("GET", "/api/shares/"+sharetest.Kind, "", r.bearer("carol")).json(&idx)
		Expect(idx.Shared).To(Equal([]gateway.SharedResource{{ID: ref.ResourceID, Role: "manage", Everyone: true, Owner: "Olivia Owner"}}))
		// An account created after the grant is reached too.
		Expect(r.p.AuthorizeRef(bg, contract.Actor{UserID: "eve", CredentialID: r.actors["eve"].CredentialID, Mode: auth.AddonModeNamedUser}, ref, policy.ActionWrite)).To(Succeed())

		Expect(r.do("PUT", grantsPath(ref)+"/@everyone", `{"role":"use"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(200))
		Expect(r.p.AuthorizeRef(bg, r.actors["carol"], ref, policy.ActionWrite)).To(MatchError(policy.ErrForbidden))
		Expect(r.p.AuthorizeRef(bg, r.actors["carol"], ref, policy.ActionRead)).To(Succeed())
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], ref, policy.ActionWrite)).To(Succeed())

		rev := r.do("DELETE", grantsPath(ref)+"/@everyone", "", r.browser("owner", ref.ResourceID))
		Expect(rev.code).To(Equal(200))
		Expect(grantedRoles(rev)).To(Equal(map[string]string{"bob": "manage"}))
		Expect(r.p.AuthorizeRef(bg, r.actors["carol"], ref, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], ref, policy.ActionWrite)).To(Succeed())
	})

	It("refuses a non-owner, an API key, a tool-marked call and a missing confirmation", func() {
		ref := r.create("owner")
		Expect(r.do("PUT", grantsPath(ref)+"/bob", `{"role":"manage"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(200))
		for _, tc := range []struct {
			name string
			opt  func(*http.Request)
		}{
			{"grantee", r.browser("bob", ref.ResourceID)},
			{"api key", r.bearer("owner")},
			{"api key with browser headers", func(q *http.Request) { r.browser("owner", ref.ResourceID)(q); r.bearer("owner")(q) }},
			{"tool call", func(q *http.Request) { r.browser("owner", ref.ResourceID)(q); q.Header.Set("X-LocalAI-Tool-Call", "1") }},
			{"mcp session", func(q *http.Request) { r.browser("owner", ref.ResourceID)(q); q.Header.Set("Mcp-Session-Id", "s") }},
			{"agent", func(q *http.Request) { r.browser("owner", ref.ResourceID)(q); q.Header.Set("X-LocalAI-Agent", "a") }},
			{"no confirmation", r.browser("owner", "")},
			{"wrong confirmation", r.browser("owner", "r-other")},
		} {
			Expect(r.do("GET", grantsPath(ref), "", tc.opt).code).To(Equal(403), tc.name)
			Expect(r.do("PUT", grantsPath(ref)+"/carol", `{"role":"use"}`, tc.opt).code).To(Equal(403), tc.name)
			Expect(r.do("DELETE", grantsPath(ref)+"/bob", "", tc.opt).code).To(Equal(403), tc.name)
			Expect(r.do("GET", "/api/shares/"+sharetest.Kind+"/"+ref.ResourceID+"/users?q=ca", "", tc.opt).code).To(Equal(403), tc.name)
		}
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], ref, policy.ActionWrite)).To(Succeed())
		Expect(r.p.AuthorizeRef(bg, r.actors["carol"], ref, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		// Without a credential. The addons feature is not needed: dave has none.
		Expect(r.do("GET", "/api/shares/"+sharetest.Kind, "").code).To(Equal(401))
		Expect(r.do("GET", "/api/shares/"+sharetest.Kind, "", r.bearer("dave")).code).To(Equal(200))
	})

	It("answers an unknown resource, a resource of someone else and an unknown kind the same", func() {
		theirs := r.create("carol")
		mine := r.create("owner")
		ghost := policy.Ref{Kind: sharetest.Kind, ResourceID: "r0123456789abcdef0123456789abcde"}
		wrongKind := policy.Ref{Kind: "example:other", ResourceID: mine.ResourceID}
		addonKind := policy.Ref{Kind: "addon:tables", ResourceID: mine.ResourceID}
		var first shareReply
		for i, ref := range []policy.Ref{theirs, ghost, wrongKind, addonKind} {
			for _, q := range []struct{ method, path, body string }{
				{"GET", grantsPath(ref), ""},
				{"PUT", grantsPath(ref) + "/bob", `{"role":"use"}`},
				{"DELETE", grantsPath(ref) + "/bob", ""},
				{"GET", "/api/shares/" + ref.Kind + "/" + ref.ResourceID + "/users?q=bo", ""},
			} {
				got := r.do(q.method, q.path, q.body, r.browser("owner", ref.ResourceID))
				Expect(got.code).To(Equal(403), q.method+" "+q.path)
				if i == 0 && q.method == "GET" && first.code == 0 {
					first = got
				}
				Expect(got.body).To(MatchJSON(first.body), q.method+" "+q.path)
			}
		}
		// The listing of a kind nobody registered and of an add-on kind is the
		// same denial too.
		Expect(r.do("GET", "/api/shares/example:other", "", r.bearer("owner")).body).To(MatchJSON(first.body))
		Expect(r.do("GET", "/api/shares/addon:tables", "", r.bearer("owner")).body).To(MatchJSON(first.body))
		Expect(r.p.AuthorizeRef(bg, r.actors["bob"], theirs, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
	})

	It("cancels the grantee's admitted work before a revocation answers", func() {
		ref := r.create("owner")
		Expect(r.do("PUT", grantsPath(ref)+"/bob", `{"role":"manage"}`, r.browser("owner", ref.ResourceID)).code).To(Equal(200))
		entered, cancelled, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- r.p.AdmitRef(bg, r.actors["bob"], ref, policy.ActionWrite, func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			})
		}()
		Eventually(entered).Should(BeClosed())
		rev := r.do("DELETE", grantsPath(ref)+"/bob", "", r.browser("owner", ref.ResourceID))
		Expect(rev.code).To(Equal(200), string(rev.body))
		// The answer came after the work stopped.
		Expect(cancelled).To(BeClosed())
		Eventually(done).Should(Receive())
	})
})

var _ = Describe("generic sharing routes in local-owner mode", Label("sharing"), func() {
	It("answers sharing_disabled", func() {
		store, err := policy.OpenStore(filepath.Join(GinkgoT().TempDir(), "policy"), policy.ModeLocalOwner)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = store.Close() })
		revalidate := auth.AddonRevalidate(nil, auth.AddonModeLocalOwner)
		account := auth.AccountRevalidate(nil, auth.AddonModeLocalOwner)
		p := policy.New(store, policy.Options{Revalidate: revalidate, Account: account})
		Expect(policy.RegisterKinds(p, &sharetest.Provider{})).To(Succeed())
		owner := contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
		ref, err := p.CreateRef(context.Background(), owner, sharetest.Kind)
		Expect(err).NotTo(HaveOccurred())
		svc := resources.New(resources.Options{Policy: p})
		e := echo.New()
		e.Use(auth.Middleware(nil, &config.ApplicationConfig{}))
		routes.RegisterAddonRoutesFor(e, &gateway.Gateway{Resources: svc, Sharing: svc, Revalidate: revalidate, RevalidateAccount: func(ctx context.Context, a contract.Actor) error { return account(ctx, a, "") }}, nil, auth.AddonModeLocalOwner)
		for _, q := range []struct{ method, path, body string }{
			{"GET", "/api/shares/" + sharetest.Kind, ""},
			{"GET", grantsPath(ref), ""},
			{"PUT", grantsPath(ref) + "/bob", `{"role":"use"}`},
			{"PUT", grantsPath(ref) + "/@everyone", `{"role":"use"}`},
			{"DELETE", grantsPath(ref) + "/bob", ""},
			{"GET", "/api/shares/" + sharetest.Kind + "/" + ref.ResourceID + "/users?q=bo", ""},
		} {
			req := httptest.NewRequest(q.method, q.path, bytes.NewBufferString(q.body))
			if q.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Sec-Fetch-Mode", "cors")
			req.Header.Set("Sec-Fetch-Dest", "empty")
			req.Header.Set("X-LocalAI-Confirm", ref.ResourceID)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(409), q.method+" "+q.path+" "+w.Body.String())
			Expect(shareReply{w.Code, w.Body.Bytes()}.errType()).To(Equal("sharing_disabled"))
		}
	})
})
