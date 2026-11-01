package agents_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the GORM agent store on SQLite", func() {
	agentstest.Run(func() agents.Store {
		s, err := agents.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	It("reads an agent stored before the folder fields as one without a folder", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		// The table as it was before the folder columns.
		Expect(db.AutoMigrate(&legacyAgentRow{})).To(Succeed())
		now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		Expect(db.Create(&legacyAgentRow{ID: "old", Principal: "u1", NameKey: "old", Name: "Old", Collections: `["docs"]`, Created: now, Updated: now}).Error).To(Succeed())
		s, err := agents.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		got, err := s.Get(context.Background(), "u1", "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Old"))
		Expect(got.Memory.AddMemory).To(BeFalse())
		Expect(got.Memory.AutomaticIngestion).To(Equal("off"))
		Expect(got.CollectionPermissions.Grants).To(Equal([]agents.CollectionGrant{{Collection: "docs", Search: true}}))
		Expect(got.FolderID).To(BeZero())
		Expect(got.FolderMode).To(BeEmpty())
	})

	It("survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		first, err := agents.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		agentstest.Seed(first)
		second, err := agents.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		agentstest.CheckSeed(second)
	})
})

// legacyAgentRow is studio_agents before the folder columns.
type legacyAgentRow struct {
	ID           string `gorm:"primaryKey;size:64"`
	Principal    string `gorm:"index:idx_studio_agents_name,unique;size:255"`
	NameKey      string `gorm:"index:idx_studio_agents_name,unique;size:255"`
	Name         string `gorm:"size:255"`
	Description  string
	Instructions string
	Model        string `gorm:"size:255"`
	MCPServers   string
	Addons       string
	Collections  string
	Skills       string
	Network      string `gorm:"size:16"`
	Created      time.Time
	Updated      time.Time
}

func (legacyAgentRow) TableName() string { return "studio_agents" }
