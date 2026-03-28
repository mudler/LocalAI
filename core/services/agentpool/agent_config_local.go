package agentpool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/sse"
	"github.com/mudler/LocalAGI/core/state"
	agiServices "github.com/mudler/LocalAGI/services"
)

// localAgentConfigBackend wraps the in-memory LocalAGI AgentPool for standalone mode.
type localAgentConfigBackend struct {
	svc *AgentPoolService // back-reference for shared fields (pool, configMeta, outputsDir, etc.)
}

func newLocalAgentConfigBackend(svc *AgentPoolService) *localAgentConfigBackend {
	return &localAgentConfigBackend{svc: svc}
}

func (b *localAgentConfigBackend) ListAgents(userID string) map[string]bool {
	statuses := map[string]bool{}
	agents := b.svc.pool.List()
	prefix := ""
	if userID != "" {
		prefix = userID + ":"
	}
	for _, a := range agents {
		if userID != "" && !strings.HasPrefix(a, prefix) {
			continue
		}
		ag := b.svc.pool.GetAgent(a)
		if ag == nil {
			continue
		}
		displayName := a
		if prefix != "" {
			displayName = strings.TrimPrefix(a, prefix)
		}
		statuses[displayName] = !ag.Paused()
	}
	return statuses
}

func (b *localAgentConfigBackend) GetConfig(userID, name string) *state.AgentConfig {
	cfg := b.svc.pool.GetConfig(agents.AgentKey(userID, name))
	if cfg == nil {
		return nil
	}
	// Return a copy with the original name (strip userID: prefix)
	result := *cfg
	result.Name = name
	return &result
}

func (b *localAgentConfigBackend) SaveConfig(userID string, cfg *state.AgentConfig) error {
	key := agents.AgentKey(userID, cfg.Name)
	cfg.Name = key
	return b.svc.pool.CreateAgent(key, cfg)
}

func (b *localAgentConfigBackend) UpdateConfig(userID, name string, cfg *state.AgentConfig) error {
	key := agents.AgentKey(userID, name)
	if old := b.svc.pool.GetConfig(key); old == nil {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	cfg.Name = key
	return b.svc.pool.RecreateAgent(key, cfg)
}

func (b *localAgentConfigBackend) DeleteConfig(userID, name string) error {
	return b.svc.pool.Remove(agents.AgentKey(userID, name))
}

func (b *localAgentConfigBackend) ImportConfig(userID string, cfg *state.AgentConfig) error {
	key := agents.AgentKey(userID, cfg.Name)
	cfg.Name = key
	return b.svc.pool.CreateAgent(key, cfg)
}

func (b *localAgentConfigBackend) ExportConfig(userID, name string) ([]byte, error) {
	cfg := b.svc.pool.GetConfig(agents.AgentKey(userID, name))
	if cfg == nil {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func (b *localAgentConfigBackend) SetStatus(userID, name, status string) error {
	ag := b.svc.pool.GetAgent(agents.AgentKey(userID, name))
	if ag == nil {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	switch status {
	case "paused":
		ag.Pause()
	case "active":
		ag.Resume()
	default:
		return fmt.Errorf("unknown status: %s", status)
	}
	return nil
}

func (b *localAgentConfigBackend) GetAgent(userID, name string) *agent.Agent {
	return b.svc.pool.GetAgent(agents.AgentKey(userID, name))
}

func (b *localAgentConfigBackend) GetSSEManager(userID, name string) sse.Manager {
	return b.svc.pool.GetManager(agents.AgentKey(userID, name))
}

func (b *localAgentConfigBackend) GetStatus(userID, name string) *state.Status {
	return b.svc.pool.GetStatusHistory(agents.AgentKey(userID, name))
}

func (b *localAgentConfigBackend) GetObservables(userID, name string) ([]json.RawMessage, error) {
	ag := b.svc.pool.GetAgent(agents.AgentKey(userID, name))
	if ag == nil {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	history := ag.Observer().History()
	result := make([]json.RawMessage, 0, len(history))
	for _, obs := range history {
		data, err := json.Marshal(obs)
		if err != nil {
			continue
		}
		result = append(result, data)
	}
	return result, nil
}

func (b *localAgentConfigBackend) ClearObservables(userID, name string) error {
	ag := b.svc.pool.GetAgent(agents.AgentKey(userID, name))
	if ag == nil {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	ag.Observer().ClearHistory()
	return nil
}

func (b *localAgentConfigBackend) ListAllGrouped() map[string][]UserAgentInfo {
	result := map[string][]UserAgentInfo{}
	agents := b.svc.pool.List()
	for _, a := range agents {
		ag := b.svc.pool.GetAgent(a)
		if ag == nil {
			continue
		}
		userID := ""
		name := a
		if u, n, ok := strings.Cut(a, ":"); ok {
			userID = u
			name = n
		}
		result[userID] = append(result[userID], UserAgentInfo{
			Name:   name,
			Active: !ag.Paused(),
		})
	}
	return result
}

func (b *localAgentConfigBackend) GetConfigMeta() AgentConfigMetaResult {
	meta := b.svc.configMeta
	return AgentConfigMetaResult{
		Fields:     meta.Fields,
		Actions:    meta.Actions,
		Connectors: meta.Connectors,
		Filters:    meta.Filters,
		OutputsDir: b.svc.outputsDir,
	}
}

func (b *localAgentConfigBackend) ListAvailableActions() []string {
	return agiServices.AvailableActions
}

func (b *localAgentConfigBackend) Chat(userID, name, message string) (string, error) {
	return b.svc.Chat(agents.AgentKey(userID, name), message)
}

func (b *localAgentConfigBackend) Stop() {
	if b.svc.pool != nil {
		b.svc.pool.StopAll()
	}
}
