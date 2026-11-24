// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/gateway/carriertest"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/policy/sharetest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// otherKind registers one more kind, after the owner started.
type otherKind struct{}

func (otherKind) RegisterKinds(r policy.KindRegistrar) error {
	return r.RegisterKind("example:other", policy.KindSpec{
		Grantable: []policy.Role{policy.RoleUse},
		Roles:     func(policy.Action) []policy.Role { return nil },
		Backing:   func(context.Context, policy.Ref) (bool, error) { return true, nil },
	})
}

// goneKind is a kind whose Backing says that every resource is gone.
type goneKind struct{}

func (goneKind) RegisterKinds(r policy.KindRegistrar) error {
	return r.RegisterKind("example:gone", policy.KindSpec{
		Grantable:       []policy.Role{policy.RoleUse},
		Roles:           func(policy.Action) []policy.Role { return nil },
		CreateAuthority: func(context.Context, contract.Actor) error { return nil },
		Backing:         func(context.Context, policy.Ref) (bool, error) { return false, nil },
	})
}

type failingKind struct{}

func (failingKind) RegisterKinds(policy.KindRegistrar) error { return errors.New("kind refused") }

var _ = Describe("share kind registration on the owner", func() {
	owner := func() (*Application, config.AddonConfig) {
		a := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		a.authDB = db
		c := config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: filepath.Join(realTempDir(), "addons"), ListenAddress: "127.0.0.1:0", Transport: ownerTransport()}
		return a, c
	}

	It("registers the kinds given before the start when the policy opens, and later ones at once", func() {
		requireAddonRuntime()
		a, c := owner()
		Expect(a.RegisterShareKinds(&sharetest.Provider{})).To(Succeed())
		Expect(a.AddonPolicy()).To(BeNil())
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)

		rules, ok := a.AddonPolicy().Shareable(sharetest.Kind)
		Expect(ok).To(BeTrue())
		Expect(rules.Grantable).To(Equal([]policy.Role{policy.RoleUse, policy.RoleManage}))
		Expect(a.AddonGateway().Sharing).NotTo(BeNil())

		Expect(a.RegisterShareKinds(otherKind{})).To(Succeed())
		_, ok = a.AddonPolicy().Shareable("example:other")
		Expect(ok).To(BeTrue())
		Expect(a.RegisterShareKinds(&sharetest.Provider{})).NotTo(Succeed(), "a kind registers once")
		Expect(a.RegisterShareKinds(nil)).NotTo(Succeed())
	})

	It("serves the kinds to an account without the add-ons feature and keeps the add-ons check for add-ons", func() {
		requireAddonRuntime()
		a, c := owner()
		Expect(a.RegisterShareKinds(&sharetest.Provider{Eligible: func(context.Context, contract.Actor, string) (bool, error) { return true, nil }})).To(Succeed())
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		actors := map[string]contract.Actor{}
		for _, id := range []string{"dave", "frank"} {
			Expect(a.authDB.Create(&auth.User{ID: id, Name: id, Email: id + "@example.test", Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
			Expect(a.authDB.Create(&auth.UserPermission{ID: "perm-" + id, UserID: id, Permissions: auth.PermissionMap{auth.FeatureAddons: false}}).Error).To(Succeed())
			_, key, err := auth.CreateAPIKey(a.authDB, id, "test", auth.RoleUser, "secret", nil)
			Expect(err).NotTo(HaveOccurred())
			actors[id] = contract.Actor{UserID: id, CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}
		}
		ctx := context.Background()
		p := a.AddonPolicy()
		ref, err := p.CreateRef(ctx, actors["dave"], sharetest.Kind)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.SetGrantRef(ctx, actors["dave"], ref, "frank", policy.RoleUse)).To(Succeed())
		idx, err := a.AddonGateway().Shares(ctx, actors["frank"], sharetest.Kind)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx.Shared).To(HaveLen(1))
		_, err = p.Register(ctx, actors["dave"], "tables")
		Expect(err).To(MatchError(policy.ErrForbidden))
	})

	It("drops at startup the share records whose resource is gone and keeps the others", func() {
		requireAddonRuntime()
		a, c := owner()
		// A store left by an earlier run: one record of a kind whose
		// resources are gone, one of a kind whose resources exist.
		dir := filepath.Join(c.Root, "policy")
		store, err := policy.OpenStore(dir, policy.ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		allow := func(context.Context, contract.Actor, string) error { return nil }
		earlier := policy.New(store, policy.Options{Account: allow})
		Expect(policy.RegisterKinds(earlier, goneKind{}, &sharetest.Provider{})).To(Succeed())
		dave := contract.Actor{UserID: "dave", CredentialID: "key:dave", Mode: auth.AddonModeNamedUser}
		gone, err := earlier.CreateRef(context.Background(), dave, "example:gone")
		Expect(err).NotTo(HaveOccurred())
		kept, err := earlier.CreateRef(context.Background(), dave, sharetest.Kind)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Close()).To(Succeed())

		Expect(a.RegisterShareKinds(goneKind{}, &sharetest.Provider{})).To(Succeed())
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		p := a.AddonPolicy()
		_, ok := p.OwnerOfRef(gone)
		Expect(ok).To(BeFalse(), "the record of a resource that is gone is dropped")
		owner, ok := p.OwnerOfRef(kept)
		Expect(ok).To(BeTrue())
		Expect(owner).To(Equal("dave"))
	})

	It("does not start the owner when a provider fails", func() {
		requireAddonRuntime()
		a, c := owner()
		Expect(a.RegisterShareKinds(failingKind{})).To(Succeed())
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(MatchError(ContainSubstring("kind refused")))
		DeferCleanup(a.Shutdown)
		Expect(a.AddonPolicy()).To(BeNil())
	})

	It("gives the owner the leases of its policy and a frontend the owner's policy through the carrier", func() {
		requireAddonRuntime()
		a, c := owner()
		Expect(a.RegisterShareKinds(&sharetest.Provider{})).To(Succeed())
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		Expect(a.AddonGateway().ShareAuthority).NotTo(BeNil())
		Expect(a.addonSharingForward()).To(BeNil(), "the owner uses its own policy")

		net, err := carriertest.New(func() *gateway.Gateway {
			return &gateway.Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(net.Close)
		front := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		// A frontend keeps the providers it is given and never asks them.
		Expect(front.RegisterShareKinds(&sharetest.Provider{})).To(Succeed())
		Expect(front.shareKinds).To(HaveLen(1))
		Expect(front.addonSharingForward()).To(BeNil(), "no carrier, no owner")
		front.addonGateway = net.Frontend(carriertest.FrontA)
		Expect(front.addonSharingForward()).To(Equal(policy.Forwarded{Gateway: front.addonGateway}))
		front.applicationConfig.Addons.OwnershipMode = auth.AddonModeLocalOwner
		Expect(front.addonSharingForward()).To(BeNil(), "local-owner mode shares nothing")
	})
})
