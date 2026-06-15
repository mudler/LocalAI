package agentpool

import (
	"sync"

	"github.com/mudler/LocalAGI/services/skills"
	"github.com/mudler/LocalAGI/webui/collections"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// UserServicesManager lazily creates per-user service instances for
// collections, skills, and jobs.
type UserServicesManager struct {
	mu               sync.RWMutex
	storage          *UserScopedStorage
	appConfig        *config.ApplicationConfig
	modelLoader      *model.ModelLoader
	configLoader     *config.ModelConfigLoader
	evaluator        *templates.Evaluator
	collectionsCache map[string]collections.Backend
	skillsCache      map[string]*skills.Service
	jobsCache        map[string]*AgentJobService

	// Shared distributed backends (set once, inherited by per-user job services)
	jobDispatcher DistributedDispatcher
	jobDBStore    *jobs.JobStore
}

// NewUserServicesManager creates a new UserServicesManager.
func NewUserServicesManager(
	storage *UserScopedStorage,
	appConfig *config.ApplicationConfig,
	modelLoader *model.ModelLoader,
	configLoader *config.ModelConfigLoader,
	evaluator *templates.Evaluator,
) *UserServicesManager {
	return &UserServicesManager{
		storage:          storage,
		appConfig:        appConfig,
		modelLoader:      modelLoader,
		configLoader:     configLoader,
		evaluator:        evaluator,
		collectionsCache: make(map[string]collections.Backend),
		skillsCache:      make(map[string]*skills.Service),
		jobsCache:        make(map[string]*AgentJobService),
	}
}

// GetCollections returns the collections backend for a user, creating it lazily.
func (m *UserServicesManager) GetCollections(userID string) (collections.Backend, error) {
	m.mu.RLock()
	if backend, ok := m.collectionsCache[userID]; ok {
		m.mu.RUnlock()
		return backend, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if backend, ok := m.collectionsCache[userID]; ok {
		return backend, nil
	}

	if err := m.storage.EnsureUserDirs(userID); err != nil {
		return nil, err
	}

	cfg := m.appConfig.AgentPool
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = "http://127.0.0.1:" + getPort(m.appConfig)
	}
	apiKey := cfg.APIKey
	if apiKey == "" && len(m.appConfig.ApiKeys) > 0 {
		apiKey = m.appConfig.ApiKeys[0]
	}

	collectionsCfg := &collections.Config{
		LLMAPIURL:        apiURL,
		LLMAPIKey:        apiKey,
		LLMModel:         cfg.DefaultModel,
		CollectionDBPath: m.storage.CollectionsDir(userID),
		FileAssets:       m.storage.AssetsDir(userID),
		VectorEngine:     cfg.VectorEngine,
		EmbeddingModel:   cfg.EmbeddingModel,
		MaxChunkingSize:  cfg.MaxChunkingSize,
		ChunkOverlap:     cfg.ChunkOverlap,
		DatabaseURL:      cfg.DatabaseURL,
	}

	backend, _ := collections.NewInProcessBackend(collectionsCfg)
	m.collectionsCache[userID] = backend
	return backend, nil
}

// GetSkills returns the skills service for a user, creating it lazily.
func (m *UserServicesManager) GetSkills(userID string) (*skills.Service, error) {
	m.mu.RLock()
	if svc, ok := m.skillsCache[userID]; ok {
		m.mu.RUnlock()
		return svc, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if svc, ok := m.skillsCache[userID]; ok {
		return svc, nil
	}

	if err := m.storage.EnsureUserDirs(userID); err != nil {
		return nil, err
	}

	skillsDir := m.storage.SkillsDir(userID)
	svc, err := skills.NewService(skillsDir)
	if err != nil {
		return nil, err
	}
	m.skillsCache[userID] = svc
	return svc, nil
}

// GetJobs returns the agent job service for a user, creating it lazily.
func (m *UserServicesManager) GetJobs(userID string) (*AgentJobService, error) {
	m.mu.RLock()
	if svc, ok := m.jobsCache[userID]; ok {
		m.mu.RUnlock()
		return svc, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if svc, ok := m.jobsCache[userID]; ok {
		return svc, nil
	}

	if err := m.storage.EnsureUserDirs(userID); err != nil {
		return nil, err
	}

	svc := NewAgentJobServiceWithPaths(
		m.appConfig,
		m.modelLoader,
		m.configLoader,
		m.evaluator,
		m.storage.TasksFile(userID),
		m.storage.JobsFile(userID),
	)
	// Set user ID for per-user DB scoping
	svc.SetUserID(userID)
	// Inherit distributed backends so per-user jobs go through NATS + DB
	if m.jobDispatcher != nil {
		svc.SetDistributedBackends(m.jobDispatcher)
	}
	if m.jobDBStore != nil {
		svc.SetDistributedJobStore(m.jobDBStore)
		// Load tasks/jobs from DB immediately (per-user services skip Start())
		svc.LoadFromDB()
	} else {
		// Load from per-user files
		if err := svc.LoadTasksFromFile(); err != nil {
			xlog.Warn("Failed to load tasks from file for user", "userID", userID, "error", err)
		}
		if err := svc.LoadJobsFromFile(); err != nil {
			xlog.Warn("Failed to load jobs from file for user", "userID", userID, "error", err)
		}
	}
	m.jobsCache[userID] = svc
	return svc, nil
}

// SetJobDispatcher sets the distributed dispatcher for per-user job services.
func (m *UserServicesManager) SetJobDispatcher(d DistributedDispatcher) {
	m.jobDispatcher = d
}

// SetJobDBStore sets the database-backed job store for per-user job services.
func (m *UserServicesManager) SetJobDBStore(s *jobs.JobStore) {
	m.jobDBStore = s
}

// ListAllUserIDs returns all user IDs that have scoped data directories.
func (m *UserServicesManager) ListAllUserIDs() ([]string, error) {
	return m.storage.ListUserDirs()
}

// getPort extracts the port from the API address config.
func getPort(appConfig *config.ApplicationConfig) string {
	addr := appConfig.APIAddress
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	return addr
}

// StopAll stops all cached job services.
func (m *UserServicesManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, svc := range m.jobsCache {
		if err := svc.Stop(); err != nil {
			xlog.Error("Failed to stop user job service", "error", err)
		}
	}
}
