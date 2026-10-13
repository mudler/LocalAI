// SPDX-License-Identifier: MIT
package agentpool

import (
	"strings"

	skillsManager "github.com/mudler/LocalAI/core/services/skills"
	skillgit "github.com/mudler/skillserver/pkg/git"
	"github.com/mudler/xlog"
)

func (s *AgentPoolService) warnLegacyAuthData() {
	if !s.appConfig.Auth.Enabled {
		return
	}

	rootAgents := 0
	for _, name := range s.localAGI.pool.List() {
		if !strings.Contains(name, ":") {
			rootAgents++
		}
	}
	rootCollections, err := s.collectionsBackend.ListCollections()
	if err != nil {
		xlog.Warn("Could not check root collections after enabling user authentication", "error", err)
	}

	skillCount, repoCount := 0, 0
	if s.localAGI.skillsService != nil {
		manager := skillsManager.NewFilesystemManager(s.localAGI.skillsService)
		rootSkills, err := manager.List()
		if err != nil {
			xlog.Warn("Could not check root skills after enabling user authentication", "error", err)
		} else {
			skillCount = len(rootSkills)
		}
		// Count disabled and not-yet-cloned repositories too; the skill index
		// contains only skills that are already available on disk.
		repos, err := skillgit.NewConfigManager(s.localAGI.skillsService.GetSkillsDir()).LoadConfig()
		if err != nil {
			xlog.Warn("Could not check root skill repositories after enabling user authentication", "error", err)
		} else {
			repoCount = len(repos)
		}
	}

	if rootAgents+len(rootCollections)+skillCount+repoCount == 0 {
		return
	}
	xlog.Warn("User authentication is enabled but root agent data has no owner",
		"agents", rootAgents, "collections", len(rootCollections), "skills", skillCount, "git_repositories", repoCount,
		"stateDir", s.stateDir,
		"details", "Existing data is not migrated to a user. Root agents still start and may run scheduled tasks; their API calls require valid credentials.",
		"docs", "https://localai.io/features/authentication/#enabling-auth-on-an-existing-instance")
}
