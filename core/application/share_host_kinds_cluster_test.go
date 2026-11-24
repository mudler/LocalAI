// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/gateway/carriertest"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The host's own kinds on a cluster: the add-on owner holds the policy, and
// two frontends that hold none reach it through the real carrier. The
// accounts and the collections are one store, as in a cluster's database.
var _ = Describe("sharing connector bindings and collections across frontends", func() {
	const (
		alice = "11111111-1111-4111-8111-111111111111"
		bob   = "22222222-2222-4222-8222-222222222222"
	)
	ctx := context.Background()
	var (
		owner, frontA, frontB *Application
		p                     *policy.Policy
		net                   *carriertest.Fixture
		svc                   *collections.Service
	)
	BeforeEach(func() {
		requireAddonRuntime()
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		for _, id := range []string{alice, bob} {
			Expect(db.Create(&auth.User{ID: id, Name: "user-" + id[:4], Email: id[:4] + "@example.test", Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
			Expect(db.Create(&auth.UserPermission{ID: "perm-" + id, UserID: id, Permissions: auth.PermissionMap{auth.FeatureConnectors: true, auth.FeatureCollections: true}}).Error).To(Succeed())
		}
		owner = &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}, authDB: db}
		owner.applicationConfig.Addons = config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local",
			Root: filepath.Join(realTempDir(), "addons"), ListenAddress: "127.0.0.1:0", Transport: ownerTransport()}
		Expect(owner.startAddons()).To(Succeed())
		DeferCleanup(owner.Shutdown)
		p = owner.AddonPolicy()
		Expect(p).NotTo(BeNil())
		// A short watch keeps a revocation and the close of the owner quick.
		host, ok := owner.AddonGateway().ShareAuthority.(*policy.ShareHost)
		Expect(ok).To(BeTrue())
		host.Wait = 300 * time.Millisecond
		// The owner's own gateway, with its account checks, serves the
		// frontends.
		net, err = carriertest.New(owner.AddonGateway)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(net.Close)

		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.EmbeddingModel = "embed"
		svc, err = collections.New(cfg, nil, collections.WithHTTPClient(embeddingClient()))
		Expect(err).NotTo(HaveOccurred())
		owner.collectionsService.Store(svc)
		frontend := func(id string) *Application {
			f := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}, authDB: db, addonGateway: net.Frontend(id)}
			f.collectionsService.Store(svc)
			Expect(f.AddonPolicy()).To(BeNil(), "a frontend holds no policy")
			return f
		}
		frontA, frontB = frontend(carriertest.FrontA), frontend(carriertest.FrontB)

		Expect(svc.CreateForUser(alice, "manuals")).To(Succeed())
		Expect(studioCollections{a: owner}.Add(ctx, alice, "manuals", "guide.txt", "how to reset the router")).To(Succeed())
	})

	// blocked runs a piece of work under a share until it is cancelled.
	type blockedRun struct {
		started, cancelled chan struct{}
		done               chan error
	}
	blockedRunOf := func(start func(run func(context.Context) error) error) blockedRun {
		b := blockedRun{started: make(chan struct{}), cancelled: make(chan struct{}), done: make(chan error, 1)}
		go func() {
			defer GinkgoRecover()
			b.done <- start(func(rctx context.Context) error {
				close(b.started)
				<-rctx.Done()
				close(b.cancelled)
				return rctx.Err()
			})
		}()
		Eventually(b.started, 5*time.Second).Should(BeClosed())
		return b
	}

	It("shares a collection on one frontend, lets the grantee use it on the other, and a revoke on the first stops the use", func() {
		Expect(frontA.CollectionSharing()).To(BeTrue(), "a frontend shares through the owner")
		id, err := frontA.EnsureCollectionShare(ctx, alice, "manuals")
		Expect(err).NotTo(HaveOccurred())
		again, err := frontB.EnsureCollectionShare(ctx, alice, "manuals")
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(id), "one record for each name, whichever frontend asks")
		ref := policy.Ref{Kind: KindCollection, ResourceID: id}
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleEditor)).To(Succeed())
		Expect(frontA.CollectionShares(ctx, alice, []string{"manuals"})).To(Equal(map[string]CollectionShare{"manuals": {ID: id, SharedWith: 1}}))

		colB := studioCollections{a: frontB}
		names, err := colB.List(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"manuals"}))
		shared := frontB.SharedCollections(ctx, bob, nil)
		Expect(shared).To(HaveLen(1))
		Expect(shared[0].Role).To(Equal("editor"))
		Expect(shared[0].OwnerName).To(Equal("user-" + alice[:4]))
		hits, err := colB.Search(ctx, bob, "manuals", "router", 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).NotTo(BeEmpty())
		Expect(colB.Add(ctx, bob, "manuals", "bob.txt", "a note")).To(Succeed())
		list, err := svc.ListEntriesForUser(alice, "manuals")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2), "the entry is in the owner's collection")

		b := blockedRunOf(func(run func(context.Context) error) error {
			return frontB.WithCollection(ctx, bob, "manuals", policy.ActionWrite, func(rctx context.Context, _, _ string) error { return run(rctx) })
		})
		Expect(frontA.hostSharing().RevokeRef(ctx, hostActor(alice), ref, bob)).To(Succeed())
		Expect(b.cancelled).To(BeClosed(), "the revoke answered after the write on the other frontend stopped")
		Eventually(b.done, 5*time.Second).Should(Receive(HaveOccurred()))

		ran := false
		err = frontB.WithCollection(ctx, bob, "manuals", policy.ActionRead, func(context.Context, string, string) error { ran = true; return nil })
		Expect(err).To(MatchError(ErrCollectionNotFound))
		Expect(ran).To(BeFalse())
		names, err = colB.List(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(BeEmpty())
	})

	It("stops a lowering's work on the other frontend and keeps what the new role allows", func() {
		id, err := frontA.EnsureCollectionShare(ctx, alice, "manuals")
		Expect(err).NotTo(HaveOccurred())
		ref := policy.Ref{Kind: KindCollection, ResourceID: id}
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleEditor)).To(Succeed())

		write := blockedRunOf(func(run func(context.Context) error) error {
			return frontB.WithCollection(ctx, bob, "manuals", policy.ActionWrite, func(rctx context.Context, _, _ string) error { return run(rctx) })
		})
		read := blockedRunOf(func(run func(context.Context) error) error {
			return frontB.WithCollection(ctx, bob, "manuals", policy.ActionRead, func(rctx context.Context, _, _ string) error { return run(rctx) })
		})
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleViewer)).To(Succeed())
		Expect(write.cancelled).To(BeClosed(), "the lowering answered after the write stopped")
		Consistently(read.cancelled, 500*time.Millisecond).ShouldNot(BeClosed(), "a viewer still reads")
		Expect(studioCollections{a: frontB}.Add(ctx, bob, "manuals", "late.txt", "x")).To(MatchError(studio.ErrNoCollection))
		Expect(frontA.hostSharing().RevokeRef(ctx, hostActor(alice), ref, bob)).To(Succeed())
		Expect(read.cancelled).To(BeClosed())
	})

	It("shares a binding on one frontend, lists it on the other, and a revoke there stops the owner's use of the credential", func() {
		sharesA, sharesB := connectionShares{frontA}, connectionShares{frontB}
		Expect(sharesB.Enabled()).To(BeTrue())
		id, err := sharesA.Ensure(ctx, alice, "binding-1")
		Expect(err).NotTo(HaveOccurred())
		ref := policy.Ref{Kind: KindConnection, ResourceID: id}
		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleUse)).To(Succeed())

		list, err := sharesB.Shared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(Equal([]studio.SharedBinding{{BindingID: "binding-1", OwnerName: "user-" + alice[:4]}}))
		st, ok := sharesB.ShareOf(ctx, alice, "binding-1")
		Expect(ok).To(BeTrue())
		Expect(st).To(Equal(studio.ShareState{ResourceID: id, SharedWith: 1}))
		_, ok = sharesB.ShareOf(ctx, bob, "binding-1")
		Expect(ok).To(BeFalse(), "a grantee has no share of their own of the binding")

		// The credential is opened and used only on the owner, which holds
		// the tokens; its admission there is the owner's policy.
		b := blockedRunOf(func(run func(context.Context) error) error {
			return connectionShares{owner}.Admit(ctx, bob, alice, "binding-1", run)
		})
		Expect(sharesB.a.hostSharing().RevokeRef(ctx, hostActor(alice), ref, bob)).To(Succeed())
		Expect(b.cancelled).To(BeClosed(), "the revoke on a frontend answered after the call on the owner stopped")
		Eventually(b.done, 5*time.Second).Should(Receive(HaveOccurred()))
		Expect(connectionShares{owner}.Admit(ctx, bob, alice, "binding-1", func(context.Context) error {
			Fail("ran after the revoke")
			return nil
		})).To(MatchError(connectors.ErrNoCredential))
		list, err = sharesB.Shared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())

		Expect(p.SetGrantRef(ctx, hostActor(alice), ref, bob, policy.RoleUse)).To(Succeed())
		Expect(sharesA.Forget(ctx, alice, "binding-1")).To(Succeed())
		_, ok = sharesB.ShareOf(ctx, alice, "binding-1")
		Expect(ok).To(BeFalse(), "a deleted binding's record is gone on every frontend")
		list, err = sharesB.Shared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("gives a grantee on a frontend the name and the owner of a shared resource and nothing that is a credential", func() {
		id, err := connectionShares{frontA}.Ensure(ctx, alice, "binding-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.SetGrantRef(ctx, hostActor(alice), policy.Ref{Kind: KindConnection, ResourceID: id}, bob, policy.RoleUse)).To(Succeed())
		views, err := frontB.AddonGateway().ShareViews(ctx, hostActor(bob), KindConnection)
		Expect(err).NotTo(HaveOccurred())
		raw, err := json.Marshal(views)
		Expect(err).NotTo(HaveOccurred())
		var wire map[string]any
		Expect(json.Unmarshal(raw, &wire)).To(Succeed())
		Expect(wire["views"]).To(HaveLen(1))
		Expect(wire["views"].([]any)[0]).To(HaveKeyWithValue("local", "binding-1"))
		for k := range wire["views"].([]any)[0].(map[string]any) {
			Expect(k).To(BeElementOf("id", "role", "ownerId", "local"), "the grantee's answer carries no other field")
		}
		list, err := connectionShares{frontB}.Shared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		raw, err = json.Marshal(list)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(MatchRegexp(`(?i)token|secret|credential`))
	})

	It("fails closed on a frontend when the owner is gone, and stops the work running there", func() {
		id, err := frontA.EnsureCollectionShare(ctx, alice, "manuals")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.SetGrantRef(ctx, hostActor(alice), policy.Ref{Kind: KindCollection, ResourceID: id}, bob, policy.RoleViewer)).To(Succeed())
		bid, err := connectionShares{frontA}.Ensure(ctx, alice, "binding-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.SetGrantRef(ctx, hostActor(alice), policy.Ref{Kind: KindConnection, ResourceID: bid}, bob, policy.RoleUse)).To(Succeed())

		b := blockedRunOf(func(run func(context.Context) error) error {
			return frontB.WithCollection(ctx, bob, "manuals", policy.ActionRead, func(rctx context.Context, _, _ string) error { return run(rctx) })
		})
		net.Server.CloseClientConnections()
		net.Close()
		Eventually(b.cancelled, 10*time.Second).Should(BeClosed(), "a frontend that cannot reach the owner stops the work")

		Expect(frontB.SharedCollections(ctx, bob, nil)).To(BeEmpty())
		ran := false
		err = frontB.WithCollection(ctx, bob, "manuals", policy.ActionRead, func(context.Context, string, string) error { ran = true; return nil })
		Expect(err).To(HaveOccurred())
		Expect(ran).To(BeFalse())
		_, err = frontB.EnsureCollectionShare(ctx, alice, "manuals")
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		_, err = connectionShares{frontB}.Shared(ctx, bob)
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		_, ok := connectionShares{frontB}.ShareOf(ctx, alice, "binding-1")
		Expect(ok).To(BeFalse())
		names, err := studioCollections{a: frontB}.List(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"manuals"}), "a person's own collections do not need the owner")
	})
})
