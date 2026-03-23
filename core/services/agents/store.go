package agents

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// AgentConfigRecord persists agent configuration in PostgreSQL.
type AgentConfigRecord struct {
	ID         string     `gorm:"primaryKey;size:36" json:"id"`
	UserID     string     `gorm:"index;size:36" json:"user_id"`
	Name       string     `gorm:"size:255;index" json:"name"`
	ConfigJSON string     `gorm:"column:config;type:text" json:"-"`  // Full agent config as JSON
	Status     string     `gorm:"size:32;default:active" json:"status"` // active, paused, deleted
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`               // Last autonomous/background run
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (AgentConfigRecord) TableName() string { return "agent_configs" }

// AgentObservableRecord persists agent action traces (reasoning, tool calls, etc.).
type AgentObservableRecord struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	AgentName  string    `gorm:"index;size:255" json:"agent_name"`
	EventType  string    `gorm:"size:64" json:"event_type"` // status, action, error
	PayloadJSON string   `gorm:"column:payload;type:text" json:"-"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

func (AgentObservableRecord) TableName() string { return "agent_observables" }

// AgentStore provides PostgreSQL-backed persistence for agent state.
type AgentStore struct {
	db *gorm.DB
}

// NewAgentStore creates a new AgentStore and auto-migrates the schema.
func NewAgentStore(db *gorm.DB) (*AgentStore, error) {
	if err := db.AutoMigrate(&AgentConfigRecord{}, &AgentObservableRecord{}); err != nil {
		return nil, fmt.Errorf("migrating agent tables: %w", err)
	}
	return &AgentStore{db: db}, nil
}

// --- Agent Config CRUD ---

// SaveConfig creates or updates an agent config.
func (s *AgentStore) SaveConfig(cfg *AgentConfigRecord) error {
	if cfg.ID == "" {
		cfg.ID = uuid.New().String()
	}
	cfg.UpdatedAt = time.Now()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = cfg.UpdatedAt
	}

	var existing AgentConfigRecord
	err := s.db.Where("user_id = ? AND name = ?", cfg.UserID, cfg.Name).First(&existing).Error
	if err == nil {
		cfg.ID = existing.ID
		cfg.CreatedAt = existing.CreatedAt
		return s.db.Model(&existing).Updates(cfg).Error
	}
	return s.db.Create(cfg).Error
}

// GetConfig retrieves an agent config by user and name.
func (s *AgentStore) GetConfig(userID, name string) (*AgentConfigRecord, error) {
	var cfg AgentConfigRecord
	q := s.db.Where("name = ?", name)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if err := q.First(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetConfigByID retrieves an agent config by ID.
func (s *AgentStore) GetConfigByID(id string) (*AgentConfigRecord, error) {
	var cfg AgentConfigRecord
	if err := s.db.First(&cfg, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ListConfigs returns all agent configs for a user.
func (s *AgentStore) ListConfigs(userID string) ([]AgentConfigRecord, error) {
	var configs []AgentConfigRecord
	q := s.db.Where("status != ?", "deleted").Order("name")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if err := q.Find(&configs).Error; err != nil {
		return nil, err
	}
	return configs, nil
}

// DeleteConfig soft-deletes an agent config.
func (s *AgentStore) DeleteConfig(userID, name string) error {
	return s.db.Model(&AgentConfigRecord{}).
		Where("user_id = ? AND name = ?", userID, name).
		Update("status", "deleted").Error
}

// HardDeleteConfig permanently removes an agent config.
func (s *AgentStore) HardDeleteConfig(id string) error {
	return s.db.Where("id = ?", id).Delete(&AgentConfigRecord{}).Error
}

// UpdateStatus updates the status of an agent (active, paused).
func (s *AgentStore) UpdateStatus(userID, name, status string) error {
	return s.db.Model(&AgentConfigRecord{}).
		Where("user_id = ? AND name = ?", userID, name).
		Updates(map[string]any{"status": status, "updated_at": time.Now()}).Error
}

// --- Conversation History ---

// UpdateLastRun updates the last autonomous run timestamp.
func (s *AgentStore) UpdateLastRun(userID, name string) error {
	now := time.Now()
	return s.db.Model(&AgentConfigRecord{}).
		Where("user_id = ? AND name = ?", userID, name).
		Update("last_run_at", &now).Error
}

// --- Observables ---

// AppendObservable adds an observable event.
func (s *AgentStore) AppendObservable(obs *AgentObservableRecord) error {
	if obs.ID == "" {
		obs.ID = uuid.New().String()
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = time.Now()
	}
	return s.db.Create(obs).Error
}

// GetObservables retrieves observables for an agent.
func (s *AgentStore) GetObservables(agentName string, limit int) ([]AgentObservableRecord, error) {
	var obs []AgentObservableRecord
	q := s.db.Where("agent_name = ?", agentName).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&obs).Error; err != nil {
		return nil, err
	}
	return obs, nil
}

// ClearObservables deletes all observables for an agent.
func (s *AgentStore) ClearObservables(agentName string) error {
	return s.db.Where("agent_name = ?", agentName).Delete(&AgentObservableRecord{}).Error
}

// DB returns the underlying database connection (for advisory locks etc.)
func (s *AgentStore) DB() *gorm.DB {
	return s.db
}

// --- Helpers ---

// NewAgentStoreFromURL creates an AgentStore by connecting to the given PostgreSQL URL.
func NewAgentStoreFromURL(dbURL string) (*AgentStore, error) {
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return NewAgentStore(db)
}

// ParseConfigJSON unmarshals a JSON string into an AgentConfig.
func ParseConfigJSON(configJSON string, cfg *AgentConfig) error {
	return json.Unmarshal([]byte(configJSON), cfg)
}
