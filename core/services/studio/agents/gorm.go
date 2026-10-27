package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// agentRow is the table of saved agents. The lists are JSON text so one schema
// works on SQLite and PostgreSQL. NameKey is the lower-cased name: the unique
// index on (principal, name_key) is what keeps names apart under a race.
type agentRow struct {
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
	Created      time.Time
	Updated      time.Time
}

func (agentRow) TableName() string { return "studio_agents" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its table.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("agents: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&agentRow{}); err != nil {
		return nil, fmt.Errorf("agents: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func listJSON(l []string) string {
	if len(l) == 0 {
		return ""
	}
	raw, _ := json.Marshal(l)
	return string(raw)
}

func listFromJSON(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var l []string
	err := json.Unmarshal([]byte(s), &l)
	return l, err
}

func (r agentRow) agent() (Agent, error) {
	a := Agent{ID: r.ID, Principal: r.Principal, Name: r.Name, Description: r.Description,
		Instructions: r.Instructions, Model: r.Model, Created: r.Created, Updated: r.Updated}
	var err error
	for _, f := range []struct {
		dst *[]string
		src string
	}{{&a.MCPServers, r.MCPServers}, {&a.Addons, r.Addons}, {&a.Collections, r.Collections}, {&a.Skills, r.Skills}} {
		if *f.dst, err = listFromJSON(f.src); err != nil {
			return a, err
		}
	}
	return a, nil
}

func isUniqueViolation(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate key")
}

func (s *gormStore) Put(ctx context.Context, a Agent) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old agentRow
		err := tx.Where("id = ?", a.ID).Take(&old).Error
		exists := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if exists && old.Principal != a.Principal {
			return ErrNotFound
		}
		row := agentRow{ID: a.ID, Principal: a.Principal, NameKey: NameKey(a.Name), Name: a.Name,
			Description: a.Description, Instructions: a.Instructions, Model: a.Model,
			MCPServers: listJSON(a.MCPServers), Addons: listJSON(a.Addons),
			Collections: listJSON(a.Collections), Skills: listJSON(a.Skills),
			Created: a.Created, Updated: a.Updated}
		if exists {
			row.Created = old.Created
			err = tx.Save(&row).Error
		} else {
			err = tx.Create(&row).Error
		}
		if err != nil && isUniqueViolation(err) {
			return ErrNameTaken
		}
		return err
	})
}

func (s *gormStore) Get(ctx context.Context, principal, id string) (Agent, error) {
	var r agentRow
	err := s.db.WithContext(ctx).Where("id = ? AND principal = ?", id, principal).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	return r.agent()
}

func (s *gormStore) List(ctx context.Context, principal string) ([]Agent, error) {
	var rows []agentRow
	if err := s.db.WithContext(ctx).Where("principal = ?", principal).Order("name_key, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(rows))
	for _, r := range rows {
		a, err := r.agent()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool { return NameKey(out[i].Name) < NameKey(out[j].Name) })
	return out, nil
}

func (s *gormStore) ListAll(ctx context.Context) ([]Agent, error) {
	var rows []agentRow
	if err := s.db.WithContext(ctx).Order("principal, name_key, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(rows))
	for _, r := range rows {
		a, err := r.agent()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *gormStore) Delete(ctx context.Context, principal, id string) error {
	res := s.db.WithContext(ctx).Where("id = ? AND principal = ?", id, principal).Delete(&agentRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
