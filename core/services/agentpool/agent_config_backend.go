package agentpool

import (
	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/sse"
	"github.com/mudler/LocalAGI/core/state"
)

// AgentConfigBackend abstracts agent config storage and runtime queries
// between local (in-memory pool) and distributed (PostgreSQL + NATS) modes.
// Each method corresponds to a previously-branched operation in AgentPoolService.
type AgentConfigBackend interface {
	// CRUD operations
	ListAgents(userID string) map[string]bool
	GetConfig(userID, name string) *state.AgentConfig
	SaveConfig(userID string, cfg *state.AgentConfig) error
	UpdateConfig(userID, name string, cfg *state.AgentConfig) error
	DeleteConfig(userID, name string) error
	ImportConfig(userID string, cfg *state.AgentConfig) error
	ExportConfig(userID, name string) ([]byte, error)

	// Status operations
	SetStatus(userID, name, status string) error // "active" or "paused"

	// Runtime queries — local mode returns real data, distributed mode returns nil/empty
	GetAgent(userID, name string) *agent.Agent
	GetSSEManager(userID, name string) sse.Manager
	GetStatus(userID, name string) *state.Status
	GetObservables(userID, name string) (any, error)
	ClearObservables(userID, name string) error

	// Admin aggregation
	ListAllGrouped() map[string][]UserAgentInfo

	// Config metadata for UI
	GetConfigMeta() AgentConfigMetaResult

	// Actions
	ListAvailableActions() []string

	// Chat dispatch
	Chat(userID, name, message string) (string, error)

	// Stop / cleanup
	Stop()
}

// AgentConfigMetaResult holds the config metadata response for the UI.
// In local mode, it comes from LocalAGI's AgentConfigMeta.
// In distributed mode, it comes from the native agents.DefaultConfigMeta().
type AgentConfigMetaResult struct {
	Fields      any    `json:"Fields"`
	Actions     any    `json:"Actions"`
	Connectors  any    `json:"Connectors"`
	Filters     any    `json:"Filters"`
	OutputsDir  string `json:"OutputsDir"`
	Distributed bool   `json:"distributed,omitempty"`
}
