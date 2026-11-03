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
	"github.com/mudler/LocalAI/core/services/studio/connsets"
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

	It("reads the single connection stored before sets as a set of one", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		s, err := agents.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		Expect(s.Put(context.Background(), agents.Agent{ID: "a1", Principal: "u1", Name: "Old", Created: now, Updated: now})).To(Succeed())
		Expect(db.Exec(`UPDATE studio_agents SET connections = ? WHERE id = ?`, `{"tracker":"b1"}`, "a1").Error).To(Succeed())
		got, err := s.Get(context.Background(), "u1", "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Connections).To(Equal(connsets.Sets{"tracker": {"b1"}}))
	})

	It("reads an agent stored before the folder fields as one without a folder", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		// The table as it was before the folder columns.
		Expect(db.AutoMigrate(&legacyAgentRow{})).To(Succeed())
		now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		Expect(db.Create(&legacyAgentRow{ID: "old", Principal: "u1", NameKey: "old", Name: "Old", Created: now, Updated: now}).Error).To(Succeed())
		s, err := agents.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		got, err := s.Get(context.Background(), "u1", "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Old"))
		Expect(got.FolderID).To(BeZero())
		Expect(got.FolderMode).To(BeEmpty())
	})

	It("moves the one-folder columns into the folder list at start, once", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db"))
		Expect(db.AutoMigrate(&oneFolderAgentRow{})).To(Succeed())
		now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		Expect(db.Create(&oneFolderAgentRow{ID: "one", Principal: "u1", NameKey: "one", Name: "One", FolderID: 12, FolderMode: "write", Created: now, Updated: now}).Error).To(Succeed())
		Expect(db.Create(&oneFolderAgentRow{ID: "none", Principal: "u1", NameKey: "none", Name: "None", Created: now, Updated: now}).Error).To(Succeed())
		for range 2 { // the second start finds nothing to do
			s, err := agents.NewGORM(db)
			Expect(err).NotTo(HaveOccurred())
			got, err := s.Get(context.Background(), "u1", "one")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Folders).To(Equal([]agents.AgentFolder{{Node: 12, Mode: agents.FolderWrite}}))
			none, _ := s.Get(context.Background(), "u1", "none")
			Expect(none.HasFolder()).To(BeFalse())
		}
		var n int64
		Expect(db.Table("studio_agent_folders").Count(&n).Error).To(Succeed())
		Expect(n).To(Equal(int64(1)))
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

// oneFolderAgentRow is studio_agents as the one-folder release left it.
type oneFolderAgentRow struct {
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
	FolderID     int64  `gorm:"not null;default:0"`
	FolderMode   string `gorm:"size:8;not null;default:''"`
	Created      time.Time
	Updated      time.Time
}

func (oneFolderAgentRow) TableName() string { return "studio_agents" }
