package agentpool

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/LocalAGI/webui/collections"
	"github.com/mudler/LocalAI/core/http/auth"
)

// Resolve on each operation so edits and permission changes apply to existing
// collections. The callback must not hold the pool or user-services mutex.
func (s *AgentPoolService) collectionModelSettings(owner string) func(string) (collections.CollectionModelSettings, error) {
	return func(collection string) (collections.CollectionModelSettings, error) {
		userID, name := owner, collection
		if prefix, agentName, namespaced := strings.Cut(collection, ":"); namespaced {
			if owner != "" && prefix != owner {
				return collections.CollectionModelSettings{}, fmt.Errorf("collection belongs to another user")
			}
			userID, name = prefix, agentName
		}
		settings := collections.CollectionModelSettings{}
		if s.configBackend == nil {
			return settings, fmt.Errorf("agent configuration unavailable")
		}
		cfg := s.configBackend.GetConfig(userID, name)
		if cfg == nil {
			// Embedded RAG normalizes storage names. Resolve REST requests only when
			// that normalized name identifies one agent in this user's namespace.
			var match *state.AgentConfig
			for candidate := range s.configBackend.ListAgents(userID) {
				if strings.TrimSpace(strings.ToLower(candidate)) == name {
					if match != nil {
						return settings, fmt.Errorf("ambiguous agent collection %q", name)
					}
					match = s.configBackend.GetConfig(userID, candidate)
				}
			}
			cfg = match
		}
		if cfg == nil {
			return settings, nil
		}
		settings.EmbeddingModel, settings.RerankerModel = cfg.EmbeddingModel, cfg.RerankerModel
		if err := s.checkCollectionModels(userID, settings); err != nil {
			return collections.CollectionModelSettings{}, err
		}
		return settings, nil
	}
}

func (s *AgentPoolService) checkCollectionModels(userID string, settings collections.CollectionModelSettings) error {
	if userID == "" || s.users.authDB == nil || (settings.EmbeddingModel == "" && settings.RerankerModel == "") {
		return nil
	}
	var user auth.User
	if err := s.users.authDB.Where("id = ?", userID).First(&user).Error; err != nil {
		return fmt.Errorf("check collection model access: %w", err)
	}
	if user.Role == auth.RoleAdmin {
		return nil
	}
	permissions, err := auth.GetUserPermissions(s.users.authDB, userID)
	if err != nil {
		return fmt.Errorf("check collection model permissions: %w", err)
	}
	if !permissions.AllowedModels.Enabled {
		return nil
	}
	for _, model := range []string{settings.EmbeddingModel, settings.RerankerModel} {
		if model != "" && !slices.Contains(permissions.AllowedModels.Models, model) {
			return fmt.Errorf("model %q is not allowed for this user", model)
		}
	}
	return nil
}
