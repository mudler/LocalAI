// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

func runTestAddonSettingsAuthorityProviderConsumers() {
	ctx := context.Background()
	owner, err := hostsettings.Open(filepath.Join(GinkgoT().TempDir(), "policy.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "owner", Version: "1", Candidates: []hostsettings.Policy{{ToolProviders: map[string]string{"web_search": "sandbox", "pdf": "office"}}}})
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(owner.Close()).To(Succeed()) }()
	var services []*studio.Service
	for _, local := range []string{"local-a", "local-b", "worker"} {
		_, carrier := consumerSettingsCarrier(&gateway.Gateway{HostSettings: owner})
		a := &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true}, ChatsToolProviders: map[string]string{"web_search": local}}, addonGateway: &gateway.Gateway{Forward: carrier}}
		reg := builtins.NewRegistry()
		if err := reg.Register(builtins.Tool{ID: "web_search", Setting: builtins.SettingHostExec, Replaceable: true, Build: func(context.Context, builtins.Chat) (session.Tool, bool, string) { return session.Tool{}, true, "" }}); err != nil {
			Fail(fmt.Sprint(err))
		}
		st, _ := store.NewJSON("")
		svc, err := studio.New(studio.Deps{Builtins: reg, Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), ToolPins: a.studioToolPins})
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		defer func() { Expect(svc.Close(ctx)).To(Succeed()) }()
		services = append(services, svc)
	}
	check := func(want string) {
		for i, s := range services {
			rows, err := s.AdminBuiltinTools(ctx, "alice")
			if err != nil {
				Fail(fmt.Sprint(err))
			}
			found := false
			for _, r := range rows {
				if r.ID == "web_search" {
					found = true
					if r.Pinned != want {
						Fail(fmt.Sprintf("consumer %d pin %q want owner %q", i, r.Pinned, want))
					}
				}
			}
			if !found {
				Fail(fmt.Sprint("missing web_search"))
			}
		}
	}
	check("sandbox")
	snap, _ := owner.Snapshot(ctx)
	next := "other"
	if _, err = owner.Update(ctx, hostsettings.Patch{ToolProviders: map[string]*string{"web_search": &next, "pdf": nil}}, snap.Revision, "change"); err != nil {
		Fail(fmt.Sprint(err))
	}
	snap, _ = owner.Snapshot(ctx)
	if _, ok := snap.Policy.ToolProviders["pdf"]; ok || snap.Policy.ToolProviders["web_search"] != "other" {
		Fail(fmt.Sprint("skill deletion changed other key", snap.Policy))
	}
	check("other")
	for _, key := range []string{"write_file", "unknown"} {
		if _, err = owner.Update(ctx, hostsettings.Patch{ToolProviders: map[string]*string{key: nil}}, snap.Revision, "bad-"+key); err == nil {
			Fail(fmt.Sprintf("accepted protected/unknown key %s", key))
		}
	}
	Expect(owner.Close()).To(Succeed())
	for _, s := range services {
		if _, err = s.AdminBuiltinTools(ctx, "alice"); err == nil {
			Fail(fmt.Sprint("outage returned builtin/local listing"))
		}
	}
}

var _ = Describe("add-on settings authority", Label("addon-settings-authority"), func() {
	It("TestAddonSettingsAuthorityProviderConsumers", runTestAddonSettingsAuthorityProviderConsumers)
})
