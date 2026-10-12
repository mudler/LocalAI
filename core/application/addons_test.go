// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = Describe("addon bootstrap", func() {
	setup := func() *Application {
		return &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
	}
	cfg := func() config.AddonConfig {
		return config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: filepath.Join(GinkgoT().TempDir(), "addons"), AdminCheck: func(context.Context, contract.Actor) error { return nil }}
	}
	It("leaves addons disabled by default without choosing an owner", func() { a := setup(); Expect(a.startAddons()).To(Succeed()); Expect(a.AddonManager()).To(BeNil()) })
	It("keeps forwarding bootstrap away from storage and rejects absent owners", func() {
		a := setup()
		c := cfg()
		c.InstanceID = "frontend"
		c.AdminCheck = nil
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		Expect(c.Root).NotTo(BeADirectory())
		Expect(a.AddonManager().Reconcile(context.Background())).To(MatchError(addons.ErrNotOwner))
		c.OwnerID = ""
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
	})
	It("locks explicit owner storage and releases it on application shutdown", func() {
		c := cfg()
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		b := setup()
		b.applicationConfig.Addons = c
		Expect(b.startAddons()).NotTo(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		Expect(b.startAddons()).To(Succeed())
		Expect(b.Shutdown()).To(Succeed())
		c.StorageID = "conflict"
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
	})
	It("reconciles interrupted intent before exposing the manager and cleans failed bootstrap", func() {
		c := cfg()
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		p := filepath.Join(c.Root, "journal.json")
		Expect(os.WriteFile(p, []byte(`{"Version":1,"Addons":{},"Operations":{"op":{"Command":{"OperationID":"op","AddonID":"sample","Action":"install"},"Status":{"OperationID":"op","AddonID":"sample","Phase":"intent"}}}}`), 0600)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		b, e := os.ReadFile(p)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"Phase":"failed"`))
		Expect(os.WriteFile(p, []byte(`{`), 0600)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
		Expect(a.AddonManager()).To(BeNil())
		Expect(os.Remove(p)).To(Succeed())
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
	})
	It("rejects shared storage and missing authoritative administration", func() {
		for _, shared := range []bool{true, false} {
			a := setup()
			c := cfg()
			c.SharedStorage = shared
			if !shared {
				c.AdminCheck = nil
			}
			a.applicationConfig.Addons = c
			Expect(a.startAddons()).NotTo(Succeed())
		}
	})
})
