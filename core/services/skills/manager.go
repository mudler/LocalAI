package skills

import (
	skillserver "github.com/mudler/skillserver/pkg/domain"
)

// GitRepoInfo describes a configured git repository for skill sourcing.
type GitRepoInfo struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Manager defines the interface for skill management operations.
// Two implementations exist:
//   - FilesystemManager: standalone mode, filesystem-only
//   - DistributedManager: distributed mode, filesystem + PostgreSQL sync
//
// Each instance is scoped to a specific user (or global if userID is empty).
type Manager interface {
	// Skills CRUD
	List() ([]skillserver.Skill, error)
	Get(name string) (*skillserver.Skill, error)
	Search(query string) ([]skillserver.Skill, error)
	Create(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skillserver.Skill, error)
	Update(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skillserver.Skill, error)
	Delete(name string) error
	Export(name string) ([]byte, error)
	Import(archiveData []byte) (*skillserver.Skill, error)

	// Resources
	ListResources(skillName string) ([]skillserver.SkillResource, *skillserver.Skill, error)
	GetResource(skillName, path string) (*skillserver.ResourceContent, *skillserver.SkillResource, error)
	CreateResource(skillName, path string, data []byte) error
	UpdateResource(skillName, path, content string) error
	DeleteResource(skillName, path string) error

	// Git repos
	ListGitRepos() ([]GitRepoInfo, error)
	AddGitRepo(repoURL string) (*GitRepoInfo, error)
	UpdateGitRepo(id, repoURL string, enabled *bool) (*GitRepoInfo, error)
	DeleteGitRepo(id string) error
	SyncGitRepo(id string) error
	ToggleGitRepo(id string) (*GitRepoInfo, error)

	// Config
	GetConfig() map[string]string
	GetSkillsDir() string
}
