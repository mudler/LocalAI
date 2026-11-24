//go:build auth

package application

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
)

var _ = Describe("the chats user lookup", func() {
	It("refuses an account without the chats feature", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		for _, u := range []*auth.User{
			{ID: "member", Email: "m@example.test", Name: "m", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal},
			{ID: "revoked", Email: "r@example.test", Name: "r", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal},
			{ID: "boss", Email: "b@example.test", Name: "b", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal},
			{ID: "paused", Email: "p@example.test", Name: "p", Role: auth.RoleUser, Status: auth.StatusDisabled, Provider: auth.ProviderLocal},
		} {
			Expect(db.Create(u).Error).To(Succeed())
		}
		Expect(auth.UpdateUserPermissions(db, "revoked", auth.PermissionMap{auth.FeatureChats: false})).To(Succeed())
		a := &Application{authDB: db}

		for id, want := range map[string]error{
			"member":  nil,
			"boss":    nil,
			"revoked": studio.ErrChatsOff,
			"paused":  studio.ErrUserDisabled,
			"nobody":  studio.ErrNoUser,
		} {
			_, err := a.loadStudioUser(context.Background(), id)
			if want == nil {
				Expect(err).ToNot(HaveOccurred(), "loadStudioUser(%q)", id)
			} else {
				Expect(err).To(MatchError(want), "loadStudioUser(%q)", id)
			}
		}
	})
})
