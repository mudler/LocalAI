package agentpool

import (
	"fmt"

	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var _ = Describe("knowledge-base models authorization", func() {
	It("checks both overrides, permits admins, and fails closed on database errors", func() {
		// Supply rows at the ORM boundary without requiring PostgreSQL or CGO.
		db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost user=test dbname=test sslmode=disable"}), &gorm.Config{DisableAutomaticPing: true, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		role := auth.RoleUser
		failPermissions, failUser := false, false
		Expect(db.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
			switch row := tx.Statement.Dest.(type) {
			case *auth.User:
				if failUser {
					tx.AddError(fmt.Errorf("user lookup unavailable"))
					return
				}
				*row = auth.User{ID: "alice", Role: role}
			case *auth.UserPermission:
				if failPermissions {
					tx.AddError(fmt.Errorf("permissions unavailable"))
					return
				}
				*row = auth.UserPermission{UserID: "alice", AllowedModels: auth.ModelAllowlist{Enabled: true, Models: []string{"allowed"}}}
			default:
				tx.AddError(fmt.Errorf("unexpected query destination %T", row))
			}
		})).To(Succeed())
		cfg := &state.AgentConfig{Name: "Research", EmbeddingModel: "denied"}
		svc := &AgentPoolService{appConfig: &config.ApplicationConfig{}, configBackend: &modelConfigBackend{configs: map[string]*state.AgentConfig{"alice/Research": cfg}}}
		svc.users.authDB = db
		resolve := svc.collectionModelSettings("alice")
		_, err = resolve("Research")
		Expect(err).To(HaveOccurred())
		cfg.EmbeddingModel, cfg.RerankerModel = "allowed", "denied"
		_, err = resolve("Research")
		Expect(err).To(HaveOccurred())
		cfg.RerankerModel = "allowed"
		settings, err := resolve("Research")
		Expect(err).NotTo(HaveOccurred())
		Expect(settings.EmbeddingModel).To(Equal("allowed"))
		role = auth.RoleAdmin
		cfg.EmbeddingModel = "denied"
		_, err = resolve("Research")
		Expect(err).NotTo(HaveOccurred())
		role, failPermissions = auth.RoleUser, true
		_, err = resolve("Research")
		Expect(err).To(MatchError(ContainSubstring("permissions unavailable")))
		failUser = true
		_, err = resolve("Research")
		Expect(err).To(MatchError(ContainSubstring("user lookup unavailable")))
	})
})
