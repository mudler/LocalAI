// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TestAddonSettingsAuthorityUpgradePolicy", Label("addon-settings-authority"), func() {
	var a *Application
	var owner *hostsettings.Store
	ctx := context.Background()
	BeforeEach(func() {
		var err error
		owner, err = hostsettings.Open(filepath.Join(GinkgoT().TempDir(), "policy.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "test", Version: "1", Candidates: []hostsettings.Policy{{}}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(owner.Close)
		a = &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true}, AutoUpgradeAddons: true}, addonGateway: &gateway.Gateway{HostSettings: owner}}
	})
	check := func(want bool, wantErr bool) {
		GinkgoHelper()
		enabled, err := a.addonUpgradePolicy(ctx)
		Expect(enabled).To(Equal(want))
		if wantErr {
			Expect(err).To(HaveOccurred())
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	}
	It("uses fresh owner defaults instead of a stale local enable", func() { check(false, false) })
	It("observes owner policy changes despite a stale local disable", func() {
		s, err := owner.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		on := true
		_, err = owner.Update(ctx, hostsettings.Patch{AutoUpgradeAddons: &on}, s.Revision, "enable")
		Expect(err).NotTo(HaveOccurred())
		a.applicationConfig.AutoUpgradeAddons = false
		check(true, false)
	})
	It("fails closed during owner outage despite a local enable", func() {
		Expect(owner.Close()).To(Succeed())
		check(false, true)
	})
	It("preserves no-addons compatibility for enabled and disabled upgrades", func() {
		Expect(owner.Close()).To(Succeed())
		a.applicationConfig.Addons.Enabled = false
		check(true, false)
		a.applicationConfig.AutoUpgradeAddons = false
		check(false, false)
	})
})
