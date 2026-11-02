// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

func TestAddonSettingsAuthorityProviderConsumers(t *testing.T) {
	ctx := context.Background()
	owner, err := hostsettings.Open(filepath.Join(t.TempDir(), "policy.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "owner", Version: "1", Candidates: []hostsettings.Policy{{ToolProviders: map[string]string{"web_search": "sandbox", "pdf": "office"}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	var services []*studio.Service
	for _, local := range []string{"local-a", "local-b", "worker"} {
		_, carrier := consumerSettingsCarrier(t, &gateway.Gateway{HostSettings: owner})
		a := &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true}, ChatsToolProviders: map[string]string{"web_search": local}}, addonGateway: &gateway.Gateway{Forward: carrier}}
		reg := builtins.NewRegistry()
		if err := reg.Register(builtins.Tool{ID: "web_search", Setting: builtins.SettingHostExec, Replaceable: true, Build: func(context.Context, builtins.Chat) (session.Tool, bool, string) { return session.Tool{}, true, "" }}); err != nil {
			t.Fatal(err)
		}
		st, _ := store.NewJSON("")
		svc, err := studio.New(studio.Deps{Builtins: reg, Store: st, Provider: provider.New(), StateDir: t.TempDir(), ToolPins: a.studioToolPins})
		if err != nil {
			t.Fatal(err)
		}
		defer svc.Close(ctx)
		services = append(services, svc)
	}
	check := func(want string) {
		t.Helper()
		for i, s := range services {
			rows, err := s.AdminBuiltinTools(ctx, "alice")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, r := range rows {
				if r.ID == "web_search" {
					found = true
					if r.Pinned != want {
						t.Fatalf("consumer %d pin %q want owner %q", i, r.Pinned, want)
					}
				}
			}
			if !found {
				t.Fatal("missing web_search")
			}
		}
	}
	check("sandbox")
	snap, _ := owner.Snapshot(ctx)
	next := "other"
	if _, err = owner.Update(ctx, hostsettings.Patch{ToolProviders: map[string]*string{"web_search": &next, "pdf": nil}}, snap.Revision, "change"); err != nil {
		t.Fatal(err)
	}
	snap, _ = owner.Snapshot(ctx)
	if _, ok := snap.Policy.ToolProviders["pdf"]; ok || snap.Policy.ToolProviders["web_search"] != "other" {
		t.Fatal("skill deletion changed other key", snap.Policy)
	}
	check("other")
	for _, key := range []string{"write_file", "unknown"} {
		if _, err = owner.Update(ctx, hostsettings.Patch{ToolProviders: map[string]*string{key: nil}}, snap.Revision, "bad-"+key); err == nil {
			t.Fatalf("accepted protected/unknown key %s", key)
		}
	}
	owner.Close()
	for _, s := range services {
		if _, err = s.AdminBuiltinTools(ctx, "alice"); err == nil {
			t.Fatal("outage returned builtin/local listing")
		}
	}
}
