// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/onsi/gomega"
)

func runTestAddonSettingsAuthorityBootstrap() {
	By("owner resource close order", func() {
		for _, scenario := range []string{"shutdown", "context cancellation", "store open failure", "runtime startup failure", "gateway startup failure"} {
			By(scenario, func() {
				g := gomega.NewWithT(GinkgoT())
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				root := filepath.Join(GinkgoT().TempDir(), "addons")
				transport, listen, err := gateway.NewLoopbackCarrierConfig(gateway.OwnerIdentity, GinkgoT().TempDir())
				g.Expect(err).NotTo(gomega.HaveOccurred())
				cfg := &config.ApplicationConfig{Context: ctx, DataPath: filepath.Dir(root)}
				cfg.Addons = config.AddonConfig{Enabled: true, InstanceID: transport.InstanceID, OwnerID: transport.OwnerID, OwnerAddress: transport.OwnerAddress, Root: root, StorageID: "local", Transport: transport, ListenAddress: listen, OwnershipMode: auth.AddonModeLocalOwner}
				guard := addons.OwnerGuard{Config: addons.OwnerConfig{InstanceID: transport.InstanceID, OwnerID: transport.OwnerID, OwnerAddress: transport.OwnerAddress, Root: root, StorageID: "local"}}
				a := &Application{applicationConfig: cfg}
				DeferCleanup(func() { cancel(); _ = a.Shutdown() })
				type observation struct{ snapshotErr, guardErr error }
				closed := make(chan observation, 2)
				a.addonHostSettingsOnClose = func() {
					_, snapshotErr := a.addonHostSettings.Snapshot(context.Background())
					release, guardErr := guard.Acquire(context.Background())
					if release != nil {
						_ = release()
					}
					closed <- observation{snapshotErr, guardErr}
				}
				var blocker string
				switch scenario {
				case "store open failure":
					blocker = filepath.Join(root, "host-settings.json")
				case "runtime startup failure":
					blocker = filepath.Join(root, "runtime")
				case "gateway startup failure":
					transport.InstanceID = "wrong-instance"
				}
				if blocker != "" {
					g.Expect(os.MkdirAll(root, 0700)).To(gomega.Succeed())
					g.Expect(os.WriteFile(blocker, []byte("corrupt"), 0600)).To(gomega.Succeed())
				}
				err = a.startAddons()
				switch scenario {
				case "shutdown", "context cancellation":
					g.Expect(err).NotTo(gomega.HaveOccurred())
					if scenario == "context cancellation" {
						cancel()
					} else {
						g.Expect(a.Shutdown()).To(gomega.Succeed())
					}
				case "store open failure":
					g.Expect(errors.Is(err, hostsettings.ErrUnavailable)).To(gomega.BeTrue())
				case "runtime startup failure":
					var pathErr *os.PathError
					g.Expect(errors.As(err, &pathErr)).To(gomega.BeTrue())
				case "gateway startup failure":
					g.Expect(errors.Is(err, gateway.ErrForbidden)).To(gomega.BeTrue())
				}
				if scenario != "store open failure" {
					select {
					case got := <-closed:
						g.Expect(got.snapshotErr).NotTo(gomega.HaveOccurred())
						g.Expect(got.guardErr).To(gomega.MatchError(addons.ErrOwnerBusy), "owner guard must remain held when the live settings store closes")
					case <-time.After(5 * time.Second):
						Fail("host settings store was not closed")
					}
					select {
					case <-a.addonHostSettingsDone:
					case <-time.After(5 * time.Second):
						Fail("host settings closure did not finish")
					}
					_, err = a.addonHostSettings.Snapshot(context.Background())
					g.Expect(err).To(gomega.HaveOccurred())
				}
				cancel()
				g.Expect(a.Shutdown()).To(gomega.Succeed())
				g.Expect(a.Shutdown()).To(gomega.Succeed())
				g.Expect(closed).To(gomega.BeEmpty(), "close callback must run at most once")
				release, err := guard.Acquire(context.Background())
				g.Expect(err).NotTo(gomega.HaveOccurred())
				g.Expect(release()).To(gomega.Succeed())
				if blocker != "" {
					g.Expect(os.Remove(blocker)).To(gomega.Succeed())
				}
				store, err := hostsettings.Open(filepath.Join(root, "host-settings.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "test", Version: "1", Candidates: []hostsettings.Policy{{}}})
				g.Expect(err).NotTo(gomega.HaveOccurred())
				g.Expect(store.Close()).To(gomega.Succeed())
			})
		}
	})
	g := gomega.NewWithT(GinkgoT())
	ctx := context.Background()
	actor := contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
	root := filepath.Join(GinkgoT().TempDir(), "addons")
	carrierRoots := map[string]string{}
	start := func(root string, change func(*config.ApplicationConfig)) (*Application, context.CancelFunc, error) {
		cfg := &config.ApplicationConfig{DataPath: filepath.Dir(root)}
		if carrierRoots[root] == "" {
			carrierRoots[root] = GinkgoT().TempDir()
		}
		transport, listen, err := gateway.NewLoopbackCarrierConfig(gateway.OwnerIdentity, carrierRoots[root])
		if err != nil {
			return nil, nil, err
		}
		cfg.Addons = config.AddonConfig{Enabled: true, InstanceID: transport.InstanceID, OwnerID: transport.OwnerID, OwnerAddress: transport.OwnerAddress, Root: root, StorageID: "local", Transport: transport, ListenAddress: listen, OwnershipMode: auth.AddonModeLocalOwner}
		change(cfg)
		cctx, cancel := context.WithCancel(ctx)
		cfg.Context = cctx
		a := &Application{applicationConfig: cfg}
		err = a.startAddons()
		DeferCleanup(func() { cancel(); _ = a.Shutdown() })
		return a, cancel, err
	}
	a, cancel, err := start(root, func(c *config.ApplicationConfig) {
		c.ChatsEgressCeiling = "registries"
		c.ChatsEgressDefault = "off"
		c.ChatsToolProviders = map[string]string{"bash": "sandbox"}
	})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	s, err := a.AddonGateway().HostSettingsSnapshot(ctx, actor)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(s.Policy.Ceiling).To(gomega.Equal("registries"))
	g.Expect(s.Policy.ToolProviders).To(gomega.HaveKeyWithValue("bash", "sandbox"))
	on := true
	s, err = a.AddonGateway().UpdateHostSettings(ctx, actor, hostsettings.Patch{AutoUpgradeAddons: &on}, s.Revision, "bootstrap-update")
	g.Expect(err).NotTo(gomega.HaveOccurred())
	forged := actor
	forged.CredentialID = "forged"
	_, err = a.AddonGateway().UpdateHostSettings(ctx, forged, hostsettings.Patch{AutoUpgradeAddons: &on}, s.Revision, "forged")
	g.Expect(errors.Is(err, gateway.ErrForbidden)).To(gomega.BeTrue())
	cancel()
	g.Expect(a.Shutdown()).To(gomega.Succeed())
	_, err = a.AddonGateway().HostSettingsInternalSnapshot(ctx)
	g.Expect(err).To(gomega.HaveOccurred())
	b, _, err := start(root, func(*config.ApplicationConfig) {})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	again, err := b.AddonGateway().HostSettingsInternalSnapshot(ctx)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(again.Revision).To(gomega.Equal(s.Revision))
	g.Expect(again.Policy).To(gomega.Equal(s.Policy))

	fresh, _, err := start(filepath.Join(GinkgoT().TempDir(), "addons"), func(*config.ApplicationConfig) {})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	defaults, err := fresh.AddonGateway().HostSettingsInternalSnapshot(ctx)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(defaults.Policy.AllowPrivate).To(gomega.BeFalse())
	g.Expect(defaults.Policy.Ceiling).To(gomega.BeElementOf("", "off"))

	badRoot := filepath.Join(GinkgoT().TempDir(), "linked")
	g.Expect(os.Symlink(filepath.Dir(root), badRoot)).To(gomega.Succeed())
	_, _, err = start(filepath.Join(badRoot, "escape"), func(*config.ApplicationConfig) {})
	g.Expect(err).To(gomega.HaveOccurred())
	_, err = os.Stat(filepath.Join(badRoot, "escape", "host-settings.json"))
	g.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	By("dedicated owner requires selection", func() {
		g := gomega.NewWithT(GinkgoT())
		root := filepath.Join(GinkgoT().TempDir(), "addons")
		legacy := func(c *config.ApplicationConfig) { c.Addons.DedicatedOwner = true; c.ChatsEgressCeiling = "registries" }
		_, _, err := start(root, legacy)
		g.Expect(errors.Is(err, hostsettings.ErrMigrationRequired)).To(gomega.BeTrue())
		_, err = os.Stat(filepath.Join(root, "host-settings.json"))
		g.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
		selected, _, err := start(root, func(c *config.ApplicationConfig) { legacy(c); c.Addons.MigrateHostSettings = true })
		g.Expect(err).NotTo(gomega.HaveOccurred())
		snap, err := selected.AddonGateway().HostSettingsInternalSnapshot(ctx)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(snap.Policy.Ceiling).To(gomega.Equal("registries"))
	})
	By("dedicated fresh defaults and duplicate guard", func() {
		g := gomega.NewWithT(GinkgoT())
		root := filepath.Join(GinkgoT().TempDir(), "addons")
		owner, _, err := start(root, func(c *config.ApplicationConfig) { c.Addons.DedicatedOwner = true })
		g.Expect(err).NotTo(gomega.HaveOccurred())
		snap, err := owner.AddonGateway().HostSettingsInternalSnapshot(ctx)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(snap.Policy).To(gomega.Equal(hostsettings.Policy{}))
		_, _, err = start(root, func(c *config.ApplicationConfig) { c.Addons.DedicatedOwner = true })
		g.Expect(err).To(gomega.HaveOccurred())
		_, err = owner.AddonGateway().HostSettingsInternalSnapshot(ctx)
		g.Expect(err).NotTo(gomega.HaveOccurred())
	})
	By("corrupt store fails closed and releases guard", func() {
		g := gomega.NewWithT(GinkgoT())
		root := filepath.Join(GinkgoT().TempDir(), "addons")
		g.Expect(os.MkdirAll(root, 0700)).To(gomega.Succeed())
		path := filepath.Join(root, "host-settings.json")
		g.Expect(os.WriteFile(path, []byte("corrupt"), 0600)).To(gomega.Succeed())
		for i := 0; i < 2; i++ {
			_, _, err := start(root, func(*config.ApplicationConfig) {})
			g.Expect(errors.Is(err, hostsettings.ErrUnavailable)).To(gomega.BeTrue())
		}
		data, err := os.ReadFile(path)
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(string(data)).To(gomega.Equal("corrupt"))
		g.Expect(os.Remove(path)).To(gomega.Succeed())
		_, _, err = start(root, func(*config.ApplicationConfig) {})
		g.Expect(err).NotTo(gomega.HaveOccurred())
	})
	By("frontend forwards without owner store", func() {
		g := gomega.NewWithT(GinkgoT())
		root := filepath.Join(GinkgoT().TempDir(), "absent")
		front, _, err := start(root, func(c *config.ApplicationConfig) {
			c.Addons.InstanceID = "frontend"
			c.Addons.Transport.InstanceID = "frontend"
			c.Addons.Transport.Pins["frontend"] = c.Addons.Transport.Pins[gateway.OwnerIdentity]
			c.Addons.Frontend = true
			c.ChatsEgressCeiling = "registries"
			c.Addons.MigrateHostSettings = true // even a programmatic selection cannot import here
		})
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(front.addonHostSettings).To(gomega.BeNil())
		g.Expect(front.AddonGateway().HostSettings).To(gomega.BeNil())
		g.Expect(front.AddonGateway().Forward).NotTo(gomega.BeNil())
		_, err = os.Stat(root)
		g.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	})

}

var _ = Describe("add-on settings authority", Label("addon-settings-authority"), func() {
	It("TestAddonSettingsAuthorityBootstrap", runTestAddonSettingsAuthorityBootstrap)
})
