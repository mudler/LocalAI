//go:build auth

package application

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
)

var _ = Describe("the owner name of a shared project", func() {
	It("gives a plain name and nothing that looks like an address", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		for _, u := range []*auth.User{
			{ID: "named", Email: "n@example.test", Name: "Alice Example", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal},
			{ID: "mail", Email: "m@example.test", Name: "m@example.test", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal},
			{ID: "blank", Email: "b@example.test", Name: "", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal},
		} {
			Expect(db.Create(u).Error).To(Succeed())
		}
		a := &Application{authDB: db}
		ctx := context.Background()
		Expect(a.studioOwnerName(ctx, "named")).To(Equal("Alice Example"))
		Expect(a.studioOwnerName(ctx, "mail")).To(BeEmpty())
		Expect(a.studioOwnerName(ctx, "blank")).To(BeEmpty())
		Expect(a.studioOwnerName(ctx, "nobody")).To(BeEmpty())
	})
})
