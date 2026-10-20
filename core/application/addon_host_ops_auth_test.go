// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
)

func TestTheFeatureCheckFollowsTheAccountPermissions(t *testing.T) {
	db, err := auth.InitDB(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id, role, status string) {
		if err := db.Create(&auth.User{ID: id, Email: id + "@example.com", Role: role, Status: status}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("admin", auth.RoleAdmin, auth.StatusActive)
	mk("plain", auth.RoleUser, auth.StatusActive)
	mk("granted", auth.RoleUser, auth.StatusActive)
	mk("gone", auth.RoleAdmin, "disabled")
	if err := auth.UpdateUserPermissions(db, "granted", auth.PermissionMap{"fine_tuning": true}); err != nil {
		t.Fatal(err)
	}
	if err := auth.UpdateUserPermissions(db, "plain", auth.PermissionMap{"fine_tuning": false}); err != nil {
		t.Fatal(err)
	}
	has := addonFeatureCheck(db)
	for user, want := range map[string]bool{"admin": true, "granted": true, "plain": false, "gone": false, "nobody": false, "": false} {
		if got := has(user, "fine_tuning"); got != want {
			t.Errorf("%q: got %v want %v", user, got, want)
		}
	}
}
