package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MigrationAutomationFeatures names the one-time migration that gives the users
// of an upgraded instance the features that were split out of "chats".
const MigrationAutomationFeatures = "automation-features-2026-10"

// DataMigration records that a one-time data migration ran, so it never runs
// twice. A user registered after it ran gets the defaults, not the migration.
type DataMigration struct {
	Name      string `gorm:"primaryKey;size:128"`
	AppliedAt time.Time
}

// TableName keeps the table apart from the model migrations of other packages.
func (DataMigration) TableName() string { return "auth_data_migrations" }

// MigrateAutomationFeatures runs the one-time rule that keeps what each existing
// user could do when saved agents, triggers and connectors became features of
// their own (default off).
//
// Before the split the single "chats" feature (default on) covered all three, so
// every user could create them. The rule keeps access where it is in use or
// was granted, and nowhere else:
//
//   - agents: kept when the user has the stored "agents" permission (the
//     permission master had, so a stored grant carries over) or owns an agent.
//   - triggers: kept when the user owns a trigger, or had "agents" or
//     "mcp_jobs" (the scheduled-task permissions of master).
//   - connectors: kept when the user owns a connector, or had "agents".
//   - mcp_jobs: stored as it was; nothing is added.
//
// A stored value always wins over the rule, so an administrator's explicit
// "off" stays off. owned maps a user ID to the features the user owns objects
// for (FeatureAgents, FeatureTriggers, FeatureConnectors). Administrators have
// every feature and are skipped. The migration is recorded in the same
// transaction as its changes; a second call does nothing.
func MigrateAutomationFeatures(db *gorm.DB, owned map[string]map[string]bool) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var done DataMigration
		err := tx.Where("name = ?", MigrationAutomationFeatures).Take(&done).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read the migration marker: %w", err)
		}
		var users []User
		if err := tx.Where("role <> ?", RoleAdmin).Find(&users).Error; err != nil {
			return fmt.Errorf("list users: %w", err)
		}
		for _, u := range users {
			if err := migrateUserAutomation(tx, u.ID, owned[u.ID]); err != nil {
				return err
			}
		}
		return tx.Create(&DataMigration{Name: MigrationAutomationFeatures, AppliedAt: time.Now().UTC()}).Error
	})
}

func migrateUserAutomation(tx *gorm.DB, userID string, owns map[string]bool) error {
	var perm UserPermission
	err := tx.Where("user_id = ?", userID).Take(&perm).Error
	exists := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("read permissions of %s: %w", userID, err)
	}
	if !exists {
		perm = UserPermission{ID: uuid.New().String(), UserID: userID, Permissions: PermissionMap{}}
	}
	if perm.Permissions == nil {
		perm.Permissions = PermissionMap{}
	}
	stored := func(f string) bool { return perm.Permissions[f] }
	has := func(f string) bool { _, ok := perm.Permissions[f]; return ok }
	rules := []struct {
		feature string
		keep    bool
	}{
		{FeatureAgents, stored(FeatureAgents) || owns[FeatureAgents]},
		{FeatureTriggers, owns[FeatureTriggers] || stored(FeatureAgents) || stored(FeatureMCPJobs)},
		{FeatureConnectors, owns[FeatureConnectors] || stored(FeatureAgents)},
	}
	changed := false
	for _, r := range rules {
		if r.keep && !has(r.feature) {
			perm.Permissions[r.feature] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return tx.Save(&perm).Error
}
