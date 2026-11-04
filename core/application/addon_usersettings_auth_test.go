//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
)

// With accounts the answer is per person, in the database that every frontend
// shares: a save through one frontend is read by the chats of another, and one
// person's choice never reaches another person.
func TestStudioUserSettingIsPerPersonAcrossFrontends(t *testing.T) {
	db, err := auth.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	newFrontend := func() *Application {
		return &Application{authDB: db, applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
	}
	a, b := newFrontend(), newFrontend()
	sa, err := a.AddonUserSettingsStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sa.Set(ctx, "u1", "memory", "save-mode", "auto"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := b.studioUserSetting(ctx, "u1", "memory", "save-mode"); err != nil || !ok || v != "auto" {
		t.Fatalf("second frontend reads %q, %v, %v", v, ok, err)
	}
	if _, ok, _ := b.studioUserSetting(ctx, "u2", "memory", "save-mode"); ok {
		t.Fatal("another person must not read it")
	}
	// The local-owner name is not a way into a named person's choice.
	if _, ok, _ := b.studioUserSetting(ctx, auth.AddonLocalOwnerID, "memory", "save-mode"); ok {
		t.Fatal("the local owner name must not read a named person's choice")
	}
}
