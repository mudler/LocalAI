package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAGI/services/skills"
	skillserver "github.com/mudler/skillserver/pkg/domain"
	skillgit "github.com/mudler/skillserver/pkg/git"
	"github.com/mudler/xlog"
)

// FilesystemManager implements Manager using the local filesystem.
// Used in standalone mode (no PostgreSQL).
type FilesystemManager struct {
	svc *skills.Service
}

// NewFilesystemManager creates a filesystem-backed skill manager.
func NewFilesystemManager(svc *skills.Service) *FilesystemManager {
	return &FilesystemManager{svc: svc}
}

func (m *FilesystemManager) fsManager() (*skillserver.FileSystemManager, error) {
	if m.svc == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := m.svc.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	fsm, ok := mgr.(*skillserver.FileSystemManager)
	if !ok {
		return nil, fmt.Errorf("unsupported manager type")
	}
	return fsm, nil
}

func (m *FilesystemManager) manager() (skillserver.SkillManager, error) {
	if m.svc == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := m.svc.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	return mgr, nil
}

// --- Skills CRUD ---

func (m *FilesystemManager) List() ([]skillserver.Skill, error) {
	mgr, err := m.manager()
	if err != nil {
		return nil, err
	}
	return mgr.ListSkills()
}

func (m *FilesystemManager) Get(name string) (*skillserver.Skill, error) {
	mgr, err := m.manager()
	if err != nil {
		return nil, err
	}
	return mgr.ReadSkill(name)
}

func (m *FilesystemManager) Search(query string) ([]skillserver.Skill, error) {
	mgr, err := m.manager()
	if err != nil {
		return nil, err
	}
	return mgr.SearchSkills(query)
}

func (m *FilesystemManager) Create(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skillserver.Skill, error) {
	fsm, err := m.fsManager()
	if err != nil {
		return nil, err
	}
	if err := skillserver.ValidateSkillName(name); err != nil {
		return nil, err
	}

	skillDir := filepath.Join(fsm.GetSkillsDir(), name)
	if _, err := os.Stat(skillDir); err == nil {
		return nil, fmt.Errorf("skill already exists")
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return nil, err
	}

	fm := buildFrontmatter(name, description, license, compatibility, allowedTools, metadata)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(fm+content), 0644); err != nil {
		os.RemoveAll(skillDir)
		return nil, err
	}

	mgr, _ := m.manager()
	if err := mgr.RebuildIndex(); err != nil {
		return nil, fmt.Errorf("failed to rebuild index: %w", err)
	}
	return mgr.ReadSkill(name)
}

func (m *FilesystemManager) Update(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skillserver.Skill, error) {
	fsm, err := m.fsManager()
	if err != nil {
		return nil, err
	}
	mgr, _ := m.manager()

	existing, err := mgr.ReadSkill(name)
	if err != nil {
		return nil, fmt.Errorf("skill not found")
	}
	if existing.ReadOnly {
		return nil, fmt.Errorf("cannot update read-only skill from git repository")
	}

	skillDir := filepath.Join(fsm.GetSkillsDir(), name)
	fm := buildFrontmatter(name, description, license, compatibility, allowedTools, metadata)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(fm+content), 0644); err != nil {
		return nil, err
	}

	if err := mgr.RebuildIndex(); err != nil {
		return nil, fmt.Errorf("failed to rebuild index: %w", err)
	}
	return mgr.ReadSkill(name)
}

func (m *FilesystemManager) Delete(name string) error {
	fsm, err := m.fsManager()
	if err != nil {
		return err
	}
	mgr, _ := m.manager()

	existing, err := mgr.ReadSkill(name)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if existing.ReadOnly {
		return fmt.Errorf("cannot delete read-only skill from git repository")
	}

	skillDir := filepath.Join(fsm.GetSkillsDir(), name)
	if err := os.RemoveAll(skillDir); err != nil {
		return err
	}
	return mgr.RebuildIndex()
}

func (m *FilesystemManager) Export(name string) ([]byte, error) {
	fsm, err := m.fsManager()
	if err != nil {
		return nil, err
	}
	mgr, _ := m.manager()

	skill, err := mgr.ReadSkill(name)
	if err != nil {
		return nil, fmt.Errorf("skill not found")
	}
	return skillserver.ExportSkill(skill.ID, fsm.GetSkillsDir())
}

func (m *FilesystemManager) Import(archiveData []byte) (*skillserver.Skill, error) {
	fsm, err := m.fsManager()
	if err != nil {
		return nil, err
	}
	mgr, _ := m.manager()

	skillName, err := skillserver.ImportSkill(archiveData, fsm.GetSkillsDir())
	if err != nil {
		return nil, err
	}
	if err := mgr.RebuildIndex(); err != nil {
		return nil, fmt.Errorf("failed to rebuild index: %w", err)
	}
	return mgr.ReadSkill(skillName)
}

// --- Resources ---

func (m *FilesystemManager) ListResources(skillName string) ([]skillserver.SkillResource, *skillserver.Skill, error) {
	mgr, err := m.manager()
	if err != nil {
		return nil, nil, err
	}
	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return nil, nil, fmt.Errorf("skill not found")
	}
	resources, err := mgr.ListSkillResources(skill.ID)
	return resources, skill, err
}

func (m *FilesystemManager) GetResource(skillName, path string) (*skillserver.ResourceContent, *skillserver.SkillResource, error) {
	mgr, err := m.manager()
	if err != nil {
		return nil, nil, err
	}
	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return nil, nil, fmt.Errorf("skill not found")
	}
	info, err := mgr.GetSkillResourceInfo(skill.ID, path)
	if err != nil {
		return nil, nil, fmt.Errorf("resource not found")
	}
	content, err := mgr.ReadSkillResource(skill.ID, path)
	if err != nil {
		return nil, nil, err
	}
	return content, info, nil
}

func (m *FilesystemManager) CreateResource(skillName, path string, data []byte) error {
	if _, err := m.fsManager(); err != nil {
		return err
	}
	mgr, _ := m.manager()

	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if skill.ReadOnly {
		return fmt.Errorf("cannot modify read-only skill")
	}

	if err := skillserver.ValidateResourcePath(path); err != nil {
		return err
	}
	absPath := filepath.Join(skill.SourcePath, path)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(absPath, data, 0644)
}

func (m *FilesystemManager) UpdateResource(skillName, path, content string) error {
	_, err := m.fsManager()
	if err != nil {
		return err
	}
	mgr, _ := m.manager()

	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if skill.ReadOnly {
		return fmt.Errorf("cannot modify read-only skill")
	}

	if err := skillserver.ValidateResourcePath(path); err != nil {
		return err
	}
	absPath := filepath.Join(skill.SourcePath, path)
	return os.WriteFile(absPath, []byte(content), 0644)
}

func (m *FilesystemManager) DeleteResource(skillName, path string) error {
	_, err := m.fsManager()
	if err != nil {
		return err
	}
	mgr, _ := m.manager()

	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if skill.ReadOnly {
		return fmt.Errorf("cannot modify read-only skill")
	}

	if err := skillserver.ValidateResourcePath(path); err != nil {
		return err
	}
	absPath := filepath.Join(skill.SourcePath, path)
	return os.Remove(absPath)
}

// --- Git repos ---

func (m *FilesystemManager) ListGitRepos() ([]GitRepoInfo, error) {
	dir := m.GetSkillsDir()
	if dir == "" {
		return []GitRepoInfo{}, nil
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return []GitRepoInfo{}, nil
	}
	out := make([]GitRepoInfo, len(repos))
	for i, r := range repos {
		out[i] = GitRepoInfo{ID: r.ID, URL: r.URL, Name: r.Name, Enabled: r.Enabled}
	}
	return out, nil
}

func (m *FilesystemManager) AddGitRepo(repoURL string) (*GitRepoInfo, error) {
	dir := m.GetSkillsDir()
	if dir == "" {
		return nil, fmt.Errorf("skills directory not configured")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, _ := cm.LoadConfig()

	// Validate URL format
	if !strings.HasPrefix(repoURL, "http://") && !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "git@") {
		return nil, fmt.Errorf("invalid git URL: must start with http://, https://, or git@")
	}

	// Check for duplicate
	for _, r := range repos {
		if r.URL == repoURL {
			return nil, fmt.Errorf("repository already exists")
		}
	}

	newRepo := skillgit.GitRepoConfig{
		ID:      skillgit.GenerateID(repoURL),
		URL:     repoURL,
		Name:    skillgit.ExtractRepoName(repoURL),
		Enabled: true,
	}
	repos = append(repos, newRepo)
	if err := cm.SaveConfig(repos); err != nil {
		return nil, err
	}

	// Background sync
	go func() {
		mgr, err := m.manager()
		if err != nil {
			return
		}
		syncer := skillgit.NewGitSyncer(dir, []string{repoURL}, mgr.RebuildIndex)
		if err := syncer.Start(); err != nil {
			xlog.Error("background sync failed", "url", repoURL, "error", err)
			m.svc.RefreshManagerFromConfig()
			return
		}
		syncer.Stop()
		m.svc.RefreshManagerFromConfig()
	}()

	info := &GitRepoInfo{ID: newRepo.ID, URL: newRepo.URL, Name: newRepo.Name, Enabled: newRepo.Enabled}
	return info, nil
}

func (m *FilesystemManager) UpdateGitRepo(id, repoURL string, enabled *bool) (*GitRepoInfo, error) {
	dir := m.GetSkillsDir()
	if dir == "" {
		return nil, fmt.Errorf("skills directory not configured")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
	for i, r := range repos {
		if r.ID == id {
			if repoURL != "" {
				repos[i].URL = repoURL
			}
			if enabled != nil {
				repos[i].Enabled = *enabled
			}
			if err := cm.SaveConfig(repos); err != nil {
				return nil, err
			}
			m.svc.RefreshManagerFromConfig()
			return &GitRepoInfo{ID: repos[i].ID, URL: repos[i].URL, Name: repos[i].Name, Enabled: repos[i].Enabled}, nil
		}
	}
	return nil, fmt.Errorf("repository not found")
}

func (m *FilesystemManager) DeleteGitRepo(id string) error {
	dir := m.GetSkillsDir()
	if dir == "" {
		return fmt.Errorf("skills directory not configured")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return err
	}
	var updated []skillgit.GitRepoConfig
	var repoDir string
	for _, r := range repos {
		if r.ID == id {
			repoDir = filepath.Join(dir, r.Name)
		} else {
			updated = append(updated, r)
		}
	}
	if repoDir == "" {
		return fmt.Errorf("repository not found")
	}
	if err := cm.SaveConfig(updated); err != nil {
		return err
	}
	os.RemoveAll(repoDir)
	m.svc.RefreshManagerFromConfig()
	return nil
}

func (m *FilesystemManager) SyncGitRepo(id string) error {
	dir := m.GetSkillsDir()
	if dir == "" {
		return fmt.Errorf("skills directory not configured")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return err
	}
	var repoURL string
	for _, r := range repos {
		if r.ID == id {
			repoURL = r.URL
			break
		}
	}
	if repoURL == "" {
		return fmt.Errorf("repository not found")
	}

	mgr, err := m.manager()
	if err != nil {
		return err
	}
	go func() {
		syncer := skillgit.NewGitSyncer(dir, []string{repoURL}, mgr.RebuildIndex)
		if err := syncer.Start(); err != nil {
			xlog.Error("background sync failed", "id", id, "error", err)
			m.svc.RefreshManagerFromConfig()
			return
		}
		syncer.Stop()
		m.svc.RefreshManagerFromConfig()
	}()
	return nil
}

func (m *FilesystemManager) ToggleGitRepo(id string) (*GitRepoInfo, error) {
	dir := m.GetSkillsDir()
	if dir == "" {
		return nil, fmt.Errorf("skills directory not configured")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
	for i, r := range repos {
		if r.ID == id {
			repos[i].Enabled = !repos[i].Enabled
			if err := cm.SaveConfig(repos); err != nil {
				return nil, err
			}
			m.svc.RefreshManagerFromConfig()
			return &GitRepoInfo{ID: repos[i].ID, URL: repos[i].URL, Name: repos[i].Name, Enabled: repos[i].Enabled}, nil
		}
	}
	return nil, fmt.Errorf("repository not found")
}

// --- Config ---

func (m *FilesystemManager) GetConfig() map[string]string {
	if m.svc == nil {
		return nil
	}
	return map[string]string{"skills_dir": m.svc.GetSkillsDir()}
}

func (m *FilesystemManager) GetSkillsDir() string {
	if m.svc == nil {
		return ""
	}
	return m.svc.GetSkillsDir()
}

// Service returns the underlying skills.Service for advanced operations.
func (m *FilesystemManager) Service() *skills.Service {
	return m.svc
}

// --- Helpers ---

func buildFrontmatter(name, description, license, compatibility, allowedTools string, metadata map[string]string) string {
	fm := fmt.Sprintf("---\nname: %s\ndescription: %s\n", name, description)
	if license != "" {
		fm += fmt.Sprintf("license: %s\n", license)
	}
	if compatibility != "" {
		fm += fmt.Sprintf("compatibility: %s\n", compatibility)
	}
	if len(metadata) > 0 {
		fm += "metadata:\n"
		for k, v := range metadata {
			fm += fmt.Sprintf("  %s: %s\n", k, v)
		}
	}
	if allowedTools != "" {
		fm += fmt.Sprintf("allowed-tools: %s\n", allowedTools)
	}
	fm += "---\n\n"
	return fm
}
