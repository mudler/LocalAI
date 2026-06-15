package auth

import (
	"fmt"

	"gorm.io/gorm"
)

// DeleteUserCascade removes a user and all of their owned data.
//
// PostgreSQL strictly enforces every foreign key, while SQLite only enforces
// them when foreign_keys=ON. So we explicitly delete every dependent row
// instead of relying on `ON DELETE CASCADE`, otherwise:
//
//   - on PostgreSQL the user delete fails with a constraint violation when
//     the user authored or consumed any invite codes (the InviteCode FKs are
//     declared without an OnDelete: CASCADE constraint), and
//   - usage_records have no FK at all, so they would be left orphaned in any
//     dialect.
//
// It also clears the in-memory quota cache for the user.
//
// Returns gorm.ErrRecordNotFound when the user does not exist.
func DeleteUserCascade(db *gorm.DB, userID string) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		// Drop invites authored by this user; the admin who issued them is gone.
		if err := tx.Where("created_by = ?", userID).Delete(&InviteCode{}).Error; err != nil {
			return fmt.Errorf("delete invites created by user: %w", err)
		}
		// Preserve audit trail for invites consumed by this user — null the FK.
		if err := tx.Model(&InviteCode{}).Where("used_by = ?", userID).Update("used_by", nil).Error; err != nil {
			return fmt.Errorf("clear used_by on invites: %w", err)
		}
		// Wipe collected metrics; they have no FK and would otherwise orphan.
		if err := tx.Where("user_id = ?", userID).Delete(&UsageRecord{}).Error; err != nil {
			return fmt.Errorf("delete usage records: %w", err)
		}
		// Explicit deletes for the CASCADE-backed children too — they're cheap
		// and keep behaviour identical across SQLite (FKs may be OFF) and
		// PostgreSQL.
		if err := tx.Where("user_id = ?", userID).Delete(&Session{}).Error; err != nil {
			return fmt.Errorf("delete sessions: %w", err)
		}
		if err := tx.Where("user_id = ?", userID).Delete(&UserAPIKey{}).Error; err != nil {
			return fmt.Errorf("delete api keys: %w", err)
		}
		if err := tx.Where("user_id = ?", userID).Delete(&UserPermission{}).Error; err != nil {
			return fmt.Errorf("delete permissions: %w", err)
		}
		if err := tx.Where("user_id = ?", userID).Delete(&QuotaRule{}).Error; err != nil {
			return fmt.Errorf("delete quota rules: %w", err)
		}

		result := tx.Where("id = ?", userID).Delete(&User{})
		if result.Error != nil {
			return fmt.Errorf("delete user: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}

	quotaCache.invalidateUser(userID)
	return nil
}
