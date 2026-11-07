// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The host's own kinds run on the real policy of an owner and, for the
// collections, on the real collections service with its chromem engine.
var _ = Describe("sharing connector bindings and collections", func() {
	const (
		alice = "11111111-1111-4111-8111-111111111111"
		bob   = "22222222-2222-4222-8222-222222222222"
		carol = "33333333-3333-4333-8333-333333333333"
	)
	ctx := context.Background()
	var (
		a   *Application
		p   *policy.Policy
		svc *collections.Service
		col studioCollections
	)
	BeforeEach(func() {
		requireAddonRuntime()
		a = &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		a.authDB = db
		for id, perms := range map[string]auth.PermissionMap{
			alice: {auth.FeatureConnectors: true, auth.FeatureCollections: true},
			bob:   {auth.FeatureConnectors: true, auth.FeatureCollections: true},
			// carol may use collections and not connectors.
			carol: {auth.FeatureConnectors: false, auth.FeatureCollections: true},
		} {
			Expect(db.Create(&auth.User{ID: id, Name: "user-" + id[:4], Email: id[:4] + "@example.test", Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
			Expect(db.Create(&auth.UserPermission{ID: "perm-" + id, UserID: id, Permissions: perms}).Error).To(Succeed())
		}
		a.applicationConfig.Addons = config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local",
			Root: filepath.Join(realTempDir(), "addons"), ListenAddress: "127.0.0.1:0", Transport: ownerTransport()}
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		p = a.AddonPolicy()
		Expect(p).NotTo(BeNil())

		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.EmbeddingModel = "embed"
		svc, err = collections.New(cfg, nil, collections.WithHTTPClient(embeddingClient()))
		Expect(err).NotTo(HaveOccurred())
		a.collectionsService.Store(svc)
		col = studioCollections{a: a}
		Expect(svc.CreateForUser(alice, "manuals")).To(Succeed())
		Expect(col.Add(ctx, alice, "manuals", "guide.txt", "how to reset the router")).To(Succeed())
	})

	shareCollection := func(name string) policy.Ref {
		id, err := a.EnsureCollectionShare(ctx, alice, name)
		Expect(err).NotTo(HaveOccurred())
		return policy.Ref{Kind: KindCollection, ResourceID: id}
	}
	entries := func(user, name string) []string {
		list, err := svc.ListEntriesForUser(user, name)
		Expect(err).NotTo(HaveOccurred())
		return list
	}

	It("registers both kinds: use only for a binding, viewer and editor for a collection, everyone for a collection only", func() {
		conn, ok := p.Shareable(KindConnection)
		Expect(ok).To(BeTrue())
		Expect(conn.Grantable).To(Equal([]policy.Role{policy.RoleUse}))
		Expect(conn.Everyone).To(BeFalse())
		coll, ok := p.Shareable(KindCollection)
		Expect(ok).To(BeTrue())
		Expect(coll.Grantable).To(Equal([]policy.Role{policy.RoleViewer, policy.RoleEditor}))
		Expect(coll.Everyone).To(BeTrue())

		binding, err := connectionShares{a}.Ensure(ctx, alice, "binding-1")
		Expect(err).NotTo(HaveOccurred())
		bref := policy.Ref{Kind: KindConnection, ResourceID: binding}
		Expect(p.SetGrantRef(ctx, hostActor(alice), bref, policy.Everyone, policy.RoleUse)).To(MatchError(policy.ErrForbidden), "a binding is never shared with everyone")
		Expect(p.SetGrantRef(ctx, hostActor(alice), bref, bob, policy.RoleManage)).To(MatchError(policy.ErrForbidden), "use is the only role of a binding")
		Expect(p.SetGrantRef(ctx, hostActor(alice), bref, carol, policy.RoleUse)).NotTo(Succeed(), "a grant needs the connectors feature")
		Expect(p.SetGrantRef(ctx, hostActor(alice), bref, bob, policy.RoleUse)).To(Succeed())

		cref := shareCollection("manuals")
		Expect(p.SetGrantRef(ctx, hostActor(alice), cref, policy.Everyone, policy.RoleViewer)).To(Succeed(), "a collection may be shared with everyone")
		Expect(p.SetGrantRef(ctx, hostActor(alice), cref, bob, policy.RoleUse)).To(MatchError(policy.ErrForbidden), "use is not a role of a collection")
	})

	It("lets a viewer search and read a shared collection but not write it, and an editor write it", func() {
		ref := shareCollection("manuals")
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleViewer)).To(Succeed())

		names, err := col.List(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"manuals"}), "the shared collection is found by its name")
		hits, err := col.Search(ctx, bob, "manuals", "router", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).NotTo(BeEmpty())
		shared := a.SharedCollections(ctx, bob, nil)
		Expect(shared).To(HaveLen(1))
		Expect(shared[0].Role).To(Equal("viewer"))
		Expect(shared[0].OwnerName).NotTo(BeEmpty())

		Expect(col.Add(ctx, bob, "manuals", "mine.txt", "a note")).To(MatchError(studio.ErrNoCollection))
		err = a.WithCollection(ctx, bob, "manuals", policy.ActionWrite, func(context.Context, string, string) error { return nil })
		Expect(err).To(MatchError(ErrCollectionRole))
		Expect(entries(alice, "manuals")).To(HaveLen(1))
		err = a.WithCollection(ctx, bob, "manuals", policy.ActionSchema, func(context.Context, string, string) error { return nil })
		Expect(err).To(MatchError(ErrCollectionRole), "sources, reset and sharing stay the owner's")

		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleEditor)).To(Succeed())
		Expect(col.Add(ctx, bob, "manuals", "mine.txt", "a note")).To(Succeed())
		Expect(entries(alice, "manuals")).To(HaveLen(2), "the entry is stored in the owner's collection, not copied")
		Expect(col.Remove(ctx, bob, "manuals", "mine.txt")).To(Succeed())
		Expect(entries(alice, "manuals")).To(HaveLen(1))
		_, err = svc.ListForUser(bob)
		Expect(err).NotTo(HaveOccurred())
		mine, _ := svc.ListForUser(bob)
		Expect(mine).To(BeEmpty(), "nothing was made under the grantee")
	})

	It("keeps the sources of a shared collection the owner's, whatever the role", func() {
		ref := shareCollection("manuals")
		for _, role := range []policy.Role{policy.RoleViewer, policy.RoleEditor} {
			Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, role)).To(Succeed())
			_, err := a.CollectionSources(ctx, bob, "manuals")
			Expect(err).To(MatchError(ErrCollectionRole), "role %s", role)
		}
		// What a viewer and an editor may do is unchanged.
		hits, err := col.Search(ctx, bob, "manuals", "router", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).NotTo(BeEmpty())
		_, err = a.CollectionSources(ctx, alice, "manuals")
		Expect(err).NotTo(HaveOccurred(), "the owner lists the sources")
	})

	It("stops a download of an entry file when the grant is revoked before the revoke answers", func() {
		ref := shareCollection("manuals")
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleViewer)).To(Succeed())
		started, stopped := make(chan string), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- a.ServeCollectionEntryFile(ctx, bob, "manuals", "guide.txt", func(rctx context.Context, path string) error {
				started <- path
				<-rctx.Done()
				close(stopped)
				return rctx.Err()
			})
		}()
		var path string
		Eventually(started).Should(Receive(&path))
		Expect(path).NotTo(BeEmpty())
		Expect(p.RevokeRef(ctx, hostActor(alice), ref, bob)).To(Succeed())
		Expect(stopped).To(BeClosed(), "the revoke answered only after the download stopped")
		Eventually(done).Should(Receive(HaveOccurred()))
		err := a.ServeCollectionEntryFile(ctx, bob, "manuals", "guide.txt", func(context.Context, string) error {
			Fail("served after the revoke")
			return nil
		})
		Expect(err).To(MatchError(ErrCollectionNotFound))
	})

	It("gives the grantee's own collection the name and the shared one its unambiguous name", func() {
		ref := shareCollection("manuals")
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleViewer)).To(Succeed())
		Expect(svc.CreateForUser(bob, "manuals")).To(Succeed())
		Expect(col.Add(ctx, bob, "manuals", "bob.txt", "bob's own notes")).To(Succeed())

		names, err := col.List(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(ConsistOf("manuals", SharedCollectionPrefix+ref.ResourceID))

		var owner string
		Expect(a.WithCollection(ctx, bob, "manuals", policy.ActionRead, func(_ context.Context, o, _ string) error { owner = o; return nil })).To(Succeed())
		Expect(owner).To(Equal(bob), "the grantee's own collection wins the name")
		Expect(a.WithCollection(ctx, bob, SharedCollectionPrefix+ref.ResourceID, policy.ActionRead, func(_ context.Context, o, _ string) error { owner = o; return nil })).To(Succeed())
		Expect(owner).To(Equal(alice))
		hits, err := col.Search(ctx, bob, SharedCollectionPrefix+ref.ResourceID, "router", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).NotTo(BeEmpty())
	})

	It("cancels the work a lowering or a revocation takes away, and then answers like an unknown collection", func() {
		ref := shareCollection("manuals")
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleEditor)).To(Succeed())

		blocked := func(action policy.Action) (started, cancelled chan struct{}, done chan error) {
			started, cancelled, done = make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				done <- a.WithCollection(ctx, bob, "manuals", action, func(rctx context.Context, _, _ string) error {
					close(started)
					<-rctx.Done()
					close(cancelled)
					return rctx.Err()
				})
			}()
			Eventually(started).Should(BeClosed())
			return
		}
		_, cancelled, done := blocked(policy.ActionWrite)
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleViewer)).To(Succeed())
		Expect(cancelled).To(BeClosed(), "the lowering answered only after the write stopped")
		Eventually(done).Should(Receive(HaveOccurred()))

		_, cancelled, done = blocked(policy.ActionRead)
		Expect(p.RevokeRef(ctx, hostActor(alice), ref, bob)).To(Succeed())
		Expect(cancelled).To(BeClosed())
		Eventually(done).Should(Receive(HaveOccurred()))

		run := func(context.Context, string, string) error { return nil }
		revoked := a.WithCollection(ctx, bob, "manuals", policy.ActionRead, run)
		unknown := a.WithCollection(ctx, bob, "no-such-collection", policy.ActionRead, run)
		Expect(revoked).To(MatchError(ErrCollectionNotFound))
		Expect(unknown).To(MatchError(ErrCollectionNotFound))
		Expect(revoked.Error()).To(Equal(unknown.Error()))
		_, err := col.Search(ctx, bob, "manuals", "router", 3)
		Expect(err).To(MatchError(studio.ErrNoCollection))
		names, err := col.List(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(BeEmpty())
	})

	It("cancels a call through a shared binding on revocation, then refuses it like an unknown binding", func() {
		shares := connectionShares{a}
		id, err := shares.Ensure(ctx, alice, "binding-1")
		Expect(err).NotTo(HaveOccurred())
		ref := policy.Ref{Kind: KindConnection, ResourceID: id}
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleUse)).To(Succeed())
		list, err := shares.Shared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].BindingID).To(Equal("binding-1"))
		st, ok := shares.ShareOf(ctx, alice, "binding-1")
		Expect(ok).To(BeTrue())
		Expect(st.SharedWith).To(Equal(1))

		started, cancelled, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- shares.Admit(ctx, bob, alice, "binding-1", func(rctx context.Context) error {
				close(started)
				<-rctx.Done()
				close(cancelled)
				return rctx.Err()
			})
		}()
		Eventually(started).Should(BeClosed())
		Expect(p.RevokeRef(ctx, hostActor(alice), ref, bob)).To(Succeed())
		Expect(cancelled).To(BeClosed())
		Eventually(done).Should(Receive(HaveOccurred()))

		ran := false
		err = shares.Admit(ctx, bob, alice, "binding-1", func(context.Context) error { ran = true; return nil })
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		Expect(ran).To(BeFalse())
		Expect(shares.Admit(ctx, bob, alice, "no-such-binding", func(context.Context) error { return nil })).To(MatchError(connectors.ErrNoCredential))

		hold := sharedHold(func(ctx context.Context, run func(context.Context) error) error {
			return shares.Admit(ctx, bob, alice, "binding-1", run)
		})
		var refusal *gateway.CredentialRefusal
		Expect(errors.As(hold(ctx, func(context.Context) error { return nil }), &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(gateway.CodeNoCredential), "the refusal of a binding the person does not have")

		Expect(shares.Forget(ctx, alice, "binding-1")).To(Succeed())
		_, ok = shares.ShareOf(ctx, alice, "binding-1")
		Expect(ok).To(BeFalse())
	})

	It("offers, for each collection and connection an agent names, only the roles its kind grants, and every offer is accepted", func() {
		agentStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		chatStore, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		chats, err := studio.New(studio.Deps{Store: chatStore, Agents: agentStore, Provider: provider.New(), Harness: studiotest.NewGateHarness(),
			StateDir: GinkgoT().TempDir(), ReferenceShares: a.studioReferenceShares})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(chats.Close(ctx)).To(Succeed()) })
		bindings, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		conns, err := studio.NewConnectors(chats, studio.ConnectorOptions{Store: bindings, Transport: tokenTransport{}, Shares: connectionShares{a}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		chats.SetConnectors(conns)
		a.studioService = chats
		DeferCleanup(func() { a.studioService = nil })
		b, err := conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "tracker", Name: "Work", Token: "token-work", Resources: []string{"acme/widgets"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(agentStore.Put(ctx, agents.Agent{ID: "ag1", Principal: alice, Name: "Reader", Collections: []string{"manuals", "absent"},
			Connections: connsets.Sets{"tracker": {b.ID}}, Created: time.Now()})).To(Succeed())

		refs, err := chats.AgentReferences(ctx, alice, "ag1")
		Expect(err).NotTo(HaveOccurred())
		offered := map[string][]string{}
		for _, r := range refs {
			if r.Share == nil {
				continue
			}
			offered[r.Kind+":"+r.ID] = r.Share.Options
			for _, choice := range r.Share.Options {
				role := r.Share.Roles[choice]
				Expect(role).NotTo(BeEmpty(), "%s %s offers %s without a role", r.Kind, r.ID, choice)
				ref := policy.Ref{Kind: r.Share.Kind, ResourceID: r.Share.ResourceID}
				Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.Role(role))).To(Succeed(), "%s %s: the choice %s grants %s", r.Kind, r.ID, choice, role)
				Expect(p.RevokeRef(ctx, hostActor(alice), ref, bob)).To(Succeed())
			}
		}
		Expect(offered).To(Equal(map[string][]string{"collection:manuals": {"view"}, "connection:" + b.ID: {"use"}}),
			"a collection the owner does not have is not offered")
		// The references made the records and granted nothing.
		Expect(connectionShares{a}.Shared(ctx, bob)).To(BeEmpty())
		Expect(col.List(ctx, bob)).To(BeEmpty())
		other, err := chats.AgentReferences(ctx, bob, "ag1")
		Expect(err).To(HaveOccurred(), "only the owner reads the references")
		Expect(other).To(BeNil())
	})

	It("shares nothing in local-owner mode", func() {
		local := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		local.applicationConfig.Addons = config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local",
			OwnershipMode: auth.AddonModeLocalOwner, Root: filepath.Join(realTempDir(), "addons"), ListenAddress: "127.0.0.1:0", Transport: ownerTransport()}
		Expect(local.startAddons()).To(Succeed())
		DeferCleanup(local.Shutdown)
		Expect(local.CollectionSharing()).To(BeFalse())
		Expect(connectionShares{local}.Enabled()).To(BeFalse())
		_, err := local.EnsureCollectionShare(ctx, alice, "manuals")
		Expect(err).To(MatchError(ErrSharingOff))
		Expect(strings.Contains(err.Error(), "not available")).To(BeTrue())
	})
})
