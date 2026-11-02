// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/onsi/gomega"
)

func TestAddonSettingsAuthorityBootstrap(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := context.Background()
	actor := contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
	root := filepath.Join(t.TempDir(), "addons")
	carrierRoots := map[string]string{}
	start := func(root string, change func(*config.ApplicationConfig)) (*Application, context.CancelFunc, error) {
		cfg := &config.ApplicationConfig{DataPath: filepath.Dir(root)}
		if carrierRoots[root] == "" {
			carrierRoots[root] = t.TempDir()
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
		t.Cleanup(func() { cancel(); _ = a.Shutdown() })
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

	fresh, _, err := start(filepath.Join(t.TempDir(), "addons"), func(*config.ApplicationConfig) {})
	g.Expect(err).NotTo(gomega.HaveOccurred())
	defaults, err := fresh.AddonGateway().HostSettingsInternalSnapshot(ctx)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(defaults.Policy.AllowPrivate).To(gomega.BeFalse())
	g.Expect(defaults.Policy.Ceiling).To(gomega.BeElementOf("", "off"))

	badRoot := filepath.Join(t.TempDir(), "linked")
	g.Expect(os.Symlink(filepath.Dir(root), badRoot)).To(gomega.Succeed())
	_, _, err = start(filepath.Join(badRoot, "escape"), func(*config.ApplicationConfig) {})
	g.Expect(err).To(gomega.HaveOccurred())
	_, err = os.Stat(filepath.Join(badRoot, "escape", "host-settings.json"))
	g.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	t.Run("dedicated owner requires selection", func(t *testing.T) {
		g := gomega.NewWithT(t)
		root := filepath.Join(t.TempDir(), "addons")
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
	t.Run("dedicated fresh defaults and duplicate guard", func(t *testing.T) {
		g := gomega.NewWithT(t)
		root := filepath.Join(t.TempDir(), "addons")
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
	t.Run("corrupt store fails closed and releases guard", func(t *testing.T) {
		g := gomega.NewWithT(t)
		root := filepath.Join(t.TempDir(), "addons")
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
	t.Run("frontend forwards without owner store", func(t *testing.T) {
		g := gomega.NewWithT(t)
		root := filepath.Join(t.TempDir(), "absent")
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
