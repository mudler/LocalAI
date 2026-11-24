// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
)

// The chats read the answer that the settings routes wrote. Without accounts
// there is one local owner: the routes write under it, so a chat of any name
// must read it.
func TestStudioUserSettingReadsWhatTheRoutesWrite(t *testing.T) {
	a := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background(), DataPath: t.TempDir()}}
	store, err := a.AddonUserSettingsStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if v, ok, err := a.studioUserSetting(ctx, "", "memory", "save-mode"); err != nil || ok || v != "" {
		t.Fatalf("unset = %q, %v, %v", v, ok, err)
	}
	if err := store.Set(ctx, auth.AddonLocalOwnerID, "memory", "save-mode", "auto"); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"", "anonymous", "someone"} {
		v, ok, err := a.studioUserSetting(ctx, user, "memory", "save-mode")
		if err != nil || !ok || v != "auto" {
			t.Fatalf("user %q reads %q, %v, %v", user, v, ok, err)
		}
	}
	if _, ok, _ := a.studioUserSetting(ctx, "", "memory", "other-key"); ok {
		t.Fatal("another key must not read the value")
	}
	if _, ok, _ := a.studioUserSetting(ctx, "", "other-addon", "save-mode"); ok {
		t.Fatal("another add-on must not read the value")
	}
}

func TestAddonUserSettingsStoreSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background(), DataPath: dir}}
	s, err := first.AddonUserSettingsStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(context.Background(), auth.AddonLocalOwnerID, "memory", "save-mode", "never"); err != nil {
		t.Fatal(err)
	}
	second := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background(), DataPath: dir}}
	v, ok, err := second.studioUserSetting(context.Background(), "", "memory", "save-mode")
	if err != nil || !ok || v != "never" {
		t.Fatalf("after a restart = %q, %v, %v", v, ok, err)
	}
}
