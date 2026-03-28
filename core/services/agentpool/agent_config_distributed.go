package agentpool

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/sse"
	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/xlog"
)

const (
	defaultObservableLimit = 100
)

// distributedAgentConfigBackend wraps PostgreSQL (AgentStore) + NATS for distributed mode.
type distributedAgentConfigBackend struct {
	svc   *AgentPoolService    // back-reference for dispatchChat, eventBridge, etc.
	store *agents.AgentStore   // concrete store for full access (observables, etc.)
}

func newDistributedAgentConfigBackend(svc *AgentPoolService, store *agents.AgentStore) *distributedAgentConfigBackend {
	return &distributedAgentConfigBackend{svc: svc, store: store}
}

func (b *distributedAgentConfigBackend) ListAgents(userID string) map[string]bool {
	statuses := map[string]bool{}
	configs, err := b.store.ListConfigs(userID)
	if err != nil {
		xlog.Error("Failed to list agents from database", "error", err)
		return statuses
	}
	for _, cfg := range configs {
		statuses[cfg.Name] = cfg.Status == agents.StatusActive
	}
	return statuses
}

func (b *distributedAgentConfigBackend) GetConfig(userID, name string) *state.AgentConfig {
	rec, err := b.store.GetConfig(userID, name)
	if err != nil || rec == nil {
		return nil
	}
	var agentCfg state.AgentConfig
	if json.Unmarshal([]byte(rec.ConfigJSON), &agentCfg) != nil {
		return nil
	}
	return &agentCfg
}

func (b *distributedAgentConfigBackend) SaveConfig(userID string, cfg *state.AgentConfig) error {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal agent config: %w", err)
	}
	return b.store.SaveConfig(&agents.AgentConfigRecord{
		UserID:     userID,
		Name:       cfg.Name,
		ConfigJSON: string(configJSON),
		Status:     agents.StatusActive,
	})
}

func (b *distributedAgentConfigBackend) UpdateConfig(userID, name string, cfg *state.AgentConfig) error {
	// Check if agent exists
	if _, err := b.store.GetConfig(userID, name); err != nil {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal agent config: %w", err)
	}
	return b.store.SaveConfig(&agents.AgentConfigRecord{
		UserID:     userID,
		Name:       cfg.Name,
		ConfigJSON: string(configJSON),
		Status:     agents.StatusActive,
	})
}

func (b *distributedAgentConfigBackend) DeleteConfig(userID, name string) error {
	return b.store.DeleteConfig(userID, name)
}

func (b *distributedAgentConfigBackend) ImportConfig(userID string, cfg *state.AgentConfig) error {
	return b.SaveConfig(userID, cfg)
}

func (b *distributedAgentConfigBackend) ExportConfig(userID, name string) ([]byte, error) {
	rec, err := b.store.GetConfig(userID, name)
	if err != nil || rec == nil {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	// Return the raw config JSON (already properly formatted)
	var pretty json.RawMessage
	if err := json.Unmarshal([]byte(rec.ConfigJSON), &pretty); err == nil {
		return json.MarshalIndent(pretty, "", "  ")
	}
	return []byte(rec.ConfigJSON), nil
}

func (b *distributedAgentConfigBackend) SetStatus(userID, name, status string) error {
	return b.store.UpdateStatus(userID, name, status)
}

// GetAgent returns nil in distributed mode — agents don't run in-process.
func (b *distributedAgentConfigBackend) GetAgent(_, _ string) *agent.Agent {
	return nil
}

// GetSSEManager returns nil in distributed mode — SSE is handled by EventBridge.
func (b *distributedAgentConfigBackend) GetSSEManager(_, _ string) sse.Manager {
	return nil
}

// GetStatus returns nil in distributed mode — status is not tracked in-process.
func (b *distributedAgentConfigBackend) GetStatus(_, _ string) *state.Status {
	return nil
}

// GetObservables reads from the PostgreSQL observables table.
func (b *distributedAgentConfigBackend) GetObservables(userID, name string) ([]json.RawMessage, error) {
	records, err := b.store.GetObservables(agents.AgentKey(userID, name), defaultObservableLimit)
	if err != nil {
		return nil, err
	}
	result := make([]json.RawMessage, 0, len(records))
	for _, rec := range records {
		result = append(result, json.RawMessage(rec.PayloadJSON))
	}
	return result, nil
}

// ClearObservables clears observables from the PostgreSQL table.
func (b *distributedAgentConfigBackend) ClearObservables(userID, name string) error {
	return b.store.ClearObservables(agents.AgentKey(userID, name))
}

func (b *distributedAgentConfigBackend) ListAllGrouped() map[string][]UserAgentInfo {
	result := map[string][]UserAgentInfo{}
	configs, err := b.store.ListConfigs("")
	if err != nil {
		return result
	}
	for _, cfg := range configs {
		result[cfg.UserID] = append(result[cfg.UserID], UserAgentInfo{
			Name:   cfg.Name,
			Active: cfg.Status == agents.StatusActive,
		})
	}
	return result
}

func (b *distributedAgentConfigBackend) GetConfigMeta() AgentConfigMetaResult {
	meta := agents.DefaultConfigMeta()
	return AgentConfigMetaResult{
		Fields:      meta.Fields,
		Actions:     meta.Actions,
		Connectors:  meta.Connectors,
		Filters:     meta.Filters,
		OutputsDir:  "",
		Distributed: true,
	}
}

// ListAvailableActions returns empty in distributed mode — actions are configured as MCP tools per agent.
func (b *distributedAgentConfigBackend) ListAvailableActions() []string {
	return []string{}
}

func (b *distributedAgentConfigBackend) Chat(userID, name, message string) (string, error) {
	return b.svc.dispatchChat(userID, name, message)
}

// Stop is a no-op in distributed mode — no in-process pool to stop.
func (b *distributedAgentConfigBackend) Stop() {}
