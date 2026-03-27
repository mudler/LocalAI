package agents

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB() *gorm.DB {
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { pgC.Terminate(context.Background()) })

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	Expect(err).ToNot(HaveOccurred())

	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	Expect(err).ToNot(HaveOccurred())
	return db
}

var _ = Describe("AgentStore", func() {
	var store *AgentStore

	BeforeEach(func() {
		db := setupTestDB()
		var err error
		store, err = NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	It("filters observables by agent name", func() {
		obs1 := &AgentObservableRecord{
			AgentName:   "u1:agent",
			EventType:   "status",
			PayloadJSON: `{"msg":"hello from u1"}`,
		}
		obs2 := &AgentObservableRecord{
			AgentName:   "u2:agent",
			EventType:   "action",
			PayloadJSON: `{"msg":"hello from u2"}`,
		}
		Expect(store.AppendObservable(obs1)).To(Succeed())
		Expect(store.AppendObservable(obs2)).To(Succeed())

		results, err := store.GetObservables("u1:agent", 100)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].AgentName).To(Equal("u1:agent"))
		Expect(results[0].PayloadJSON).To(Equal(`{"msg":"hello from u1"}`))
	})

	It("clears observables for an agent", func() {
		obs := &AgentObservableRecord{
			AgentName:   "clearme:agent",
			EventType:   "status",
			PayloadJSON: `{"msg":"will be cleared"}`,
		}
		Expect(store.AppendObservable(obs)).To(Succeed())

		results, err := store.GetObservables("clearme:agent", 100)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))

		Expect(store.ClearObservables("clearme:agent")).To(Succeed())

		results, err = store.GetObservables("clearme:agent", 100)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("saves and gets a config", func() {
		cfg := &AgentConfigRecord{
			UserID:     "user-1",
			Name:       "my-agent",
			ConfigJSON: `{"model":"gpt-4","connector":[]}`,
			Status:     "active",
		}
		Expect(store.SaveConfig(cfg)).To(Succeed())

		got, err := store.GetConfig("user-1", "my-agent")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.UserID).To(Equal("user-1"))
		Expect(got.Name).To(Equal("my-agent"))
		Expect(got.ConfigJSON).To(Equal(`{"model":"gpt-4","connector":[]}`))
		Expect(got.Status).To(Equal("active"))
	})

	It("upserts config on duplicate user+name", func() {
		cfg := &AgentConfigRecord{
			UserID:     "user-2",
			Name:       "upsert-agent",
			ConfigJSON: `{"model":"gpt-3.5"}`,
			Status:     "active",
		}
		Expect(store.SaveConfig(cfg)).To(Succeed())
		originalID := cfg.ID

		cfg2 := &AgentConfigRecord{
			UserID:     "user-2",
			Name:       "upsert-agent",
			ConfigJSON: `{"model":"gpt-4o"}`,
			Status:     "paused",
		}
		Expect(store.SaveConfig(cfg2)).To(Succeed())

		got, err := store.GetConfig("user-2", "upsert-agent")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.ID).To(Equal(originalID))
		Expect(got.ConfigJSON).To(Equal(`{"model":"gpt-4o"}`))
		Expect(got.Status).To(Equal("paused"))
	})
})
