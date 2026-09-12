// Package chathistory implements server-side persistence of WebUI chat
// conversations (GitHub issue #9432). Conversations live in the same
// GORM-backed database as the rest of the per-user state (AgentStore,
// JobStore): the store reuses Application.authDB so chat history,
// agent configs and jobs land in one place. When auth is disabled the
// store is not initialised and the React UI transparently falls back to
// localStorage.
package chathistory

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/schema"
)

var (
	// ErrNotFound is returned when a conversation does not exist.
	ErrNotFound = errors.New("conversation not found")
	// ErrInvalidID is returned for malformed conversation IDs.
	ErrInvalidID = errors.New("invalid conversation id")
	// ErrInvalidUserID is returned for malformed user IDs.
	ErrInvalidUserID = errors.New("invalid user id")
)

// idRegex constrains conversation IDs so they fit into the conv_id column
// (size 128) and cannot smuggle whitespace or control characters into log
// lines / responses. The React UI uses crypto-random IDs
// (utils/format.generateId) which fit comfortably inside this class.
var idRegex = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// userIDMaxLen caps the user ID length to match the user_id column size.
// We don't constrain the character class because the auth subsystem chooses
// the shape (UUID, OAuth subject, etc.) and SQL parameter binding already
// prevents injection.
const userIDMaxLen = 128

// ConversationRecord is the GORM row representation of a chat conversation.
//
// The primary key is (UserID, ConvID): React mints conversation IDs locally
// with no global coordination so they're only unique per user. A composite
// key makes per-user partitioning fall out naturally — anonymous users
// (UserID == "") get their own slice with no special-case code path.
type ConversationRecord struct {
	UserID    string         `gorm:"primaryKey;size:128;column:user_id"`
	ConvID    string         `gorm:"primaryKey;size:128;column:conv_id"`
	Content   string         `gorm:"type:text;column:content"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"index;column:updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index;column:deleted_at"`
}

// TableName returns the database table name for ConversationRecord.
func (ConversationRecord) TableName() string { return "chat_conversations" }

// Store persists conversations to a GORM-backed database, partitioned by
// userID. The empty string is treated as the anonymous (no-auth) user.
type Store struct {
	db *gorm.DB
}

// New creates a new Store backed by db and auto-migrates the schema. The
// caller is expected to pass the shared authDB so chat history sits in
// the same database as the other per-user state in LocalAI.
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("chathistory: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&ConversationRecord{}); err != nil {
		return nil, fmt.Errorf("chathistory: auto-migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func validateID(id string) error {
	if !idRegex.MatchString(id) {
		return ErrInvalidID
	}
	return nil
}

func validateUserID(id string) error {
	if len(id) > userIDMaxLen {
		return ErrInvalidUserID
	}
	return nil
}

// List returns all conversations for userID, sorted newest-updated first.
func (s *Store) List(userID string) ([]schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	var rows []ConversationRecord
	if err := s.db.
		Where("user_id = ?", userID).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("chathistory: list: %w", err)
	}
	out := make([]schema.Conversation, 0, len(rows))
	for _, r := range rows {
		var c schema.Conversation
		if err := json.Unmarshal([]byte(r.Content), &c); err != nil {
			return nil, fmt.Errorf("chathistory: unmarshal %q/%q: %w", r.UserID, r.ConvID, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// Get returns a single conversation, or ErrNotFound if absent.
func (s *Store) Get(userID, id string) (*schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	var row ConversationRecord
	err := s.db.Where("user_id = ? AND conv_id = ?", userID, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chathistory: get: %w", err)
	}
	var c schema.Conversation
	if err := json.Unmarshal([]byte(row.Content), &c); err != nil {
		return nil, fmt.Errorf("chathistory: unmarshal: %w", err)
	}
	return &c, nil
}

// Save upserts a conversation. CreatedAt is preserved across updates when
// the caller passes 0; UpdatedAt is refreshed on every save. Timestamps
// inside the Conversation struct are React-managed Unix milliseconds; the
// GORM row's own created_at/updated_at columns are DB metadata used for
// future retention queries.
func (s *Store) Save(userID string, conv schema.Conversation) (*schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateID(conv.ID); err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()

	// Look up the existing row first so we can preserve the original
	// CreatedAt when the caller omits it on an update.
	var existing ConversationRecord
	err := s.db.Where("user_id = ? AND conv_id = ?", userID, conv.ID).First(&existing).Error
	switch {
	case err == nil:
		if conv.CreatedAt == 0 {
			var prev schema.Conversation
			// Best-effort decode: if the previous row was malformed we still
			// want the write to succeed and just stamp CreatedAt with now.
			if uerr := json.Unmarshal([]byte(existing.Content), &prev); uerr == nil {
				conv.CreatedAt = prev.CreatedAt
			} else {
				conv.CreatedAt = now
			}
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		if conv.CreatedAt == 0 {
			conv.CreatedAt = now
		}
	default:
		return nil, fmt.Errorf("chathistory: lookup: %w", err)
	}
	conv.UpdatedAt = now

	data, err := json.Marshal(conv)
	if err != nil {
		return nil, fmt.Errorf("chathistory: marshal: %w", err)
	}
	rec := ConversationRecord{
		UserID:  userID,
		ConvID:  conv.ID,
		Content: string(data),
	}
	// gorm.Save() issues INSERT ... ON CONFLICT DO UPDATE when all primary
	// key columns are set, which matches our composite-key shape exactly
	// and keeps Save() race-free under concurrent writers.
	if err := s.db.Save(&rec).Error; err != nil {
		return nil, fmt.Errorf("chathistory: save: %w", err)
	}
	return &conv, nil
}

// ReplaceAll atomically swaps the user's entire conversation set. Used by
// the localStorage migration upload: retries are safe because the
// operation is all-or-nothing.
func (s *Store) ReplaceAll(userID string, convs []schema.Conversation) error {
	if err := validateUserID(userID); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	rows := make([]ConversationRecord, 0, len(convs))
	for _, c := range convs {
		if err := validateID(c.ID); err != nil {
			return err
		}
		if c.CreatedAt == 0 {
			c.CreatedAt = now
		}
		if c.UpdatedAt == 0 {
			c.UpdatedAt = now
		}
		data, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("chathistory: marshal %q: %w", c.ID, err)
		}
		rows = append(rows, ConversationRecord{
			UserID:  userID,
			ConvID:  c.ID,
			Content: string(data),
		})
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Hard delete (Unscoped) so a future retention sweep cannot
		// resurrect an old soft-deleted row that shares an ID with a
		// freshly uploaded conversation.
		if err := tx.
			Unscoped().
			Where("user_id = ?", userID).
			Delete(&ConversationRecord{}).Error; err != nil {
			return fmt.Errorf("chathistory: replace clear: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

// Delete removes a conversation, returning ErrNotFound if it does not exist.
// Soft delete (GORM populates deleted_at) so a future retention or audit
// pruner can still see what was there.
func (s *Store) Delete(userID, id string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	if err := validateID(id); err != nil {
		return err
	}
	result := s.db.
		Where("user_id = ? AND conv_id = ?", userID, id).
		Delete(&ConversationRecord{})
	if result.Error != nil {
		return fmt.Errorf("chathistory: delete: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAll wipes every conversation for a user. Soft delete semantics so a
// retention or audit pruner can still see what was there before the wipe.
func (s *Store) DeleteAll(userID string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	return s.db.
		Where("user_id = ?", userID).
		Delete(&ConversationRecord{}).Error
}
