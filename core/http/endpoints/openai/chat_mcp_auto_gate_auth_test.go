//go:build auth

package openai

import (
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// With auth on, mcp.auto must honour the caller's "mcp" feature like the
// dedicated MCP routes do; having "chat" alone is not enough.
var _ = Describe("ChatEndpoint mcp.auto user gate", func() {
	var (
		db        *gorm.DB
		appConfig *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		appConfig = &config.ApplicationConfig{}
		appConfig.Auth.Enabled = true
	})

	userWith := func(perms auth.PermissionMap) *auth.User {
		user := &auth.User{
			ID:       uuid.NewString(),
			Email:    uuid.NewString() + "@example.com",
			Provider: auth.ProviderLocal,
			Subject:  uuid.NewString(),
			Role:     auth.RoleUser,
			Status:   auth.StatusActive,
		}
		Expect(db.Create(user).Error).ToNot(HaveOccurred())
		Expect(auth.UpdateUserPermissions(db, user.ID, perms)).To(Succeed())
		return user
	}

	It("stays a plain chat for a user who has chat but not mcp", func() {
		fx := newMCPAutoFixture()
		user := userWith(auth.PermissionMap{auth.FeatureChat: true, auth.FeatureMCP: false})
		Expect(fx.chat(appConfig, db, user)).To(BeEmpty())
		Expect(fx.requests.Load()).To(BeZero(), "no MCP server may be contacted")
	})

	It("enables the model's servers for a user who has mcp", func() {
		fx := newMCPAutoFixture()
		user := userWith(auth.PermissionMap{auth.FeatureChat: true, auth.FeatureMCP: true})
		Expect(fx.chat(appConfig, db, user)).To(ContainElement("lookup"))
		Expect(fx.requests.Load()).To(BeNumerically(">", 0))
	})
})
