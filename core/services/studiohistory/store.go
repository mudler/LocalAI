// Package studiohistory implements server-side persistence of the Studio
// add-on's generation history (pictures, clips, voice lines, sounds, audio
// transforms, diarizations, 3D meshes). It follows the chat history store
// (core/services/chathistory): entries live in the shared GORM database
// (Application.authDB), partitioned by user, with tombstones so a deletion
// reaches every browser. When auth is disabled the store is not initialised
// and the Studio keeps its history in localStorage as before.
//
// Only the records are stored here. The generated files themselves stay
// where the generation endpoints write them (GeneratedContentDir).
package studiohistory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/schema"
)

var (
	// ErrNotFound is returned when an entry does not exist.
	ErrNotFound = errors.New("studio history entry not found")
	// ErrInvalidID is returned for malformed entry IDs.
	ErrInvalidID = errors.New("invalid studio history entry id")
	// ErrInvalidType is returned for an unknown Studio workspace.
	ErrInvalidType = errors.New("invalid studio history type")
	// ErrInvalidUserID is returned for malformed user IDs.
	ErrInvalidUserID = errors.New("invalid user id")
	// ErrInvalidEntry is returned when the entry body is not a JSON object.
	ErrInvalidEntry = errors.New("studio history entry must be a JSON object")
	// ErrDeleted is returned when an entry was deleted. Its tombstone stays
	// so a browser with a stale copy cannot write it back.
	ErrDeleted = errors.New("studio history entry was deleted")
	// ErrConflict is returned when concurrent writers kept racing on the same
	// entry and the write could not be settled.
	ErrConflict = errors.New("studio history entry changed concurrently, retry")

	// errRaced makes a transaction start over: another writer created the
	// row between our lookup and our insert.
	errRaced = errors.New("studiohistory: raced with a concurrent create")
)

// DefaultMaxPerType is how many live entries a user keeps per workspace. The
// browser cache keeps 100; the server keeps more, and the oldest entries
// beyond the cap become tombstones.
const DefaultMaxPerType = 1000

// MaxEntryBytes caps one stored entry. Studio entries are a prompt, a few
// parameters and result URLs; the Studio itself caps a prompt at 2000
// characters. Anything far larger is a client bug (an inlined data: URL).
const MaxEntryBytes = 64 * 1024

const maxTxAttempts = 3

// idRegex matches the Studio's crypto-random ids (utils/format.generateId)
// and keeps ids inside the entry_id column (size 128), free of whitespace and
// control characters.
var idRegex = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

const userIDMaxLen = 128

// EntryRecord is the GORM row of a Studio history entry. The primary key is
// (UserID, EntryID): the Studio mints ids locally, so they are only unique per
// user. A row with DeletedAt set is a tombstone: Content is emptied and the
// row only stays so that no later write can bring the entry back.
type EntryRecord struct {
	UserID    string         `gorm:"primaryKey;size:128;column:user_id"`
	EntryID   string         `gorm:"primaryKey;size:128;column:entry_id"`
	MediaType string         `gorm:"size:32;index:idx_studio_history_user_type,priority:2;column:media_type"`
	Content   string         `gorm:"type:text;column:content"`
	Version   int64          `gorm:"not null;default:0;column:version"`
	EntryAt   int64          `gorm:"index:idx_studio_history_user_type,priority:3;column:entry_at"` // the entry's createdAt, Unix ms
	EditedAt  int64          `gorm:"column:edited_at"`                                              // the entry's updatedAt, Unix ms
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index;column:deleted_at"`
}

// TableName returns the database table name for EntryRecord.
func (EntryRecord) TableName() string { return "studio_history_entries" }

// Store persists Studio history entries to a GORM-backed database,
// partitioned by userID.
type Store struct {
	db         *gorm.DB
	maxPerType int
}

// New creates a Store backed by db and auto-migrates the schema. The caller
// passes the shared authDB, so Studio history sits next to chat history.
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("studiohistory: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&EntryRecord{}); err != nil {
		return nil, fmt.Errorf("studiohistory: auto-migrate: %w", err)
	}
	return &Store{db: db, maxPerType: DefaultMaxPerType}, nil
}

// SetMaxPerType changes how many live entries a user keeps per workspace.
// Values below 1 restore the default.
func (s *Store) SetMaxPerType(n int) {
	if n < 1 {
		n = DefaultMaxPerType
	}
	s.maxPerType = n
}

// CacheKey returns the opaque per-account tag the Studio uses to keep one
// browser cache per account. It is derived from the user id, so it is stable
// across browsers, and hashed, so it does not reveal the id.
func CacheKey(userID string) string {
	sum := sha256.Sum256([]byte("localai-studio-history:" + userID))
	return hex.EncodeToString(sum[:12])
}

func validateUserID(id string) error {
	if len(id) > userIDMaxLen {
		return ErrInvalidUserID
	}
	return nil
}

func validateID(id string) error {
	if !idRegex.MatchString(id) {
		return ErrInvalidID
	}
	return nil
}

func validateType(t string) error {
	if !slices.Contains(schema.StudioHistoryTypes, t) {
		return ErrInvalidType
	}
	return nil
}

func validateEntry(e schema.StudioHistoryEntry) error {
	if err := validateID(e.ID); err != nil {
		return err
	}
	if err := validateType(e.Type); err != nil {
		return err
	}
	if len(e.Entry) > MaxEntryBytes {
		return ErrInvalidEntry
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(e.Entry, &obj); err != nil || obj == nil {
		return ErrInvalidEntry
	}
	return nil
}

// List returns the user's live entries, newest first. An empty mediaType
// lists every workspace.
func (s *Store) List(userID, mediaType string) ([]schema.StudioHistoryEntry, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	q := s.db.Where("user_id = ?", userID)
	if mediaType != "" {
		if err := validateType(mediaType); err != nil {
			return nil, err
		}
		q = q.Where("media_type = ?", mediaType)
	}
	var rows []EntryRecord
	if err := q.Order("entry_at DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("studiohistory: list: %w", err)
	}
	out := make([]schema.StudioHistoryEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, decodeRecord(r))
	}
	return out, nil
}

// Tombstones returns the entries the user deleted, newest deletion first. An
// empty mediaType lists every workspace.
func (s *Store) Tombstones(userID, mediaType string) ([]schema.StudioHistoryTombstone, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	q := s.db.Unscoped().
		Select("entry_id", "media_type", "version", "deleted_at").
		Where("user_id = ? AND deleted_at IS NOT NULL", userID)
	if mediaType != "" {
		if err := validateType(mediaType); err != nil {
			return nil, err
		}
		q = q.Where("media_type = ?", mediaType)
	}
	var rows []EntryRecord
	if err := q.Order("deleted_at DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("studiohistory: tombstones: %w", err)
	}
	out := make([]schema.StudioHistoryTombstone, 0, len(rows))
	for _, r := range rows {
		out = append(out, schema.StudioHistoryTombstone{
			ID:        r.EntryID,
			Type:      r.MediaType,
			Version:   r.Version,
			DeletedAt: r.DeletedAt.Time.UnixMilli(),
		})
	}
	return out, nil
}

// Get returns one entry: ErrNotFound if it never existed, ErrDeleted if the
// user deleted it.
func (s *Store) Get(userID, id string) (*schema.StudioHistoryEntry, error) {
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
	e := decodeRecord(*row)
	return &e, nil
}

// Save upserts an entry and returns it with its new Version. An entry the
// user deleted is never written again (ErrDeleted). An entry cannot move to
// another workspace: its type is fixed by its first save.
//
// CreatedAt and UpdatedAt are client-managed Unix milliseconds, stored as
// sent; zero values are stamped with the current time.
func (s *Store) Save(userID string, e schema.StudioHistoryEntry) (*schema.StudioHistoryEntry, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateEntry(e); err != nil {
		return nil, err
	}
	var saved *schema.StudioHistoryEntry
	err := s.transaction(func(tx *gorm.DB) error {
		var err error
		if saved, err = saveInTx(tx, userID, e); err != nil {
			return err
		}
		return s.trimInTx(tx, userID, e.Type)
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// Import stores entries a browser kept before its history moved to the
// server. Unlike a replace it never deletes: entries the server already has
// are updated, new ones are added, and ids the user deleted are skipped and
// returned. Invalid entries fail the whole import.
func (s *Store) Import(userID string, entries []schema.StudioHistoryEntry) (stored int, skipped []string, err error) {
	if err := validateUserID(userID); err != nil {
		return 0, nil, err
	}
	for _, e := range entries {
		if err := validateEntry(e); err != nil {
			return 0, nil, err
		}
	}
	err = s.transaction(func(tx *gorm.DB) error {
		stored, skipped = 0, skipped[:0]
		types := map[string]struct{}{}
		for _, e := range entries {
			if _, err := saveInTx(tx, userID, e); err != nil {
				if errors.Is(err, ErrDeleted) {
					skipped = append(skipped, e.ID)
					continue
				}
				return err
			}
			stored++
			types[e.Type] = struct{}{}
		}
		for t := range types {
			if err := s.trimInTx(tx, userID, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return stored, skipped, nil
}

func saveInTx(tx *gorm.DB, userID string, e schema.StudioHistoryEntry) (*schema.StudioHistoryEntry, error) {
	existing, err := lookup(tx, userID, e.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.DeletedAt.Valid {
		return nil, ErrDeleted
	}
	if existing != nil && existing.MediaType != e.Type {
		return nil, ErrInvalidType
	}

	now := time.Now().UnixMilli()
	if e.CreatedAt == 0 {
		e.CreatedAt = now
		if existing != nil && existing.EntryAt != 0 {
			e.CreatedAt = existing.EntryAt
		}
	}
	if e.UpdatedAt == 0 {
		e.UpdatedAt = now
	}

	if existing == nil {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&EntryRecord{
			UserID:    userID,
			EntryID:   e.ID,
			MediaType: e.Type,
			Content:   string(e.Entry),
			Version:   1,
			EntryAt:   e.CreatedAt,
			EditedAt:  e.UpdatedAt,
		})
		if res.Error != nil {
			return nil, fmt.Errorf("studiohistory: insert: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return nil, errRaced
		}
		e.Version = 1
		return &e, nil
	}

	// The default scope adds deleted_at IS NULL, so a delete that commits
	// between the lookup and this write still wins.
	res := tx.Model(&EntryRecord{}).
		Where("user_id = ? AND entry_id = ?", userID, e.ID).
		Updates(map[string]any{
			"content":   string(e.Entry),
			"entry_at":  e.CreatedAt,
			"edited_at": e.UpdatedAt,
			"version":   gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return nil, fmt.Errorf("studiohistory: update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrDeleted
	}
	if err := tx.Model(&EntryRecord{}).
		Select("version").
		Where("user_id = ? AND entry_id = ?", userID, e.ID).
		Scan(&e.Version).Error; err != nil {
		return nil, fmt.Errorf("studiohistory: read version: %w", err)
	}
	return &e, nil
}

// trimInTx turns the user's oldest live entries of mediaType beyond the cap
// into tombstones.
func (s *Store) trimInTx(tx *gorm.DB, userID, mediaType string) error {
	var old []string
	if err := tx.Model(&EntryRecord{}).
		Where("user_id = ? AND media_type = ?", userID, mediaType).
		Order("entry_at DESC").
		Offset(s.maxPerType).
		Pluck("entry_id", &old).Error; err != nil {
		return fmt.Errorf("studiohistory: trim: %w", err)
	}
	if len(old) == 0 {
		return nil
	}
	return tombstone(tx.Where("entry_id IN ?", old), userID)
}

// Delete turns an entry into a tombstone. Deleting is idempotent, and
// deleting an id the server has not seen yet still records a tombstone (with
// mediaType, which may be empty if the client does not know it): the Studio
// deletes at once while a debounced save of the same entry may be in flight,
// and that save must not win.
func (s *Store) Delete(userID, id, mediaType string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	if err := validateID(id); err != nil {
		return err
	}
	if mediaType != "" {
		if err := validateType(mediaType); err != nil {
			return err
		}
	}
	return s.transaction(func(tx *gorm.DB) error {
		existing, err := lookup(tx, userID, id)
		if err != nil {
			return err
		}
		if existing == nil {
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&EntryRecord{
				UserID:    userID,
				EntryID:   id,
				MediaType: mediaType,
				Version:   1,
				DeletedAt: gorm.DeletedAt{Time: time.Now(), Valid: true},
			})
			if res.Error != nil {
				return fmt.Errorf("studiohistory: delete: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				return errRaced
			}
			return nil
		}
		if existing.DeletedAt.Valid {
			return nil
		}
		return tombstone(tx.Where("entry_id = ?", id), userID)
	})
}

// DeleteAll deletes the user's entries of mediaType, or of every workspace
// when mediaType is empty, leaving tombstones.
func (s *Store) DeleteAll(userID, mediaType string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	scope := s.db
	if mediaType != "" {
		if err := validateType(mediaType); err != nil {
			return err
		}
		scope = scope.Where("media_type = ?", mediaType)
	}
	return tombstone(scope, userID)
}

// tombstone turns the user's live rows matched by scope into tombstones:
// content dropped, version bumped, deleted_at set.
func tombstone(scope *gorm.DB, userID string) error {
	err := scope.Unscoped().
		Model(&EntryRecord{}).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Updates(map[string]any{
			"content":    "",
			"version":    gorm.Expr("version + 1"),
			"deleted_at": time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("studiohistory: delete: %w", err)
	}
	return nil
}

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
func lookup(db *gorm.DB, userID, id string) (*EntryRecord, error) {
	var row EntryRecord
	err := db.Unscoped().Where("user_id = ? AND entry_id = ?", userID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("studiohistory: lookup: %w", err)
	}
	return &row, nil
}

func decodeRecord(r EntryRecord) schema.StudioHistoryEntry {
	return schema.StudioHistoryEntry{
		ID:        r.EntryID,
		Type:      r.MediaType,
		Entry:     json.RawMessage(r.Content),
		CreatedAt: r.EntryAt,
		UpdatedAt: r.EditedAt,
		Version:   r.Version,
	}
}
