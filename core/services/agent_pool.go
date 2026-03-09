package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/sse"
	"github.com/mudler/LocalAGI/core/state"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	agiServices "github.com/mudler/LocalAGI/services"
	"github.com/mudler/LocalAGI/services/skills"
	"github.com/mudler/LocalAGI/webui/collections"
	"github.com/mudler/xlog"

	skilldomain "github.com/mudler/skillserver/pkg/domain"
	skillgit "github.com/mudler/skillserver/pkg/git"
)

// AgentPoolService wraps LocalAGI's AgentPool, Skills service, and collections backend
// to provide agentic capabilities integrated directly into LocalAI.
type AgentPoolService struct {
	appConfig          *config.ApplicationConfig
	pool               *state.AgentPool
	skillsService      *skills.Service
	collectionsBackend collections.Backend
	configMeta         state.AgentConfigMeta
	actionsConfig      map[string]string
	sharedState        *coreTypes.AgentSharedState
	stateDir           string
	outputsDir         string
	mu                 sync.Mutex
}

func NewAgentPoolService(appConfig *config.ApplicationConfig) (*AgentPoolService, error) {
	return &AgentPoolService{
		appConfig: appConfig,
	}, nil
}

func (s *AgentPoolService) Start(ctx context.Context) error {
	cfg := s.appConfig.AgentPool

	// API URL: use configured value, or derive self-referencing URL from LocalAI's address
	apiURL := cfg.APIURL
	if apiURL == "" {
		_, port, err := net.SplitHostPort(s.appConfig.APIAddress)
		if err != nil {
			port = strings.TrimPrefix(s.appConfig.APIAddress, ":")
		}
		apiURL = "http://127.0.0.1:" + port
	}
	apiKey := cfg.APIKey
	if apiKey == "" && len(s.appConfig.ApiKeys) > 0 {
		apiKey = s.appConfig.ApiKeys[0]
	}

	// State dir: explicit config > DataPath > DynamicConfigsDir > fallback
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = s.appConfig.DataPath
	}
	if stateDir == "" {
		stateDir = s.appConfig.DynamicConfigsDir
	}
	if stateDir == "" {
		stateDir = "agents"
	}
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return fmt.Errorf("failed to create agent pool state dir: %w", err)
	}

	// Collections paths
	collectionDBPath := cfg.CollectionDBPath
	if collectionDBPath == "" {
		collectionDBPath = filepath.Join(stateDir, "collections")
	}
	fileAssets := filepath.Join(stateDir, "assets")

	// Skills service — always created since the agent pool calls GetSkillsPrompt unconditionally.
	// When EnableSkills is false, the service still exists but the skills directory will be empty.
	skillsSvc, err := skills.NewService(stateDir)
	if err != nil {
		xlog.Error("Failed to create skills service", "error", err)
	}
	s.skillsService = skillsSvc

	// Actions config map — only set CustomActionsDir if non-empty to avoid
	// "open : no such file or directory" errors
	actionsConfig := map[string]string{
		agiServices.ConfigStateDir: stateDir,
	}
	if cfg.CustomActionsDir != "" {
		actionsConfig[agiServices.CustomActionsDir] = cfg.CustomActionsDir
	}

	// Create outputs subdirectory for action-generated files (PDFs, audio, etc.)
	outputsDir := filepath.Join(stateDir, "outputs")
	if err := os.MkdirAll(outputsDir, 0750); err != nil {
		xlog.Error("Failed to create outputs directory", "path", outputsDir, "error", err)
	}

	s.actionsConfig = actionsConfig
	s.stateDir = stateDir
	s.outputsDir = outputsDir
	s.sharedState = coreTypes.NewAgentSharedState(5 * time.Minute)

	// Create the agent pool
	pool, err := state.NewAgentPool(
		cfg.DefaultModel,
		cfg.MultimodalModel,
		cfg.TranscriptionModel,
		cfg.TranscriptionLanguage,
		cfg.TTSModel,
		apiURL,
		apiKey,
		stateDir,
		agiServices.Actions(actionsConfig),
		agiServices.Connectors,
		agiServices.DynamicPrompts(actionsConfig),
		agiServices.Filters,
		cfg.Timeout,
		cfg.EnableLogs,
		skillsSvc,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent pool: %w", err)
	}
	s.pool = pool

	// Create in-process collections backend and RAG provider directly
	collectionsCfg := &collections.Config{
		LLMAPIURL:       apiURL,
		LLMAPIKey:       apiKey,
		LLMModel:        cfg.DefaultModel,
		CollectionDBPath: collectionDBPath,
		FileAssets:       fileAssets,
		VectorEngine:    cfg.VectorEngine,
		EmbeddingModel:  cfg.EmbeddingModel,
		MaxChunkingSize: cfg.MaxChunkingSize,
		ChunkOverlap:    cfg.ChunkOverlap,
		DatabaseURL:     cfg.DatabaseURL,
	}
	collectionsBackend, collectionsState := collections.NewInProcessBackend(collectionsCfg)
	s.collectionsBackend = collectionsBackend

	// Set up in-process RAG provider from collections state
	embedded := collections.RAGProviderFromState(collectionsState)
	pool.SetRAGProvider(func(collectionName, _, _ string) (agent.RAGDB, state.KBCompactionClient, bool) {
		return embedded(collectionName)
	})

	// Build config metadata for UI
	s.configMeta = state.NewAgentConfigMeta(
		agiServices.ActionsConfigMeta(cfg.CustomActionsDir),
		agiServices.ConnectorsConfigMeta(),
		agiServices.DynamicPromptsConfigMeta(cfg.CustomActionsDir),
		agiServices.FiltersConfigMeta(),
	)

	// Start all agents
	if err := pool.StartAll(); err != nil {
		xlog.Error("Failed to start agent pool", "error", err)
	}

	xlog.Info("Agent pool started", "stateDir", stateDir, "apiURL", apiURL)
	return nil
}

func (s *AgentPoolService) Stop() {
	if s.pool != nil {
		s.pool.StopAll()
	}
}

// Pool returns the underlying AgentPool.
func (s *AgentPoolService) Pool() *state.AgentPool {
	return s.pool
}

// --- Agent CRUD ---

func (s *AgentPoolService) ListAgents() map[string]bool {
	statuses := map[string]bool{}
	agents := s.pool.List()
	for _, a := range agents {
		ag := s.pool.GetAgent(a)
		if ag == nil {
			continue
		}
		statuses[a] = !ag.Paused()
	}
	return statuses
}

func (s *AgentPoolService) CreateAgent(config *state.AgentConfig) error {
	if config.Name == "" {
		return fmt.Errorf("name is required")
	}
	return s.pool.CreateAgent(config.Name, config)
}

func (s *AgentPoolService) GetAgent(name string) *agent.Agent {
	return s.pool.GetAgent(name)
}

func (s *AgentPoolService) GetAgentConfig(name string) *state.AgentConfig {
	return s.pool.GetConfig(name)
}

func (s *AgentPoolService) UpdateAgent(name string, config *state.AgentConfig) error {
	old := s.pool.GetConfig(name)
	if old == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	return s.pool.RecreateAgent(name, config)
}

func (s *AgentPoolService) DeleteAgent(name string) error {
	return s.pool.Remove(name)
}

func (s *AgentPoolService) PauseAgent(name string) error {
	ag := s.pool.GetAgent(name)
	if ag == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag.Pause()
	return nil
}

func (s *AgentPoolService) ResumeAgent(name string) error {
	ag := s.pool.GetAgent(name)
	if ag == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag.Resume()
	return nil
}

func (s *AgentPoolService) GetAgentStatus(name string) *state.Status {
	return s.pool.GetStatusHistory(name)
}

func (s *AgentPoolService) GetAgentObservables(name string) ([]coreTypes.Observable, error) {
	ag := s.pool.GetAgent(name)
	if ag == nil {
		return nil, fmt.Errorf("agent not found: %s", name)
	}
	return ag.Observer().History(), nil
}

func (s *AgentPoolService) ClearAgentObservables(name string) error {
	ag := s.pool.GetAgent(name)
	if ag == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag.Observer().ClearHistory()
	return nil
}

// Chat sends a message to an agent and returns immediately. Responses come via SSE.
func (s *AgentPoolService) Chat(name, message string) (string, error) {
	ag := s.pool.GetAgent(name)
	if ag == nil {
		return "", fmt.Errorf("agent not found: %s", name)
	}
	manager := s.pool.GetManager(name)
	if manager == nil {
		return "", fmt.Errorf("SSE manager not found for agent: %s", name)
	}

	messageID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Send user message via SSE
	userMsg, _ := json.Marshal(map[string]any{
		"id":        messageID + "-user",
		"sender":    "user",
		"content":   message,
		"timestamp": time.Now().Format(time.RFC3339),
	})
	manager.Send(sse.NewMessage(string(userMsg)).WithEvent("json_message"))

	// Send processing status
	statusMsg, _ := json.Marshal(map[string]any{
		"status":    "processing",
		"timestamp": time.Now().Format(time.RFC3339),
	})
	manager.Send(sse.NewMessage(string(statusMsg)).WithEvent("json_message_status"))

	// Process asynchronously
	go func() {
		response := ag.Ask(coreTypes.WithText(message))

		if response == nil {
			errMsg, _ := json.Marshal(map[string]any{
				"error":     "agent request failed or was cancelled",
				"timestamp": time.Now().Format(time.RFC3339),
			})
			manager.Send(sse.NewMessage(string(errMsg)).WithEvent("json_error"))
		} else if response.Error != nil {
			errMsg, _ := json.Marshal(map[string]any{
				"error":     response.Error.Error(),
				"timestamp": time.Now().Format(time.RFC3339),
			})
			manager.Send(sse.NewMessage(string(errMsg)).WithEvent("json_error"))
		} else {
			// Collect metadata from all action states
			metadata := map[string]any{}
			for _, state := range response.State {
				for k, v := range state.Metadata {
					if existing, ok := metadata[k]; ok {
						if existList, ok := existing.([]string); ok {
							if newList, ok := v.([]string); ok {
								metadata[k] = append(existList, newList...)
								continue
							}
						}
					}
					metadata[k] = v
				}
			}

			if len(metadata) > 0 {
				s.collectAndCopyMetadata(metadata)
			}

			msg := map[string]any{
				"id":        messageID + "-agent",
				"sender":    "agent",
				"content":   response.Response,
				"timestamp": time.Now().Format(time.RFC3339),
			}
			if len(metadata) > 0 {
				msg["metadata"] = metadata
			}
			respMsg, _ := json.Marshal(msg)
			manager.Send(sse.NewMessage(string(respMsg)).WithEvent("json_message"))
		}

		completedMsg, _ := json.Marshal(map[string]any{
			"status":    "completed",
			"timestamp": time.Now().Format(time.RFC3339),
		})
		manager.Send(sse.NewMessage(string(completedMsg)).WithEvent("json_message_status"))
	}()

	return messageID, nil
}

// copyToOutputs copies a file into the outputs directory and returns the new path.
// If the file is already inside outputsDir, it returns the original path unchanged.
func (s *AgentPoolService) copyToOutputs(srcPath string) (string, error) {
	srcClean := filepath.Clean(srcPath)
	absOutputs, _ := filepath.Abs(s.outputsDir)
	absSrc, _ := filepath.Abs(srcClean)
	if strings.HasPrefix(absSrc, absOutputs+string(os.PathSeparator)) {
		return srcPath, nil
	}

	src, err := os.Open(srcClean)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dstPath := filepath.Join(s.outputsDir, filepath.Base(srcClean))
	dst, err := os.Create(dstPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	return dstPath, nil
}

// collectAndCopyMetadata iterates all metadata keys and, for any value that is
// a []string of local file paths, copies those files into the outputs directory
// so the file endpoint can serve them from a single confined location.
// Entries that are URLs (http/https) are left unchanged.
func (s *AgentPoolService) collectAndCopyMetadata(metadata map[string]any) {
	for key, val := range metadata {
		list, ok := val.([]string)
		if !ok {
			continue
		}
		updated := make([]string, 0, len(list))
		for _, p := range list {
			if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
				updated = append(updated, p)
				continue
			}
			newPath, err := s.copyToOutputs(p)
			if err != nil {
				xlog.Error("Failed to copy file to outputs", "src", p, "error", err)
				updated = append(updated, p)
				continue
			}
			updated = append(updated, newPath)
		}
		metadata[key] = updated
	}
}

func (s *AgentPoolService) GetSSEManager(name string) sse.Manager {
	return s.pool.GetManager(name)
}

func (s *AgentPoolService) GetConfigMeta() state.AgentConfigMeta {
	return s.configMeta
}

func (s *AgentPoolService) AgentHubURL() string {
	return s.appConfig.AgentPool.AgentHubURL
}

func (s *AgentPoolService) StateDir() string {
	return s.stateDir
}

func (s *AgentPoolService) OutputsDir() string {
	return s.outputsDir
}

// ExportAgent returns the agent config as JSON bytes.
func (s *AgentPoolService) ExportAgent(name string) ([]byte, error) {
	cfg := s.pool.GetConfig(name)
	if cfg == nil {
		return nil, fmt.Errorf("agent not found: %s", name)
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ImportAgent creates an agent from JSON config data.
func (s *AgentPoolService) ImportAgent(data []byte) error {
	var cfg state.AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid agent config: %w", err)
	}
	if cfg.Name == "" {
		return fmt.Errorf("agent name is required")
	}
	return s.pool.CreateAgent(cfg.Name, &cfg)
}

// --- Skills ---

func (s *AgentPoolService) SkillsService() *skills.Service {
	return s.skillsService
}

func (s *AgentPoolService) GetSkillsConfig() map[string]any {
	if s.skillsService == nil {
		return nil
	}
	return map[string]any{"skills_dir": s.skillsService.GetSkillsDir()}
}

func (s *AgentPoolService) ListSkills() ([]skilldomain.Skill, error) {
	if s.skillsService == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		if mgr == nil {
			return []skilldomain.Skill{}, nil
		}
		return nil, err
	}
	return mgr.ListSkills()
}

func (s *AgentPoolService) GetSkill(name string) (*skilldomain.Skill, error) {
	if s.skillsService == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	return mgr.ReadSkill(name)
}

func (s *AgentPoolService) SearchSkills(query string) ([]skilldomain.Skill, error) {
	if s.skillsService == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	return mgr.SearchSkills(query)
}

func (s *AgentPoolService) CreateSkill(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skilldomain.Skill, error) {
	if s.skillsService == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	fsManager, ok := mgr.(*skilldomain.FileSystemManager)
	if !ok {
		return nil, fmt.Errorf("unsupported manager type")
	}
	if err := skilldomain.ValidateSkillName(name); err != nil {
		return nil, err
	}

	skillsDir := fsManager.GetSkillsDir()
	skillDir := filepath.Join(skillsDir, name)
	if _, err := os.Stat(skillDir); err == nil {
		return nil, fmt.Errorf("skill already exists")
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return nil, err
	}

	frontmatter := fmt.Sprintf("---\nname: %s\ndescription: %s\n", name, description)
	if license != "" {
		frontmatter += fmt.Sprintf("license: %s\n", license)
	}
	if compatibility != "" {
		frontmatter += fmt.Sprintf("compatibility: %s\n", compatibility)
	}
	if len(metadata) > 0 {
		frontmatter += "metadata:\n"
		for k, v := range metadata {
			frontmatter += fmt.Sprintf("  %s: %s\n", k, v)
		}
	}
	if allowedTools != "" {
		frontmatter += fmt.Sprintf("allowed-tools: %s\n", allowedTools)
	}
	frontmatter += "---\n\n"

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(frontmatter+content), 0644); err != nil {
		os.RemoveAll(skillDir)
		return nil, err
	}
	if err := mgr.RebuildIndex(); err != nil {
		return nil, fmt.Errorf("failed to rebuild index: %w", err)
	}
	return mgr.ReadSkill(name)
}

func (s *AgentPoolService) UpdateSkill(name, description, content, license, compatibility, allowedTools string, metadata map[string]string) (*skilldomain.Skill, error) {
	if s.skillsService == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	fsManager, ok := mgr.(*skilldomain.FileSystemManager)
	if !ok {
		return nil, fmt.Errorf("unsupported manager type")
	}
	existing, err := mgr.ReadSkill(name)
	if err != nil {
		return nil, fmt.Errorf("skill not found")
	}
	if existing.ReadOnly {
		return nil, fmt.Errorf("cannot update read-only skill from git repository")
	}

	skillDir := filepath.Join(fsManager.GetSkillsDir(), name)
	frontmatter := fmt.Sprintf("---\nname: %s\ndescription: %s\n", name, description)
	if license != "" {
		frontmatter += fmt.Sprintf("license: %s\n", license)
	}
	if compatibility != "" {
		frontmatter += fmt.Sprintf("compatibility: %s\n", compatibility)
	}
	if len(metadata) > 0 {
		frontmatter += "metadata:\n"
		for k, v := range metadata {
			frontmatter += fmt.Sprintf("  %s: %s\n", k, v)
		}
	}
	if allowedTools != "" {
		frontmatter += fmt.Sprintf("allowed-tools: %s\n", allowedTools)
	}
	frontmatter += "---\n\n"

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(frontmatter+content), 0644); err != nil {
		return nil, err
	}
	if err := mgr.RebuildIndex(); err != nil {
		return nil, fmt.Errorf("failed to rebuild index: %w", err)
	}
	return mgr.ReadSkill(name)
}

func (s *AgentPoolService) DeleteSkill(name string) error {
	if s.skillsService == nil {
		return fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return fmt.Errorf("skills directory not configured")
	}
	fsManager, ok := mgr.(*skilldomain.FileSystemManager)
	if !ok {
		return fmt.Errorf("unsupported manager type")
	}
	existing, err := mgr.ReadSkill(name)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if existing.ReadOnly {
		return fmt.Errorf("cannot delete read-only skill from git repository")
	}
	skillDir := filepath.Join(fsManager.GetSkillsDir(), name)
	if err := os.RemoveAll(skillDir); err != nil {
		return err
	}
	return mgr.RebuildIndex()
}

func (s *AgentPoolService) ExportSkill(name string) ([]byte, error) {
	if s.skillsService == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	fsManager, ok := mgr.(*skilldomain.FileSystemManager)
	if !ok {
		return nil, fmt.Errorf("unsupported manager type")
	}
	skill, err := mgr.ReadSkill(name)
	if err != nil {
		return nil, fmt.Errorf("skill not found")
	}
	return skilldomain.ExportSkill(skill.ID, fsManager.GetSkillsDir())
}

func (s *AgentPoolService) ImportSkill(archiveData []byte) (*skilldomain.Skill, error) {
	if s.skillsService == nil {
		return nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, fmt.Errorf("skills directory not configured")
	}
	fsManager, ok := mgr.(*skilldomain.FileSystemManager)
	if !ok {
		return nil, fmt.Errorf("unsupported manager type")
	}
	skillName, err := skilldomain.ImportSkill(archiveData, fsManager.GetSkillsDir())
	if err != nil {
		return nil, err
	}
	if err := mgr.RebuildIndex(); err != nil {
		return nil, fmt.Errorf("failed to rebuild index: %w", err)
	}
	return mgr.ReadSkill(skillName)
}

// --- Skill Resources ---

func (s *AgentPoolService) ListSkillResources(skillName string) ([]skilldomain.SkillResource, *skilldomain.Skill, error) {
	if s.skillsService == nil {
		return nil, nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, nil, fmt.Errorf("skills directory not configured")
	}
	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return nil, nil, fmt.Errorf("skill not found")
	}
	resources, err := mgr.ListSkillResources(skill.ID)
	if err != nil {
		return nil, nil, err
	}
	return resources, skill, nil
}

func (s *AgentPoolService) GetSkillResource(skillName, resourcePath string) (*skilldomain.ResourceContent, *skilldomain.SkillResource, error) {
	if s.skillsService == nil {
		return nil, nil, fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return nil, nil, fmt.Errorf("skills directory not configured")
	}
	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return nil, nil, fmt.Errorf("skill not found")
	}
	info, err := mgr.GetSkillResourceInfo(skill.ID, resourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("resource not found")
	}
	content, err := mgr.ReadSkillResource(skill.ID, resourcePath)
	if err != nil {
		return nil, nil, err
	}
	return content, info, nil
}

func (s *AgentPoolService) CreateSkillResource(skillName, path string, data []byte) error {
	if s.skillsService == nil {
		return fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return fmt.Errorf("skills directory not configured")
	}
	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if skill.ReadOnly {
		return fmt.Errorf("cannot add resources to read-only skill")
	}
	if err := skilldomain.ValidateResourcePath(path); err != nil {
		return err
	}
	fullPath := filepath.Join(skill.SourcePath, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, data, 0644)
}

func (s *AgentPoolService) UpdateSkillResource(skillName, resourcePath, content string) error {
	if s.skillsService == nil {
		return fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return fmt.Errorf("skills directory not configured")
	}
	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if skill.ReadOnly {
		return fmt.Errorf("cannot update resources in read-only skill")
	}
	if err := skilldomain.ValidateResourcePath(resourcePath); err != nil {
		return err
	}
	fullPath := filepath.Join(skill.SourcePath, resourcePath)
	return os.WriteFile(fullPath, []byte(content), 0644)
}

func (s *AgentPoolService) DeleteSkillResource(skillName, resourcePath string) error {
	if s.skillsService == nil {
		return fmt.Errorf("skills service not available")
	}
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return fmt.Errorf("skills directory not configured")
	}
	skill, err := mgr.ReadSkill(skillName)
	if err != nil {
		return fmt.Errorf("skill not found")
	}
	if skill.ReadOnly {
		return fmt.Errorf("cannot delete resources from read-only skill")
	}
	if err := skilldomain.ValidateResourcePath(resourcePath); err != nil {
		return err
	}
	fullPath := filepath.Join(skill.SourcePath, resourcePath)
	return os.Remove(fullPath)
}

// --- Git Repos ---

func (s *AgentPoolService) getSkillsDir() string {
	if s.skillsService == nil {
		return ""
	}
	return s.skillsService.GetSkillsDir()
}

type GitRepoInfo struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (s *AgentPoolService) ListGitRepos() ([]GitRepoInfo, error) {
	dir := s.getSkillsDir()
	if dir == "" {
		return []GitRepoInfo{}, nil
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
	out := make([]GitRepoInfo, len(repos))
	for i, r := range repos {
		out[i] = GitRepoInfo{ID: r.ID, URL: r.URL, Name: r.Name, Enabled: r.Enabled}
	}
	return out, nil
}

func (s *AgentPoolService) AddGitRepo(repoURL string) (*GitRepoInfo, error) {
	dir := s.getSkillsDir()
	if dir == "" {
		return nil, fmt.Errorf("skills directory not configured")
	}
	if !strings.HasPrefix(repoURL, "http://") && !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "git@") {
		return nil, fmt.Errorf("invalid URL format")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
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
		mgr, err := s.skillsService.GetManager()
		if err != nil || mgr == nil {
			return
		}
		syncer := skillgit.NewGitSyncer(dir, []string{repoURL}, mgr.RebuildIndex)
		if err := syncer.Start(); err != nil {
			xlog.Error("background sync failed", "url", repoURL, "error", err)
			s.skillsService.RefreshManagerFromConfig()
			return
		}
		syncer.Stop()
		s.skillsService.RefreshManagerFromConfig()
	}()

	return &GitRepoInfo{ID: newRepo.ID, URL: newRepo.URL, Name: newRepo.Name, Enabled: newRepo.Enabled}, nil
}

func (s *AgentPoolService) UpdateGitRepo(id, repoURL string, enabled *bool) (*GitRepoInfo, error) {
	dir := s.getSkillsDir()
	if dir == "" {
		return nil, fmt.Errorf("skills directory not configured")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, r := range repos {
		if r.ID == id {
			idx = i
			if repoURL != "" {
				parsedURL, err := url.Parse(repoURL)
				if err != nil || parsedURL.Scheme == "" {
					return nil, fmt.Errorf("invalid repository URL")
				}
				repos[i].URL = repoURL
				repos[i].Name = skillgit.ExtractRepoName(repoURL)
			}
			if enabled != nil {
				repos[i].Enabled = *enabled
			}
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("repository not found")
	}
	if err := cm.SaveConfig(repos); err != nil {
		return nil, err
	}
	s.skillsService.RefreshManagerFromConfig()
	r := repos[idx]
	return &GitRepoInfo{ID: r.ID, URL: r.URL, Name: r.Name, Enabled: r.Enabled}, nil
}

func (s *AgentPoolService) DeleteGitRepo(id string) error {
	dir := s.getSkillsDir()
	if dir == "" {
		return fmt.Errorf("skills directory not configured")
	}
	cm := skillgit.NewConfigManager(dir)
	repos, err := cm.LoadConfig()
	if err != nil {
		return err
	}
	var newRepos []skillgit.GitRepoConfig
	var repoName string
	for _, r := range repos {
		if r.ID == id {
			repoName = r.Name
		} else {
			newRepos = append(newRepos, r)
		}
	}
	if len(newRepos) == len(repos) {
		return fmt.Errorf("repository not found")
	}
	if err := cm.SaveConfig(newRepos); err != nil {
		return err
	}
	if repoName != "" {
		os.RemoveAll(filepath.Join(dir, repoName))
	}
	s.skillsService.RefreshManagerFromConfig()
	return nil
}

func (s *AgentPoolService) SyncGitRepo(id string) error {
	dir := s.getSkillsDir()
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
	mgr, err := s.skillsService.GetManager()
	if err != nil || mgr == nil {
		return fmt.Errorf("manager not ready")
	}
	go func() {
		syncer := skillgit.NewGitSyncer(dir, []string{repoURL}, mgr.RebuildIndex)
		if err := syncer.Start(); err != nil {
			xlog.Error("background sync failed", "id", id, "error", err)
			s.skillsService.RefreshManagerFromConfig()
			return
		}
		syncer.Stop()
		s.skillsService.RefreshManagerFromConfig()
	}()
	return nil
}

func (s *AgentPoolService) ToggleGitRepo(id string) (*GitRepoInfo, error) {
	dir := s.getSkillsDir()
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
			s.skillsService.RefreshManagerFromConfig()
			return &GitRepoInfo{ID: repos[i].ID, URL: repos[i].URL, Name: repos[i].Name, Enabled: repos[i].Enabled}, nil
		}
	}
	return nil, fmt.Errorf("repository not found")
}

// --- Collections ---

func (s *AgentPoolService) CollectionsBackend() collections.Backend {
	return s.collectionsBackend
}

func (s *AgentPoolService) ListCollections() ([]string, error) {
	return s.collectionsBackend.ListCollections()
}

func (s *AgentPoolService) CreateCollection(name string) error {
	return s.collectionsBackend.CreateCollection(name)
}

func (s *AgentPoolService) UploadToCollection(collection, filename string, fileBody io.Reader) error {
	return s.collectionsBackend.Upload(collection, filename, fileBody)
}

func (s *AgentPoolService) ListCollectionEntries(collection string) ([]string, error) {
	return s.collectionsBackend.ListEntries(collection)
}

func (s *AgentPoolService) GetCollectionEntryContent(collection, entry string) (string, int, error) {
	return s.collectionsBackend.GetEntryContent(collection, entry)
}

func (s *AgentPoolService) SearchCollection(collection, query string, maxResults int) ([]collections.SearchResult, error) {
	return s.collectionsBackend.Search(collection, query, maxResults)
}

func (s *AgentPoolService) ResetCollection(collection string) error {
	return s.collectionsBackend.Reset(collection)
}

func (s *AgentPoolService) DeleteCollectionEntry(collection, entry string) ([]string, error) {
	return s.collectionsBackend.DeleteEntry(collection, entry)
}

func (s *AgentPoolService) AddCollectionSource(collection, sourceURL string, intervalMin int) error {
	return s.collectionsBackend.AddSource(collection, sourceURL, intervalMin)
}

func (s *AgentPoolService) RemoveCollectionSource(collection, sourceURL string) error {
	return s.collectionsBackend.RemoveSource(collection, sourceURL)
}

func (s *AgentPoolService) ListCollectionSources(collection string) ([]collections.SourceInfo, error) {
	return s.collectionsBackend.ListSources(collection)
}

func (s *AgentPoolService) CollectionEntryExists(collection, entry string) bool {
	return s.collectionsBackend.EntryExists(collection, entry)
}

// --- Actions ---

// ListAvailableActions returns the list of all available action type names.
func (s *AgentPoolService) ListAvailableActions() []string {
	return agiServices.AvailableActions
}

// GetActionDefinition creates an action instance by name with the given config and returns its definition.
func (s *AgentPoolService) GetActionDefinition(actionName string, actionConfig map[string]string) (any, error) {
	if actionConfig == nil {
		actionConfig = map[string]string{}
	}
	a, err := agiServices.Action(actionName, "", actionConfig, s.pool, s.actionsConfig)
	if err != nil {
		return nil, err
	}
	return a.Definition(), nil
}

// ExecuteAction creates an action instance and runs it with the given params.
func (s *AgentPoolService) ExecuteAction(ctx context.Context, actionName string, actionConfig map[string]string, params coreTypes.ActionParams) (coreTypes.ActionResult, error) {
	if actionConfig == nil {
		actionConfig = map[string]string{}
	}
	a, err := agiServices.Action(actionName, "", actionConfig, s.pool, s.actionsConfig)
	if err != nil {
		return coreTypes.ActionResult{}, err
	}
	return a.Run(ctx, s.sharedState, params)
}
