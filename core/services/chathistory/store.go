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
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/schema"
)

var (
	// ErrNotFound is returned when a conversation does not exist.
	ErrNotFound = errors.New("conversation not found")
	// ErrInvalidID is returned for malformed conversation IDs.
	ErrInvalidID = errors.New("invalid conversation id")
	// ErrInvalidUserID is returned for malformed user IDs.
	ErrInvalidUserID = errors.New("invalid user id")
	// ErrDeleted is returned when a conversation was deleted. Its tombstone
	// stays so a client with a stale copy cannot write it back.
	ErrDeleted = errors.New("conversation was deleted")
	// ErrConflict is returned when concurrent writers kept racing on the
	// same conversation and the write could not be settled.
	ErrConflict = errors.New("conversation changed concurrently, retry")

	// errRaced makes a transaction start over: another writer created the
	// row between our lookup and our insert.
	errRaced = errors.New("chathistory: raced with a concurrent create")
)

// maxTxAttempts bounds the retry loop for errRaced. One retry settles the
// only race we expect (two first writes of the same id); the rest is margin.
const maxTxAttempts = 3

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
//
// A row with DeletedAt set is a tombstone: the user deleted the conversation,
// Content is emptied, and the row only stays so that no later write can
// bring the conversation back. Version counts every write to the row.
type ConversationRecord struct {
	UserID    string         `gorm:"primaryKey;size:128;column:user_id"`
	ConvID    string         `gorm:"primaryKey;size:128;column:conv_id"`
	Content   string         `gorm:"type:text;column:content"`
	Version   int64          `gorm:"not null;default:0;column:version"`
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

// List returns the live conversations for userID, sorted newest-updated first.
// Deleted conversations are reported by Tombstones instead.
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
		c, err := decodeRecord(r)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, nil
}

// Tombstones returns the conversations userID deleted, newest deletion first.
func (s *Store) Tombstones(userID string) ([]schema.ConversationTombstone, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	var rows []ConversationRecord
	if err := s.db.Unscoped().
		Select("conv_id", "version", "deleted_at").
		Where("user_id = ? AND deleted_at IS NOT NULL", userID).
		Order("deleted_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("chathistory: tombstones: %w", err)
	}
	out := make([]schema.ConversationTombstone, 0, len(rows))
	for _, r := range rows {
		out = append(out, schema.ConversationTombstone{
			ID:        r.ConvID,
			Version:   r.Version,
			DeletedAt: r.DeletedAt.Time.UnixMilli(),
		})
	}
	return out, nil
}

// Get returns a single conversation: ErrNotFound if it never existed,
// ErrDeleted if the user deleted it.
func (s *Store) Get(userID, id string) (*schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	row, err := lookup(s.db, userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotFound
	}
	if row.DeletedAt.Valid {
		return nil, ErrDeleted
	}
	return decodeRecord(*row)
}

// Save upserts a conversation and returns it with its new Version. A
// conversation the user deleted is never written again: Save returns
// ErrDeleted, so a client holding a stale copy cannot resurrect it.
//
// CreatedAt and UpdatedAt are client-managed Unix milliseconds and are
// stored as sent; the UI compares UpdatedAt across devices, so the server
// clock must not replace it. Zero values are stamped with the current time,
// and a zero CreatedAt on an update keeps the stored one.
func (s *Store) Save(userID string, conv schema.Conversation) (*schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateID(conv.ID); err != nil {
		return nil, err
	}
	var saved *schema.Conversation
	err := s.transaction(func(tx *gorm.DB) error {
		var err error
		saved, err = saveInTx(tx, userID, conv)
		return err
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// saveInTx does the lookup and the write in the caller's transaction. The
// version bump is a single UPDATE ... SET version = version + 1 guarded by
// deleted_at IS NULL, so concurrent writers each count, and a delete that
// commits between the lookup and the write still wins.
func saveInTx(tx *gorm.DB, userID string, conv schema.Conversation) (*schema.Conversation, error) {
	existing, err := lookup(tx, userID, conv.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.DeletedAt.Valid {
		return nil, ErrDeleted
	}

	now := time.Now().UnixMilli()
	if conv.CreatedAt == 0 {
		conv.CreatedAt = now
		if existing != nil {
			// Best-effort: a malformed previous row just gets a fresh stamp.
			if prev, derr := decodeRecord(*existing); derr == nil && prev.CreatedAt != 0 {
				conv.CreatedAt = prev.CreatedAt
			}
		}
	}
	if conv.UpdatedAt == 0 {
		conv.UpdatedAt = now
	}
	// The version lives in its own column; keep it out of the stored JSON.
	conv.Version = 0
	data, err := json.Marshal(conv)
	if err != nil {
		return nil, fmt.Errorf("chathistory: marshal: %w", err)
	}

	if existing == nil {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationRecord{
			UserID:  userID,
			ConvID:  conv.ID,
			Content: string(data),
			Version: 1,
		})
		if res.Error != nil {
			return nil, fmt.Errorf("chathistory: insert: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return nil, errRaced
		}
		conv.Version = 1
		return &conv, nil
	}

	// The default scope adds deleted_at IS NULL.
	res := tx.Model(&ConversationRecord{}).
		Where("user_id = ? AND conv_id = ?", userID, conv.ID).
		Updates(map[string]any{
			"content": string(data),
			"version": gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return nil, fmt.Errorf("chathistory: update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrDeleted
	}
	var version int64
	if err := tx.Model(&ConversationRecord{}).
		Select("version").
		Where("user_id = ? AND conv_id = ?", userID, conv.ID).
		Scan(&version).Error; err != nil {
		return nil, fmt.Errorf("chathistory: read version: %w", err)
	}
	conv.Version = version
	return &conv, nil
}

// ReplaceAll makes convs the user's live conversation set in one
// transaction: each one is upserted, and every other live conversation is
// deleted (tombstoned, so the deletion reaches other devices too). Ids the
// user already deleted are not written and are returned in skipped.
func (s *Store) ReplaceAll(userID string, convs []schema.Conversation) (skipped []string, err error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	for _, c := range convs {
		if err := validateID(c.ID); err != nil {
			return nil, err
		}
	}

	err = s.transaction(func(tx *gorm.DB) error {
		skipped = skipped[:0]
		keep := make(map[string]struct{}, len(convs))
		for _, c := range convs {
			keep[c.ID] = struct{}{}
			if _, err := saveInTx(tx, userID, c); err != nil {
				if errors.Is(err, ErrDeleted) {
					skipped = append(skipped, c.ID)
					continue
				}
				return err
			}
		}

		var live []string
		if err := tx.Model(&ConversationRecord{}).
			Where("user_id = ?", userID).
			Pluck("conv_id", &live).Error; err != nil {
			return fmt.Errorf("chathistory: replace list: %w", err)
		}
		var drop []string
		for _, id := range live {
			if _, ok := keep[id]; !ok {
				drop = append(drop, id)
			}
		}
		if len(drop) == 0 {
			return nil
		}
		return tombstone(tx.Where("conv_id IN ?", drop), userID)
	})
	if err != nil {
		return nil, err
	}
	return skipped, nil
}

// Delete deletes a conversation by turning it into a tombstone. Deleting is
// idempotent, and deleting an id the server has not seen yet still records
// a tombstone: the UI deletes immediately while a debounced save of the same
// conversation may still be in flight, and that save must not win.
func (s *Store) Delete(userID, id string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	if err := validateID(id); err != nil {
		return err
	}
	return s.transaction(func(tx *gorm.DB) error {
		existing, err := lookup(tx, userID, id)
		if err != nil {
			return err
		}
		if existing == nil {
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationRecord{
				UserID:    userID,
				ConvID:    id,
				Version:   1,
				DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true},
			})
			if res.Error != nil {
				return fmt.Errorf("chathistory: delete: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				return errRaced
			}
			return nil
		}
		if existing.DeletedAt.Valid {
			return nil
		}
		return tombstone(tx.Where("conv_id = ?", id), userID)
	})
}

// DeleteAll deletes every conversation of a user, leaving tombstones so the
// deletion reaches the user's other devices.
func (s *Store) DeleteAll(userID string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	return tombstone(s.db, userID)
}

// tombstone turns the user's live rows matched by scope into tombstones:
// content dropped, version bumped, deleted_at set.
func tombstone(scope *gorm.DB, userID string) error {
	err := scope.Unscoped().
		Model(&ConversationRecord{}).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Updates(map[string]any{
			"content":    "",
			"version":    gorm.Expr("version + 1"),
			"deleted_at": time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("chathistory: delete: %w", err)
	}
	return nil
}

// transaction runs fn in a transaction and starts over when it lost a race
// with a concurrent first write of the same id.
func (s *Store) transaction(fn func(tx *gorm.DB) error) error {
	for range maxTxAttempts {
		err := s.db.Transaction(fn)
		if !errors.Is(err, errRaced) {
			return err
		}
	}
	return ErrConflict
}

// lookup returns the row for (userID, id) including tombstones, or nil.
func lookup(db *gorm.DB, userID, id string) (*ConversationRecord, error) {
	var row ConversationRecord
	err := db.Unscoped().Where("user_id = ? AND conv_id = ?", userID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chathistory: lookup: %w", err)
	}
	return &row, nil
}

func decodeRecord(r ConversationRecord) (*schema.Conversation, error) {
	var c schema.Conversation
	if err := json.Unmarshal([]byte(r.Content), &c); err != nil {
		return nil, fmt.Errorf("chathistory: unmarshal %q/%q: %w", r.UserID, r.ConvID, err)
	}
	c.Version = r.Version
	return &c, nil
}
