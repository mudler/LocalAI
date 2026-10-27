//go:build auth

package application

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
)

func TestTheChatsUserLookupRefusesAnAccountWithoutTheChatsFeature(t *testing.T) {
	db, err := auth.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*auth.User{
		{ID: "member", Email: "m@example.test", Name: "m", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal},
		{ID: "revoked", Email: "r@example.test", Name: "r", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal},
		{ID: "boss", Email: "b@example.test", Name: "b", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal},
		{ID: "paused", Email: "p@example.test", Name: "p", Role: auth.RoleUser, Status: auth.StatusDisabled, Provider: auth.ProviderLocal},
	} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := auth.UpdateUserPermissions(db, "revoked", auth.PermissionMap{auth.FeatureChats: false}); err != nil {
		t.Fatal(err)
	}
	a := &Application{authDB: db}

	for id, want := range map[string]error{
		"member":  nil,
		"boss":    nil,
		"revoked": studio.ErrChatsOff,
		"paused":  studio.ErrUserDisabled,
		"nobody":  studio.ErrNoUser,
	} {
		_, err := a.loadStudioUser(context.Background(), id)
		if !errors.Is(err, want) || (want == nil && err != nil) {
			t.Errorf("loadStudioUser(%q) = %v, want %v", id, err, want)
		}
	}
}
