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
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/internal/dberr"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
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
	Network      string `gorm:"size:16"`
	// Memory came later: empty for the agents made before, which read as
	// "as before".
	Memory string `gorm:"size:16"`
	// MemorySave came later: empty uses the person's setting.
	MemorySave string `gorm:"size:16"`
	// FolderID and FolderMode came after the table existed: the defaults
	// fill the rows of agents made before, which have no folder, so
	// AutoMigrate is their migration.
	FolderID   int64  `gorm:"not null;default:0"`
	FolderMode string `gorm:"size:8;not null;default:''"`
	// BuiltinsOff and BuiltinsOn came later too: JSON lists, empty for the
	// agents made before.
	BuiltinsOff string
	BuiltinsOn  string
	// Builtins (the grants) came after them: a JSON list, empty before.
	Builtins string
	// Connections is the JSON object of the connection the agent names for
	// each credentialed add-on; empty for the agents made before.
	Connections string
	// Resources is the JSON object of the resources the agent may use for each
	// add-on with per-agent resources; empty for the agents made before.
	Resources string
	Created   time.Time
	Updated   time.Time
}

func (agentRow) TableName() string { return "studio_agents" }

// folderRow is one folder of an agent, at position Pos in its list. The pair
// (AgentID, Pos) is the key, so two frontends that run the startup migration
// at once cannot make a folder twice.
type folderRow struct {
	AgentID string `gorm:"primaryKey;size:64"`
	Pos     int    `gorm:"primaryKey;autoIncrement:false"`
	Node    int64  `gorm:"not null;default:0"`
	Mode    string `gorm:"size:8;not null;default:''"`
}

func (folderRow) TableName() string { return "studio_agent_folders" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its table.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("agents: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&agentRow{}, &folderRow{}); err != nil {
		return nil, fmt.Errorf("agents: migrate: %w", err)
	}
	if err := migrateScalarFolders(db); err != nil {
		return nil, fmt.Errorf("agents: migrate folders: %w", err)
	}
	return &gormStore{db: db}, nil
}

// migrateScalarFolders copies the one folder that an agent kept in the columns
// folder_id and folder_mode into the folder table. The columns stay as they
// are for one release, so a rollback works. It is safe to run on every start
// and from several frontends at once: it only adds the first row of an agent
// that has none, and a clash on the key is ignored.
func migrateScalarFolders(db *gorm.DB) error {
	var rows []agentRow
	if err := db.Where("folder_mode <> ''").
		Where("id NOT IN (?)", db.Model(&folderRow{}).Select("agent_id")).
		Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		err := db.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&folderRow{AgentID: r.ID, Pos: 0, Node: r.FolderID, Mode: r.FolderMode}).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func listJSON(l []string) string {
	if len(l) == 0 {
		return ""
	}
	raw, _ := json.Marshal(l)
	return string(raw)
}

func connectionsJSON(m connsets.Sets) string {
	if len(m) == 0 {
		return ""
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

func resourcesJSON(m resourcesets.Sets) string {
	if len(m) == 0 {
		return ""
	}
	raw, _ := json.Marshal(m)
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

func (r agentRow) agent(folders []folderRow) (Agent, error) {
	a := Agent{ID: r.ID, Principal: r.Principal, Name: r.Name, Description: r.Description,
		Instructions: r.Instructions, Model: r.Model, Network: r.Network, Memory: r.Memory, MemorySave: r.MemorySave, FolderID: r.FolderID, FolderMode: r.FolderMode, Created: r.Created, Updated: r.Updated}
	for _, f := range folders {
		a.Folders = append(a.Folders, AgentFolder{Node: f.Node, Mode: f.Mode})
	}
	var err error
	if r.Connections != "" {
		if err = json.Unmarshal([]byte(r.Connections), &a.Connections); err != nil {
			return a, err
		}
	}
	if r.Resources != "" {
		if err = json.Unmarshal([]byte(r.Resources), &a.Resources); err != nil {
			return a, err
		}
	}
	for _, f := range []struct {
		dst *[]string
		src string
	}{{&a.MCPServers, r.MCPServers}, {&a.Addons, r.Addons}, {&a.Collections, r.Collections}, {&a.Skills, r.Skills}, {&a.BuiltinsOff, r.BuiltinsOff}, {&a.BuiltinsOn, r.BuiltinsOn}, {&a.Builtins, r.Builtins}} {
		if *f.dst, err = listFromJSON(f.src); err != nil {
			return a, err
		}
	}
	return a.normalizeFolders(), nil
}

// foldersOf loads the folder rows of the agents, by agent ID, in list order.
func foldersOf(db *gorm.DB, ids []string) (map[string][]folderRow, error) {
	out := map[string][]folderRow{}
	for len(ids) > 0 {
		n := min(len(ids), 500)
		var rows []folderRow
		if err := db.Where("agent_id IN ?", ids[:n]).Order("agent_id, pos").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.AgentID] = append(out[r.AgentID], r)
		}
		ids = ids[n:]
	}
	return out, nil
}

func (s *gormStore) agentsOf(rows []agentRow) ([]Agent, error) {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	fs, err := foldersOf(s.db, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(rows))
	for _, r := range rows {
		a, err := r.agent(fs[r.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// isNameTaken reports a unique violation of the name index. A collision on the
// primary key is a unique violation too, but it is not a name that is taken.
// SQLite names the columns of the index and PostgreSQL names the index.
func isNameTaken(err error) bool {
	if !dberr.IsUniqueViolation(err) {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "name_key") || strings.Contains(s, "idx_studio_agents_name")
}

func (s *gormStore) Put(ctx context.Context, a Agent) error {
	a = a.normalizeFolders()
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
			Collections: listJSON(a.Collections), Skills: listJSON(a.Skills), Network: a.Network, Memory: a.Memory, MemorySave: a.MemorySave,
			FolderID: a.FolderID, FolderMode: a.FolderMode, BuiltinsOff: listJSON(a.BuiltinsOff), BuiltinsOn: listJSON(a.BuiltinsOn), Builtins: listJSON(a.Builtins),
			Connections: connectionsJSON(a.Connections), Resources: resourcesJSON(a.Resources),
			Created: a.Created, Updated: a.Updated}
		if exists {
			row.Created = old.Created
			err = tx.Save(&row).Error
		} else {
			err = tx.Create(&row).Error
		}
		if err != nil && isNameTaken(err) {
			return ErrNameTaken
		}
		if err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", a.ID).Delete(&folderRow{}).Error; err != nil {
			return err
		}
		for i, f := range a.Folders {
			if err := tx.Create(&folderRow{AgentID: a.ID, Pos: i, Node: f.Node, Mode: f.Mode}).Error; err != nil {
				return err
			}
		}
		return nil
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
	fs, err := foldersOf(s.db.WithContext(ctx), []string{r.ID})
	if err != nil {
		return Agent{}, err
	}
	return r.agent(fs[r.ID])
}

func (s *gormStore) List(ctx context.Context, principal string) ([]Agent, error) {
	var rows []agentRow
	if err := s.db.WithContext(ctx).Where("principal = ?", principal).Order("name_key, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out, err := s.agentsOf(rows)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return NameKey(out[i].Name) < NameKey(out[j].Name) })
	return out, nil
}

func (s *gormStore) ListAll(ctx context.Context) ([]Agent, error) {
	var rows []agentRow
	if err := s.db.WithContext(ctx).Order("principal, name_key, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return s.agentsOf(rows)
}

func (s *gormStore) Delete(ctx context.Context, principal, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND principal = ?", id, principal).Delete(&agentRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Where("agent_id = ?", id).Delete(&folderRow{}).Error
	})
}
