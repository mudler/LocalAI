// SPDX-License-Identifier: MIT
//go:build auth

package addons_test

import (
	"context"
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
)

// newOpenShareRig is the generic sharing API wired as the host wires it, for
// people who may not use add-ons. Only bob holds the add-ons permission. The
// example kind accepts every active account but eve; gina's account is
// disabled.
func newOpenShareRig() *shareRig {
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
	for id, name := range map[string]string{"dave": "Dave", "frank": "Frank", "fiona": "Fiona", "eve": "Eve", "bob": "Bob", "gina": "Gina"} {
		status := auth.StatusActive
		if id == "gina" {
			status = auth.StatusDisabled
		}
		Expect(r.db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: name, Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: status}).Error).To(Succeed())
		Expect(r.db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: id, Permissions: auth.PermissionMap{auth.FeatureAddons: id == "bob"}}).Error).To(Succeed())
		if status != auth.StatusActive {
			continue
		}
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
	Expect(policy.RegisterKinds(r.p, &sharetest.Provider{Eligible: func(_ context.Context, _ contract.Actor, id string) (bool, error) {
		return id != "eve", nil
	}})).To(Succeed())
	svc := resources.New(resources.Options{Policy: r.p, Users: auth.NewAddonUsers(r.db)})
	gw := &gateway.Gateway{Resources: svc, Sharing: svc, Revalidate: revalidate, RevalidateAccount: func(ctx context.Context, a contract.Actor) error { return account(ctx, a, "") }}
	r.e = echo.New()
	r.e.Use(auth.Middleware(r.db, app.ApplicationConfig()))
	routes.RegisterAddonRoutesFor(r.e, gw, r.db, auth.AddonModeNamedUser)
	return r
}

func foundIDs(s shareReply) []string {
	var found gateway.UserList
	s.json(&found)
	ids := []string{}
	for _, u := range found.Users {
		ids = append(ids, u.ID)
	}
	return ids
}

var _ = Describe("generic sharing without the add-ons feature", Label("sharing"), func() {
	bg := context.Background()
	var r *shareRig
	BeforeEach(func() { r = newOpenShareRig() })

	It("lets people who may not use add-ons share a kind, find each other and be granted", func() {
		ref := r.create("dave")
		list := r.do("GET", "/api/shares/"+sharetest.Kind, "", r.browser("dave", ""))
		Expect(list.code).To(Equal(200), string(list.body))

		// The picker searches every active named account, then the kind's
		// rule: frank and fiona may not use add-ons and are found; eve is
		// refused by the kind, gina is disabled, and the owner is left out.
		users := func(q string) shareReply {
			return r.do("GET", "/api/shares/"+sharetest.Kind+"/"+ref.ResourceID+"/users?q="+q, "", r.browser("dave", ref.ResourceID))
		}
		// The picker matches a display name, from three characters; an address
		// finds nobody who has a name.
		for q, want := range map[string][]string{"fra": {"frank"}, "fio": {"fiona"}, "fr": nil, "eve": {}, "gin": {}, "dav": {}, "bob": {"bob"}, "frank@example": {}, "frank@example.test": {}} {
			got := users(q)
			if want == nil {
				Expect(got.code).To(Equal(400), q)
				continue
			}
			Expect(got.code).To(Equal(200), q+" "+string(got.body))
			Expect(foundIDs(got)).To(Equal(want), q)
		}
		var found gateway.UserList
		users("fra").json(&found)
		Expect(found.Users).To(Equal([]gateway.UserRef{{ID: "frank", DisplayName: "Frank"}}))

		set := r.do("PUT", grantsPath(ref)+"/frank", `{"role":"manage"}`, r.browser("dave", ref.ResourceID))
		Expect(set.code).To(Equal(200), string(set.body))
		Expect(grantedRoles(set)).To(Equal(map[string]string{"frank": "manage"}))
		Expect(r.p.AuthorizeRef(bg, r.actors["frank"], ref, policy.ActionWrite)).To(Succeed())
		var idx gateway.ShareIndex
		mine := r.do("GET", "/api/shares/"+sharetest.Kind, "", r.bearer("frank"))
		Expect(mine.code).To(Equal(200), string(mine.body))
		mine.json(&idx)
		Expect(idx.Shared).To(Equal([]gateway.SharedResource{{ID: ref.ResourceID, Role: "manage", Owner: "Dave"}}))

		// The kind's rule and the active-account rule both hold for a grant.
		Expect(r.do("PUT", grantsPath(ref)+"/eve", `{"role":"use"}`, r.browser("dave", ref.ResourceID)).code).To(Equal(403))
		Expect(r.do("PUT", grantsPath(ref)+"/gina", `{"role":"use"}`, r.browser("dave", ref.ResourceID)).code).To(Equal(403))
		Expect(r.do("PUT", grantsPath(ref)+"/nobody", `{"role":"use"}`, r.browser("dave", ref.ResourceID)).code).To(Equal(403))
		Expect(r.do("PUT", grantsPath(ref)+"/@everyone", `{"role":"use"}`, r.browser("dave", ref.ResourceID)).code).To(Equal(200))
		Expect(r.p.AuthorizeRef(bg, r.actors["fiona"], ref, policy.ActionRead)).To(Succeed())

		// The human-surface rules stay: an API key or a grantee cannot share.
		Expect(r.do("PUT", grantsPath(ref)+"/fiona", `{"role":"use"}`, r.bearer("dave")).code).To(Equal(403))
		Expect(r.do("PUT", grantsPath(ref)+"/fiona", `{"role":"use"}`, r.browser("frank", ref.ResourceID)).code).To(Equal(403))

		// Revoking still stops the grantee's admitted work before it answers.
		entered, cancelled, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- r.p.AdmitRef(bg, r.actors["frank"], ref, policy.ActionWrite, func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			})
		}()
		Eventually(entered).Should(BeClosed())
		rev := r.do("DELETE", grantsPath(ref)+"/frank", "", r.browser("dave", ref.ResourceID))
		Expect(rev.code).To(Equal(200), string(rev.body))
		Expect(cancelled).To(BeClosed())
		Eventually(done).Should(Receive())
	})

	It("answers an unknown resource and a resource of someone else the same for a caller without add-ons", func() {
		theirs := r.create("frank")
		ghost := policy.Ref{Kind: sharetest.Kind, ResourceID: "r0123456789abcdef0123456789abcde"}
		var first shareReply
		for _, ref := range []policy.Ref{theirs, ghost} {
			for _, q := range []struct{ method, path, body string }{
				{"GET", grantsPath(ref), ""},
				{"PUT", grantsPath(ref) + "/fiona", `{"role":"use"}`},
				{"DELETE", grantsPath(ref) + "/fiona", ""},
				{"GET", "/api/shares/" + ref.Kind + "/" + ref.ResourceID + "/users?q=fi", ""},
			} {
				got := r.do(q.method, q.path, q.body, r.browser("dave", ref.ResourceID))
				Expect(got.code).To(Equal(403), q.method+" "+q.path)
				if first.code == 0 {
					first = got
				}
				Expect(got.body).To(MatchJSON(first.body), q.method+" "+q.path)
			}
		}
	})

	It("keeps the add-on resource routes behind the add-ons feature", func() {
		for _, path := range []string{"/api/addons/resources?addonId=tables", "/api/addons/resources/r1/grants", "/api/addons/resources/r1/users?q=bo"} {
			got := r.do("GET", path, "", r.browser("dave", "r1"))
			Expect(got.code).To(Equal(403), path)
			Expect(got.errType()).To(Equal("authorization_error"), path)
		}
		got := r.do("PUT", "/api/addons/resources/r1/grants/frank", `{"role":"viewer"}`, r.browser("dave", "r1"))
		Expect(got.code).To(Equal(403))
		Expect(got.errType()).To(Equal("authorization_error"))
		// bob holds it and reaches the owner, which knows no such resource.
		got = r.do("GET", "/api/addons/resources/r1/grants", "", r.browser("bob", "r1"))
		Expect(got.code).To(Equal(403))
		Expect(got.errType()).NotTo(Equal("authorization_error"))
	})
})
