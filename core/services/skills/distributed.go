package skills

import (
	"github.com/mudler/LocalAI/core/services/distributed"
	skillserver "github.com/mudler/skillserver/pkg/domain"
	"github.com/mudler/xlog"
)

// DistributedManager wraps FilesystemManager and syncs metadata to PostgreSQL.
// Used in distributed mode where agent workers need skills from the database.
//
// Write operations go to both filesystem (full content) and PostgreSQL (metadata).
// List reads from PostgreSQL (source of truth for agent execution).
// Read operations (Get, Search, Export, resources) read from filesystem (need full content).
type DistributedManager struct {
	*FilesystemManager
	store  *distributed.SkillStore
	userID string
}

// NewDistributedManager creates a distributed skill manager.
func NewDistributedManager(fs *FilesystemManager, store *distributed.SkillStore, userID string) *DistributedManager {
	return &DistributedManager{
		FilesystemManager: fs,
		store:             store,
		userID:            userID,
	}
}

// List returns skills from PostgreSQL (source of truth for agent execution).
// Falls back to filesystem if the store has no records.
func (m *DistributedManager) List() ([]skillserver.Skill, error) {
	if m.store == nil {
		return m.FilesystemManager.List()
	}

	// Read from PostgreSQL
	records, err := m.store.List(m.userID)
	if err != nil {
		xlog.Warn("Failed to list skills from store, falling back to filesystem", "error", err)
		return m.FilesystemManager.List()
	}

	// If PostgreSQL is empty, fall back to filesystem and sync
	if len(records) == 0 {
		fsSkills, err := m.FilesystemManager.List()
		if err != nil {
			return nil, err
		}
		// Sync filesystem skills to PostgreSQL
		for _, s := range fsSkills {
			m.persistMetadata(s.Name, "inline", "")
		}
		return fsSkills, nil
	}

	// Convert PostgreSQL records to skilldomain.Skill
	skills := make([]skillserver.Skill, 0, len(records))
	for _, r := range records {
		if !r.Enabled {
			continue
		}
		skills = append(skills, skillserver.Skill{
			Name:    r.Name,
			ID:      r.Name,
			Content: r.Definition,
			Metadata: &skillserver.SkillMetadata{
				Name:        r.Name,
				Description: r.Definition,
			},
		})
	}
	return skills, nil
}

// Create writes to filesystem and syncs metadata to PostgreSQL.
func (m *DistributedManager) Create(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skillserver.Skill, error) {
	skill, err := m.FilesystemManager.Create(name, description, content, license, compatibility, allowedTools, metadata)
	if err != nil {
		return nil, err
	}
	m.persistMetadata(name, "inline", "")
	return skill, nil
}

// Update writes to filesystem and syncs metadata to PostgreSQL.
func (m *DistributedManager) Update(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skillserver.Skill, error) {
	skill, err := m.FilesystemManager.Update(name, description, content, license, compatibility, allowedTools, metadata)
	if err != nil {
		return nil, err
	}
	m.persistMetadata(name, "inline", "")
	return skill, nil
}

// Delete removes from filesystem and PostgreSQL.
func (m *DistributedManager) Delete(name string) error {
	if err := m.FilesystemManager.Delete(name); err != nil {
		return err
	}
	m.removeMetadata(name)
	return nil
}

// Import writes to filesystem and syncs metadata to PostgreSQL.
func (m *DistributedManager) Import(archiveData []byte) (*skillserver.Skill, error) {
	skill, err := m.FilesystemManager.Import(archiveData)
	if err != nil {
		return nil, err
	}
	m.persistMetadata(skill.Name, "inline", "")
	return skill, nil
}

// AddGitRepo adds a git repo and syncs metadata to PostgreSQL.
func (m *DistributedManager) AddGitRepo(repoURL string) (*GitRepoInfo, error) {
	info, err := m.FilesystemManager.AddGitRepo(repoURL)
	if err != nil {
		return nil, err
	}
	m.persistMetadata(info.Name, "git", repoURL)
	return info, nil
}

// DeleteGitRepo removes a git repo and cleans up PostgreSQL.
func (m *DistributedManager) DeleteGitRepo(id string) error {
	// Get repo name before deleting
	repos, _ := m.FilesystemManager.ListGitRepos()
	var repoName string
	for _, r := range repos {
		if r.ID == id {
			repoName = r.Name
			break
		}
	}

	if err := m.FilesystemManager.DeleteGitRepo(id); err != nil {
		return err
	}

	if repoName != "" {
		m.removeMetadata(repoName)
	}
	return nil
}

// persistMetadata saves skill metadata to PostgreSQL (best-effort).
func (m *DistributedManager) persistMetadata(name, sourceType, sourceURL string) {
	if m.store == nil {
		return
	}

	// Read full definition from filesystem if available
	definition := ""
	if skill, err := m.FilesystemManager.Get(name); err == nil && skill != nil {
		definition = skill.Content
		if len(definition) > 500 {
			definition = definition[:500]
		}
	}

	rec := &distributed.SkillMetadataRecord{
		UserID:     m.userID,
		Name:       name,
		Definition: definition,
		SourceType: sourceType,
		SourceURL:  sourceURL,
		Enabled:    true,
	}
	if err := m.store.Save(rec); err != nil {
		xlog.Warn("Failed to persist skill metadata", "name", name, "error", err)
	}
}

// removeMetadata deletes skill metadata from PostgreSQL (best-effort).
func (m *DistributedManager) removeMetadata(name string) {
	if m.store == nil {
		return
	}
	if err := m.store.Delete(m.userID, name); err != nil {
		xlog.Warn("Failed to remove skill metadata", "name", name, "error", err)
	}
}
