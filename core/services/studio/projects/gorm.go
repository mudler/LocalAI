package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/internal/dberr"
)

// projectRow is the table of the project index. Slug is stored in lower case:
// the unique index on (owner, slug) is what keeps slugs apart under a race.
type projectRow struct {
	ID      string `gorm:"primaryKey;size:64"`
	Owner   string `gorm:"size:255;not null;uniqueIndex:idx_studio_projects_owner_slug"`
	Slug    string `gorm:"size:255;not null;uniqueIndex:idx_studio_projects_owner_slug"`
	Name    string `gorm:"size:255"`
	Node    int64
	Status  string `gorm:"size:16"`
	Error   string
	Updated time.Time
}

func (projectRow) TableName() string { return "studio_projects" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its table.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("projects: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&projectRow{}); err != nil {
		return nil, fmt.Errorf("projects: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (r projectRow) project() Project {
	return Project{ID: r.ID, Owner: r.Owner, Slug: r.Slug, Name: r.Name, Node: r.Node,
		Status: Status(r.Status), Error: r.Error, Updated: r.Updated.UTC()}
}

// isSlugTaken tells the (owner, slug) index from the primary key: both are
// unique violations. SQLite names the columns and PostgreSQL names the index.
func isSlugTaken(err error) bool {
	if !dberr.IsUniqueViolation(err) {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "slug") || strings.Contains(s, "idx_studio_projects_owner_slug")
}

func (s *gormStore) Put(ctx context.Context, p Project) error {
	slug := strings.ToLower(strings.TrimSpace(p.Slug))
	if slug == "" {
		return fmt.Errorf("%w: slug is empty", ErrInvalid)
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var other int64
		if err := tx.Model(&projectRow{}).Where("id = ? AND owner <> ?", p.ID, p.Owner).Count(&other).Error; err != nil {
			return err
		}
		if other > 0 {
			return errIDInUse
		}
		var n int64
		if err := tx.Model(&projectRow{}).Where("owner = ? AND slug = ? AND id <> ?", p.Owner, slug, p.ID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrNameTaken
		}
		return tx.Save(&projectRow{ID: p.ID, Owner: p.Owner, Slug: slug, Name: p.Name, Node: p.Node,
			Status: string(p.Status), Error: p.Error, Updated: p.Updated}).Error
	})
	if isSlugTaken(err) {
		return ErrNameTaken
	}
	return err
}

func (s *gormStore) Get(ctx context.Context, owner, id string) (Project, error) {
	var r projectRow
	err := s.db.WithContext(ctx).Where("id = ? AND owner = ?", id, owner).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return r.project(), nil
}

func (s *gormStore) List(ctx context.Context, owner string) ([]Project, error) {
	var rows []projectRow
	if err := s.db.WithContext(ctx).Where("owner = ?", owner).Order("name, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.project())
	}
	return out, nil
}

func (s *gormStore) Delete(ctx context.Context, owner, id string) error {
	return s.db.WithContext(ctx).Where("id = ? AND owner = ?", id, owner).Delete(&projectRow{}).Error
}
