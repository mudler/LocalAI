package mcpconns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type connectorRow struct {
	ID         string `gorm:"primaryKey;size:64"`
	Owner      string `gorm:"index;size:255;not null;uniqueIndex:idx_studio_mcp_owner_name"`
	Name       string `gorm:"size:255;uniqueIndex:idx_studio_mcp_owner_name"`
	Transport  string `gorm:"size:16"`
	URL        string
	Command    string
	Args       string
	Secrets    []byte
	HasToken   bool
	HasHeaders bool
	HasEnv     bool
	Enabled    bool
	Created    time.Time
	Updated    time.Time
}

func (connectorRow) TableName() string { return "studio_mcp_connectors" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its table.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("mcpconns: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&connectorRow{}); err != nil {
		return nil, fmt.Errorf("mcpconns: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (r connectorRow) connector() (Connector, error) {
	c := Connector{ID: r.ID, Owner: r.Owner, Name: r.Name, Transport: r.Transport, URL: r.URL, Command: r.Command,
		Secrets: append([]byte(nil), r.Secrets...), HasToken: r.HasToken, HasHeaders: r.HasHeaders, HasEnv: r.HasEnv,
		Enabled: r.Enabled, Created: r.Created.UTC(), Updated: r.Updated.UTC()}
	if r.Args != "" {
		if err := json.Unmarshal([]byte(r.Args), &c.Args); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (s *gormStore) Put(ctx context.Context, c Connector) error {
	args := ""
	if len(c.Args) > 0 {
		raw, _ := json.Marshal(c.Args)
		args = string(raw)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&connectorRow{}).Where("owner = ? AND name = ? AND id <> ?", c.Owner, c.Name, c.ID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrNameTaken
		}
		return tx.Save(&connectorRow{ID: c.ID, Owner: c.Owner, Name: c.Name, Transport: c.Transport, URL: c.URL, Command: c.Command,
			Args: args, Secrets: c.Secrets, HasToken: c.HasToken, HasHeaders: c.HasHeaders, HasEnv: c.HasEnv, Enabled: c.Enabled,
			Created: c.Created, Updated: c.Updated}).Error
	})
}

func (s *gormStore) Get(ctx context.Context, id string) (Connector, error) {
	var r connectorRow
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Connector{}, ErrNotFound
	}
	if err != nil {
		return Connector{}, err
	}
	return r.connector()
}

func (s *gormStore) List(ctx context.Context, owner string) ([]Connector, error) {
	var rows []connectorRow
	if err := s.db.WithContext(ctx).Where("owner = ?", owner).Order("name, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Connector, 0, len(rows))
	for _, r := range rows {
		c, err := r.connector()
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *gormStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&connectorRow{}).Error
}
