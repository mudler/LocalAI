package remotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// remoteRow is one remote in the shared database. The record without
// its key is the JSON document; the sealed key has its own column.
type remoteRow struct {
	ID      string `gorm:"primaryKey;size:64"`
	NameKey string `gorm:"uniqueIndex;size:255;not null"`
	Doc     string `gorm:"not null"`
	Sealed  []byte
	Created time.Time
	Updated time.Time
}

func (remoteRow) TableName() string { return "proxy_remotes" }

type keyRow struct {
	Name  string `gorm:"primaryKey;size:32"`
	Value []byte
}

func (keyRow) TableName() string { return "proxy_remote_keys" }

type gormStore struct{ db *gorm.DB }

// NewGORMStore returns a Store on db (PostgreSQL in a cluster; SQLite works
// too) and creates its tables.
func NewGORMStore(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("remotes: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&remoteRow{}, &keyRow{}); err != nil {
		return nil, fmt.Errorf("remotes: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func toRow(r record) (remoteRow, error) {
	sealed := r.Sealed
	r.Sealed = nil
	doc, err := json.Marshal(r)
	if err != nil {
		return remoteRow{}, err
	}
	return remoteRow{ID: r.ID, NameKey: nameKey(r.Name), Doc: string(doc), Sealed: sealed, Created: r.Created, Updated: time.Now().UTC()}, nil
}

func (row remoteRow) record() (record, error) {
	var r record
	if err := json.Unmarshal([]byte(row.Doc), &r); err != nil {
		return record{}, fmt.Errorf("remotes: read row %q: %w", row.ID, err)
	}
	r.ID = row.ID
	r.Sealed = append([]byte(nil), row.Sealed...)
	return r, nil
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "duplicate key")
}

func (s *gormStore) Create(ctx context.Context, r record) error {
	row, err := toRow(r)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (s *gormStore) Get(ctx context.Context, id string) (record, error) {
	var row remoteRow
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return record{}, ErrNotFound
	}
	if err != nil {
		return record{}, err
	}
	return row.record()
}

func (s *gormStore) List(ctx context.Context) ([]record, error) {
	var rows []remoteRow
	if err := s.db.WithContext(ctx).Order("name_key, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]record, 0, len(rows))
	for _, row := range rows {
		r, err := row.record()
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Update locks the row for the length of the transaction (FOR UPDATE on
// PostgreSQL), so two frontends that change one remote run one after the
// other.
func (s *gormStore) Update(ctx context.Context, id string, fn func(*record) error) (record, error) {
	var out record
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row remoteRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		cur, err := row.record()
		if err != nil {
			return err
		}
		next := cur.clone()
		if err := fn(&next); err != nil {
			return err
		}
		next.ID = id
		nrow, err := toRow(next)
		if err != nil {
			return err
		}
		nrow.Created = row.Created
		if err := tx.Save(&nrow).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return err
		}
		out = next
		return nil
	})
	return out, err
}

// Delete locks the row when there is a check, so the check and the delete
// see the same record even when another frontend writes it meanwhile.
func (s *gormStore) Delete(ctx context.Context, id string, check func(record) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if check != nil {
			var row remoteRow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			cur, err := row.record()
			if err != nil {
				return err
			}
			if err := check(cur); err != nil {
				return err
			}
		}
		res := tx.Where("id = ?", id).Delete(&remoteRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *gormStore) EnsureKey(ctx context.Context, generate func() ([]byte, error)) ([]byte, error) {
	read := func() ([]byte, bool, error) {
		var r keyRow
		err := s.db.WithContext(ctx).Where("name = ?", "seal").Take(&r).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return r.Value, err == nil, err
	}
	if k, ok, err := read(); err != nil || ok {
		return k, err
	}
	var sealed int64
	if err := s.db.WithContext(ctx).Model(&remoteRow{}).Where("sealed IS NOT NULL AND length(sealed) > 0").Count(&sealed).Error; err != nil {
		return nil, err
	}
	if sealed > 0 {
		xlog.Error("The remotes sealing key is missing from the database but stored keys were sealed with it; no new key is made")
		return nil, errKeyMissing
	}
	k, err := generate()
	if err != nil {
		return nil, err
	}
	// Two frontends may race here; the first insert wins and both read it.
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&keyRow{Name: "seal", Value: k}).Error; err != nil {
		return nil, err
	}
	got, ok, err := read()
	if err != nil || !ok {
		return nil, fmt.Errorf("remotes: the sealing key was not stored (%v)", err)
	}
	return got, nil
}
