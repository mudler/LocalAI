package services

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	skillsManager "github.com/mudler/LocalAI/core/services/skills"

	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/sse"
	"github.com/mudler/LocalAGI/core/state"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	agiServices "github.com/mudler/LocalAGI/services"
	"github.com/mudler/LocalAGI/services/skills"
	"github.com/mudler/LocalAGI/webui/collections"
	"github.com/mudler/xlog"

	"gorm.io/gorm"
)

// AgentPoolService wraps LocalAGI's AgentPool, Skills service, and collections backend
// to provide agentic capabilities integrated directly into LocalAI.
//
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
	userServices       *UserServicesManager
	userStorage        *UserScopedStorage
	authDB             *gorm.DB
	skillStore         *distributed.SkillStore // PostgreSQL skill metadata (distributed mode)

	// Distributed mode fields
	natsClient  NATSClient                // NATS client for distributed agent execution
	eventBridge AgentEventBridge          // Event bridge for SSE + persistence
	agentStore  AgentConfigStore          // PostgreSQL agent config store
	dispatcher  agents.Dispatcher         // Native dispatcher (distributed or local)
	apiURL      string                    // Resolved API URL for agent execution
	apiKey      string                    // Resolved API key for agent execution
}

// NATSClient is the interface for NATS operations needed by AgentPoolService.
// In distributed mode, the frontend only publishes chat events to NATS.
// The agent-worker process handles subscriptions via the NATSDispatcher.
type NATSClient interface {
	Publish(subject string, data any) error
}

// AgentEventBridge is the interface for event publishing needed by AgentPoolService.
type AgentEventBridge interface {
	PublishMessage(agentName, userID, sender, content, messageID string) error
	PublishStatus(agentName, userID, status string) error
	PublishStreamEvent(agentName, userID string, data map[string]any) error
	RegisterCancel(agentName, userID string, cancel context.CancelFunc)
	DeregisterCancel(agentName, userID string)
}

// AgentConfigStore is the interface for agent config persistence.
type AgentConfigStore interface {
	SaveConfig(cfg *agents.AgentConfigRecord) error
	GetConfig(userID, name string) (*agents.AgentConfigRecord, error)
	ListConfigs(userID string) ([]agents.AgentConfigRecord, error)
	DeleteConfig(userID, name string) error
	UpdateStatus(userID, name, status string) error
	UpdateLastRun(userID, name string) error
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

	s.apiURL = apiURL
	s.apiKey = apiKey

	// Distributed mode: use native executor + NATSDispatcher.
	// No LocalAGI pool, no collections, no skills service — all stateless.
	if s.natsClient != nil {
		return s.startDistributed(ctx, apiURL, apiKey)
	}

	// Standalone mode: use LocalAGI pool (backward compat)
	return s.startLocalAGI(ctx, cfg, apiURL, apiKey)
}

// startDistributed initializes the native agent executor with NATS dispatcher.
// No LocalAGI pool is created — agent execution is stateless.
// Skills and collections are still initialized for the frontend UI.
func (s *AgentPoolService) startDistributed(_ context.Context, apiURL, apiKey string) error {
	cfg := s.appConfig.AgentPool

	// State dir for skills and outputs
	stateDir := cmp.Or(cfg.StateDir, s.appConfig.DataPath, s.appConfig.DynamicConfigsDir, "agents")
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		xlog.Warn("Failed to create agent state dir", "error", err)
	}
	s.stateDir = stateDir

	// Outputs directory
	outputsDir := filepath.Join(stateDir, "outputs")
	if err := os.MkdirAll(outputsDir, 0750); err != nil {
		xlog.Warn("Failed to create outputs directory", "error", err)
	}
	s.outputsDir = outputsDir

	// Skills service — same as standalone, filesystem-based
	skillsSvc, err := skills.NewService(stateDir)
	if err != nil {
		xlog.Warn("Failed to create skills service in distributed mode", "error", err)
	} else {
		s.skillsService = skillsSvc
	}

	// Collections backend — same as standalone, in-process
	collectionDBPath := cfg.CollectionDBPath
	if collectionDBPath == "" {
		collectionDBPath = filepath.Join(stateDir, "collections")
	}
	fileAssets := filepath.Join(stateDir, "assets")

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
	collectionsBackend, _ := collections.NewInProcessBackend(collectionsCfg)
	s.collectionsBackend = collectionsBackend

	// User-scoped storage
	dataDir := cmp.Or(s.appConfig.DataPath, s.appConfig.DynamicConfigsDir)
	s.userStorage = NewUserScopedStorage(stateDir, dataDir)

	// Start the background agent scheduler on the frontend.
	// It needs DB access to list configs and update LastRunAt — the worker doesn't have DB.
	// The advisory lock ensures only one frontend instance runs the scheduler.
	if s.authDB != nil && s.natsClient != nil && s.agentStore != nil {
		var schedulerOpts []agents.AgentSchedulerOpt
		if s.skillStore != nil {
			schedulerOpts = append(schedulerOpts, agents.WithSchedulerSkillProvider(s.buildSkillProvider()))
		}
		scheduler := agents.NewAgentScheduler(
			s.authDB,
			&natsPublisherAdapter{s.natsClient},
			s.agentStore,
			messaging.SubjectAgentExecute,
			schedulerOpts...,
		)
		go scheduler.Start(context.Background())
	}

	xlog.Info("Agent pool started in distributed mode (frontend dispatcher only)", "apiURL", apiURL, "stateDir", stateDir)
	return nil
}

// startLocalAGI initializes the full LocalAGI pool for standalone mode.
func (s *AgentPoolService) startLocalAGI(_ context.Context, cfg config.AgentPoolConfig, apiURL, apiKey string) error {
	// State dir: explicit config > DataPath > DynamicConfigsDir > fallback
	stateDir := cmp.Or(cfg.StateDir, s.appConfig.DataPath, s.appConfig.DynamicConfigsDir, "agents")
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return fmt.Errorf("failed to create agent pool state dir: %w", err)
	}

	// Collections paths
	collectionDBPath := cfg.CollectionDBPath
	if collectionDBPath == "" {
		collectionDBPath = filepath.Join(stateDir, "collections")
	}
	fileAssets := filepath.Join(stateDir, "assets")

	// Skills service
	skillsSvc, err := skills.NewService(stateDir)
	if err != nil {
		xlog.Error("Failed to create skills service", "error", err)
	}
	s.skillsService = skillsSvc

	// Actions config map
	actionsConfig := map[string]string{
		agiServices.ConfigStateDir: stateDir,
	}
	if cfg.CustomActionsDir != "" {
		actionsConfig[agiServices.CustomActionsDir] = cfg.CustomActionsDir
	}

	// Create outputs subdirectory
	outputsDir := filepath.Join(stateDir, "outputs")
	if err := os.MkdirAll(outputsDir, 0750); err != nil {
		xlog.Error("Failed to create outputs directory", "path", outputsDir, "error", err)
	}

	s.actionsConfig = actionsConfig
	s.stateDir = stateDir
	s.outputsDir = outputsDir
	s.sharedState = coreTypes.NewAgentSharedState(5 * time.Minute)

	// Initialize user-scoped storage
	dataDir := cmp.Or(s.appConfig.DataPath, s.appConfig.DynamicConfigsDir)
	s.userStorage = NewUserScopedStorage(stateDir, dataDir)

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

	// Create in-process collections backend and RAG provider
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

	xlog.Info("Agent pool started (standalone/LocalAGI mode)", "stateDir", stateDir, "apiURL", apiURL)
	return nil
}

func (s *AgentPoolService) Stop() {
	if s.pool != nil {
		s.pool.StopAll()
	}
}

// IsDistributed returns true if the service is running in distributed mode.
func (s *AgentPoolService) IsDistributed() bool {
	return s.natsClient != nil
}

// APIURL returns the resolved API URL for agent execution.
func (s *AgentPoolService) APIURL() string {
	return s.apiURL
}

// APIKey returns the resolved API key for agent execution.
func (s *AgentPoolService) APIKey() string {
	return s.apiKey
}

// Pool returns the underlying AgentPool.
func (s *AgentPoolService) Pool() *state.AgentPool {
	return s.pool
}

// SetNATSClient sets the NATS client for distributed agent execution.
func (s *AgentPoolService) SetNATSClient(nc NATSClient) {
	s.natsClient = nc
}

// SetEventBridge sets the event bridge for distributed SSE + persistence.
func (s *AgentPoolService) SetEventBridge(eb AgentEventBridge) {
	s.eventBridge = eb
}

// SetAgentStore sets the PostgreSQL agent config store.
func (s *AgentPoolService) SetAgentStore(store AgentConfigStore) {
	s.agentStore = store
}

// Agent execution in distributed mode is handled by the dedicated agent-worker process
// using the NATSDispatcher from core/services/agents/dispatcher.go.
// The frontend only dispatches chat events to NATS via dispatchChat().

// --- Agent CRUD ---

func (s *AgentPoolService) GetAgent(name string) *agent.Agent {
	if s.pool == nil {
		return nil
	}
	return s.pool.GetAgent(name)
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
				// Extract userID from the agent key (format: "userID:agentName")
				var chatUserID string
				if parts := strings.SplitN(name, ":", 2); len(parts) == 2 {
					chatUserID = parts[0]
				}
				s.collectAndCopyMetadata(metadata, chatUserID)
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

// userOutputsDir returns the per-user outputs directory, creating it if needed.
// If userID is empty, falls back to the shared outputs directory.
func (s *AgentPoolService) userOutputsDir(userID string) string {
	if userID == "" {
		return s.outputsDir
	}
	dir := filepath.Join(s.outputsDir, userID)
	os.MkdirAll(dir, 0750)
	return dir
}

// copyToOutputs copies a file into the per-user outputs directory and returns the new path.
// If the file is already inside the target dir, it returns the original path unchanged.
func (s *AgentPoolService) copyToOutputs(srcPath, userID string) (string, error) {
	targetDir := s.userOutputsDir(userID)
	srcClean := filepath.Clean(srcPath)
	absTarget, _ := filepath.Abs(targetDir)
	absSrc, _ := filepath.Abs(srcClean)
	if strings.HasPrefix(absSrc, absTarget+string(os.PathSeparator)) {
		return srcPath, nil
	}

	src, err := os.Open(srcClean)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dstPath := filepath.Join(targetDir, filepath.Base(srcClean))
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
// a []string of local file paths, copies those files into the per-user outputs
// directory so the file endpoint can serve them from a single confined location.
// Entries that are URLs (http/https) are left unchanged.
func (s *AgentPoolService) collectAndCopyMetadata(metadata map[string]any, userID string) {
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
			newPath, err := s.copyToOutputs(p, userID)
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
	if s.pool != nil {
		cfg := s.pool.GetConfig(name)
		if cfg != nil {
			return json.MarshalIndent(cfg, "", "  ")
		}
	}
	// Fall back to PostgreSQL in distributed mode
	if s.agentStore != nil {
		// Try to extract userID and agent name from the key
		userID := ""
		agentName := name
		if u, a, ok := strings.Cut(name, ":"); ok {
			userID = u
			agentName = a
		}
		rec, err := s.agentStore.GetConfig(userID, agentName)
		if err != nil || rec == nil {
			return nil, fmt.Errorf("agent not found: %s", name)
		}
		// Return the raw config JSON (already properly formatted)
		var pretty json.RawMessage
		if err := json.Unmarshal([]byte(rec.ConfigJSON), &pretty); err == nil {
			return json.MarshalIndent(pretty, "", "  ")
		}
		return []byte(rec.ConfigJSON), nil
	}
	return nil, fmt.Errorf("agent not found: %s", name)
}

// --- User Services ---

// SetUserServicesManager sets the user services manager for per-user scoping.
func (s *AgentPoolService) SetUserServicesManager(usm *UserServicesManager) {
	s.userServices = usm
}

// UserStorage returns the user-scoped storage.
func (s *AgentPoolService) UserStorage() *UserScopedStorage {
	return s.userStorage
}

// UserServicesManager returns the user services manager.
func (s *AgentPoolService) UserServicesManager() *UserServicesManager {
	return s.userServices
}

// SetAuthDB sets the auth database for API key generation.
func (s *AgentPoolService) SetAuthDB(db *gorm.DB) {
	s.authDB = db
}

// SetSkillStore sets the distributed skill store for persisting skill metadata to PostgreSQL.
func (s *AgentPoolService) SetSkillStore(store *distributed.SkillStore) {
	s.skillStore = store
}

// --- Admin Aggregation ---

// UserAgentInfo holds agent info for cross-user listing.
type UserAgentInfo struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// ListAllAgentsGrouped returns all agents grouped by user ID.
// Keys without ":" go into the "" (root) group.
func (s *AgentPoolService) ListAllAgentsGrouped() map[string][]UserAgentInfo {
	result := map[string][]UserAgentInfo{}

	// In distributed mode, read from PostgreSQL
	if s.pool == nil && s.agentStore != nil {
		configs, err := s.agentStore.ListConfigs("")
		if err != nil {
			return result
		}
		for _, cfg := range configs {
			result[cfg.UserID] = append(result[cfg.UserID], UserAgentInfo{
				Name:   cfg.Name,
				Active: cfg.Status == "active",
			})
		}
		return result
	}

	if s.pool == nil {
		return result
	}

	agents := s.pool.List()
	for _, a := range agents {
		ag := s.pool.GetAgent(a)
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

// --- ForUser Collections ---

// ListCollectionsForUser lists collections for a specific user.
func (s *AgentPoolService) ListCollectionsForUser(userID string) ([]string, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return nil, err
	}
	return backend.ListCollections()
}

// CreateCollectionForUser creates a collection for a specific user.
func (s *AgentPoolService) CreateCollectionForUser(userID, name string) error {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return err
	}
	return backend.CreateCollection(name)
}

// ensureCollectionForUser creates a collection for the user if it doesn't already exist.
func (s *AgentPoolService) ensureCollectionForUser(userID, name string) error {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return err
	}
	collections, err := backend.ListCollections()
	if err != nil {
		return err
	}
	for _, c := range collections {
		if c == name {
			return nil
		}
	}
	return backend.CreateCollection(name)
}

// UploadToCollectionForUser uploads to a collection for a specific user.
func (s *AgentPoolService) UploadToCollectionForUser(userID, collection, filename string, fileBody io.Reader) (string, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return "", err
	}
	return backend.Upload(collection, filename, fileBody)
}

// CollectionEntryExistsForUser checks if an entry exists in a user's collection.
func (s *AgentPoolService) CollectionEntryExistsForUser(userID, collection, entry string) bool {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return false
	}
	return backend.EntryExists(collection, entry)
}

// ListCollectionEntriesForUser lists entries in a user's collection.
func (s *AgentPoolService) ListCollectionEntriesForUser(userID, collection string) ([]string, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return nil, err
	}
	return backend.ListEntries(collection)
}

// GetCollectionEntryContentForUser gets entry content for a user's collection.
func (s *AgentPoolService) GetCollectionEntryContentForUser(userID, collection, entry string) (string, int, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return "", 0, err
	}
	return backend.GetEntryContent(collection, entry)
}

// SearchCollectionForUser searches a user's collection.
func (s *AgentPoolService) SearchCollectionForUser(userID, collection, query string, maxResults int) ([]collections.SearchResult, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return nil, err
	}
	return backend.Search(collection, query, maxResults)
}

// ResetCollectionForUser resets a user's collection.
func (s *AgentPoolService) ResetCollectionForUser(userID, collection string) error {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return err
	}
	return backend.Reset(collection)
}

// DeleteCollectionEntryForUser deletes an entry from a user's collection.
func (s *AgentPoolService) DeleteCollectionEntryForUser(userID, collection, entry string) ([]string, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return nil, err
	}
	return backend.DeleteEntry(collection, entry)
}

// AddCollectionSourceForUser adds a source to a user's collection.
func (s *AgentPoolService) AddCollectionSourceForUser(userID, collection, sourceURL string, intervalMin int) error {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return err
	}
	return backend.AddSource(collection, sourceURL, intervalMin)
}

// RemoveCollectionSourceForUser removes a source from a user's collection.
func (s *AgentPoolService) RemoveCollectionSourceForUser(userID, collection, sourceURL string) error {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return err
	}
	return backend.RemoveSource(collection, sourceURL)
}

// ListCollectionSourcesForUser lists sources for a user's collection.
func (s *AgentPoolService) ListCollectionSourcesForUser(userID, collection string) ([]collections.SourceInfo, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return nil, err
	}
	return backend.ListSources(collection)
}

// GetCollectionEntryFilePathForUser gets the file path for an entry in a user's collection.
func (s *AgentPoolService) GetCollectionEntryFilePathForUser(userID, collection, entry string) (string, error) {
	backend, err := s.CollectionsBackendForUser(userID)
	if err != nil {
		return "", err
	}
	return backend.GetEntryFilePath(collection, entry)
}

// --- ForUser Agent Methods ---

// agentKey returns the namespaced key for an agent: "{userID}:{name}" or just "{name}" if no userID.
func agentKey(userID, name string) string {
	if userID == "" {
		return name
	}
	return userID + ":" + name
}

// ListAgentsForUser lists agents belonging to a specific user.
// If userID is empty, returns all agents (backward compat).
func (s *AgentPoolService) ListAgentsForUser(userID string) map[string]bool {
	// In distributed mode, read from PostgreSQL
	if s.agentStore != nil {
		statuses := map[string]bool{}
		configs, err := s.agentStore.ListConfigs(userID)
		if err != nil {
			xlog.Error("Failed to list agents from database", "error", err)
			return statuses
		}
		for _, cfg := range configs {
			statuses[cfg.Name] = cfg.Status == "active"
		}
		return statuses
	}

	// Local mode: read from in-memory pool
	statuses := map[string]bool{}
	agents := s.pool.List()
	prefix := ""
	if userID != "" {
		prefix = userID + ":"
	}
	for _, a := range agents {
		if userID != "" && !strings.HasPrefix(a, prefix) {
			continue
		}
		ag := s.pool.GetAgent(a)
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

// CreateAgentForUser creates an agent namespaced to a user.
// When auth is enabled and the agent config has no API key, a new user API key
// is auto-generated so the agent can authenticate against LocalAI's own API.
func (s *AgentPoolService) CreateAgentForUser(userID string, config *state.AgentConfig) error {
	if err := ValidateAgentName(config.Name); err != nil {
		return err
	}

	// Auto-generate a user API key when auth is active and none is specified
	if s.authDB != nil && userID != "" && config.APIKey == "" {
		plaintext, _, err := auth.CreateAPIKey(s.authDB, userID, "agent:"+config.Name, "user", s.appConfig.Auth.APIKeyHMACSecret, nil)
		if err != nil {
			return fmt.Errorf("failed to create API key for agent: %w", err)
		}
		config.APIKey = plaintext
		xlog.Info("Auto-generated API key for agent", "agent", config.Name, "user", userID)
	}

	key := agentKey(userID, config.Name)

	// Persist to PostgreSQL in distributed mode
	if s.agentStore != nil {
		configJSON, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal agent config: %w", err)
		}
		if err := s.agentStore.SaveConfig(&agents.AgentConfigRecord{
			UserID:     userID,
			Name:       config.Name,
			ConfigJSON: string(configJSON),
			Status:     "active",
		}); err != nil {
			return fmt.Errorf("failed to save agent config to database: %w", err)
		}
	}

	// Auto-create collection when knowledge base or long-term memory is enabled
	if config.EnableKnowledgeBase || config.LongTermMemory {
		if err := s.ensureCollectionForUser(userID, config.Name); err != nil {
			xlog.Warn("Failed to auto-create collection for agent", "agent", config.Name, "error", err)
		}
	}

	// In distributed mode, DB save is sufficient — no local pool
	if s.pool == nil {
		return nil
	}
	config.Name = key
	return s.pool.CreateAgent(key, config)
}

// GetAgentForUser returns the agent for a user.
func (s *AgentPoolService) GetAgentForUser(userID, name string) *agent.Agent {
	if s.pool == nil {
		return nil
	}
	return s.pool.GetAgent(agentKey(userID, name))
}

// GetAgentConfigForUser returns the agent config for a user's agent.
func (s *AgentPoolService) GetAgentConfigForUser(userID, name string) *state.AgentConfig {
	if s.pool != nil {
		cfg := s.pool.GetConfig(agentKey(userID, name))
		if cfg != nil {
			// Return a copy with the original name (strip userID: prefix)
			// to avoid leaking the internal key into the UI.
			result := *cfg
			result.Name = name
			return &result
		}
	}
	// Fall back to PostgreSQL in distributed mode
	if s.agentStore != nil {
		rec, err := s.agentStore.GetConfig(userID, name)
		if err != nil || rec == nil {
			return nil
		}
		var agentCfg state.AgentConfig
		if json.Unmarshal([]byte(rec.ConfigJSON), &agentCfg) != nil {
			return nil
		}
		return &agentCfg
	}
	return nil
}

// UpdateAgentForUser updates a user's agent.
func (s *AgentPoolService) UpdateAgentForUser(userID, name string, config *state.AgentConfig) error {
	key := agentKey(userID, name)

	// Check if agent exists (pool or DB)
	found := false
	if s.pool != nil {
		if old := s.pool.GetConfig(key); old != nil {
			found = true
		}
	}
	if !found && s.agentStore != nil {
		if _, err := s.agentStore.GetConfig(userID, name); err == nil {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("agent not found: %s", name)
	}

	// Auto-generate a user API key when auth is active and none is specified
	if s.authDB != nil && userID != "" && config.APIKey == "" {
		plaintext, _, err := auth.CreateAPIKey(s.authDB, userID, "agent:"+name, "user", s.appConfig.Auth.APIKeyHMACSecret, nil)
		if err != nil {
			return fmt.Errorf("failed to create API key for agent: %w", err)
		}
		config.APIKey = plaintext
	}

	// Persist to PostgreSQL in distributed mode
	if s.agentStore != nil {
		configJSON, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal agent config: %w", err)
		}
		if err := s.agentStore.SaveConfig(&agents.AgentConfigRecord{
			UserID:     userID,
			Name:       config.Name,
			ConfigJSON: string(configJSON),
			Status:     "active",
		}); err != nil {
			return fmt.Errorf("failed to update agent config in database: %w", err)
		}
	}

	// Auto-create collection when knowledge base or long-term memory is enabled
	if config.EnableKnowledgeBase || config.LongTermMemory {
		if err := s.ensureCollectionForUser(userID, config.Name); err != nil {
			xlog.Warn("Failed to auto-create collection for agent", "agent", config.Name, "error", err)
		}
	}

	if s.pool == nil {
		return nil
	}
	config.Name = key
	return s.pool.RecreateAgent(key, config)
}

// DeleteAgentForUser deletes a user's agent.
func (s *AgentPoolService) DeleteAgentForUser(userID, name string) error {
	if s.agentStore != nil {
		if err := s.agentStore.DeleteConfig(userID, name); err != nil {
			xlog.Warn("Failed to delete agent config from database", "error", err)
		}
	}
	if s.pool == nil {
		return nil
	}
	return s.pool.Remove(agentKey(userID, name))
}

// PauseAgentForUser pauses a user's agent.
func (s *AgentPoolService) PauseAgentForUser(userID, name string) error {
	// In distributed mode, update status in DB
	if s.agentStore != nil {
		return s.agentStore.UpdateStatus(userID, name, "paused")
	}
	if s.pool == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag := s.pool.GetAgent(agentKey(userID, name))
	if ag == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag.Pause()
	return nil
}

// ResumeAgentForUser resumes a user's agent.
func (s *AgentPoolService) ResumeAgentForUser(userID, name string) error {
	if s.agentStore != nil {
		return s.agentStore.UpdateStatus(userID, name, "active")
	}
	if s.pool == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag := s.pool.GetAgent(agentKey(userID, name))
	if ag == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag.Resume()
	return nil
}

// GetAgentStatusForUser returns the status of a user's agent.
func (s *AgentPoolService) GetAgentStatusForUser(userID, name string) *state.Status {
	if s.pool == nil {
		return nil
	}
	return s.pool.GetStatusHistory(agentKey(userID, name))
}

// GetAgentObservablesForUser returns observables for a user's agent.
func (s *AgentPoolService) GetAgentObservablesForUser(userID, name string) ([]coreTypes.Observable, error) {
	if s.pool == nil {
		return nil, nil
	}
	ag := s.pool.GetAgent(agentKey(userID, name))
	if ag == nil {
		return nil, fmt.Errorf("agent not found: %s", name)
	}
	return ag.Observer().History(), nil
}

// ClearAgentObservablesForUser clears observables for a user's agent.
func (s *AgentPoolService) ClearAgentObservablesForUser(userID, name string) error {
	if s.pool == nil {
		return nil
	}
	ag := s.pool.GetAgent(agentKey(userID, name))
	if ag == nil {
		return fmt.Errorf("agent not found: %s", name)
	}
	ag.Observer().ClearHistory()
	return nil
}

// ChatForUser sends a message to a user's agent.
func (s *AgentPoolService) ChatForUser(userID, name, message string) (string, error) {
	if s.natsClient != nil {
		// Distributed mode: dispatch via NATS queue
		return s.dispatchChat(userID, name, message)
	}
	return s.Chat(agentKey(userID, name), message)
}

// dispatchChat publishes a chat event to the NATS agent execution queue.
// The event is enriched with the full agent config and resolved skills so that
// the worker does not need direct database access.
func (s *AgentPoolService) dispatchChat(userID, name, message string) (string, error) {
	messageID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Send user message to SSE immediately so the UI shows it right away
	if s.eventBridge != nil {
		agentName := name
		s.eventBridge.PublishMessage(agentName, userID, "user", message, messageID+"-user")
		s.eventBridge.PublishStatus(agentName, userID, "processing")
	}

	// Load config from DB to embed in the NATS payload
	var cfg *agents.AgentConfig
	if s.agentStore != nil {
		rec, err := s.agentStore.GetConfig(userID, name)
		if err != nil {
			return "", fmt.Errorf("agent config not found: %w", err)
		}
		var c agents.AgentConfig
		if err := agents.ParseConfigJSON(rec.ConfigJSON, &c); err != nil {
			return "", fmt.Errorf("invalid agent config: %w", err)
		}
		cfg = &c
	}

	// Load skills if enabled — uses SkillManager which reads from filesystem/PostgreSQL
	var skills []agents.SkillInfo
	if cfg != nil && cfg.EnableSkills {
		if loaded, err := s.loadSkillsForUser(userID); err == nil {
			skills = loaded
		}
	}

	evt := agents.AgentChatEvent{
		AgentName: name,
		UserID:    userID,
		Message:   message,
		MessageID: messageID,
		Role:      "user",
		Config:    cfg,
		Skills:    skills,
	}
	if err := s.natsClient.Publish(messaging.SubjectAgentExecute, evt); err != nil {
		return "", fmt.Errorf("failed to dispatch agent chat: %w", err)
	}
	return messageID, nil
}

// GetSSEManagerForUser returns the SSE manager for a user's agent.
func (s *AgentPoolService) GetSSEManagerForUser(userID, name string) sse.Manager {
	if s.pool == nil {
		return nil
	}
	return s.pool.GetManager(agentKey(userID, name))
}

// ExportAgentForUser exports a user's agent config.
func (s *AgentPoolService) ExportAgentForUser(userID, name string) ([]byte, error) {
	return s.ExportAgent(agentKey(userID, name))
}

// ImportAgentForUser imports an agent for a user.
func (s *AgentPoolService) ImportAgentForUser(userID string, data []byte) error {
	var cfg state.AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid agent config: %w", err)
	}
	if err := ValidateAgentName(cfg.Name); err != nil {
		return err
	}

	// Auto-generate a user API key when auth is active and none is specified
	if s.authDB != nil && userID != "" && cfg.APIKey == "" {
		plaintext, _, err := auth.CreateAPIKey(s.authDB, userID, "agent:"+cfg.Name, "user", s.appConfig.Auth.APIKeyHMACSecret, nil)
		if err != nil {
			return fmt.Errorf("failed to create API key for agent: %w", err)
		}
		cfg.APIKey = plaintext
	}

	// Persist to PostgreSQL in distributed mode
	if s.agentStore != nil {
		configJSON, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal agent config: %w", err)
		}
		if err := s.agentStore.SaveConfig(&agents.AgentConfigRecord{
			UserID:     userID,
			Name:       cfg.Name,
			ConfigJSON: string(configJSON),
			Status:     "active",
		}); err != nil {
			return fmt.Errorf("failed to save imported agent config: %w", err)
		}
	}

	if s.pool == nil {
		return nil
	}
	key := agentKey(userID, cfg.Name)
	cfg.Name = key
	return s.pool.CreateAgent(key, &cfg)
}

// --- ForUser Collections ---

// CollectionsBackendForUser returns the collections backend for a user.
func (s *AgentPoolService) CollectionsBackendForUser(userID string) (collections.Backend, error) {
	if s.userServices == nil || userID == "" {
		if s.collectionsBackend == nil {
			return nil, fmt.Errorf("collections not available in distributed mode")
		}
		return s.collectionsBackend, nil
	}
	return s.userServices.GetCollections(userID)
}

// --- ForUser Skills ---

// SkillsServiceForUser returns the skills service for a user.
func (s *AgentPoolService) SkillsServiceForUser(userID string) (*skills.Service, error) {
	if s.userServices == nil || userID == "" {
		if s.skillsService == nil {
			return nil, fmt.Errorf("skills service not available")
		}
		return s.skillsService, nil
	}
	return s.userServices.GetSkills(userID)
}

// SkillManagerForUser returns a SkillManager for a specific user.
// In distributed mode, returns a DistributedManager that syncs to PostgreSQL.
// In standalone mode, returns a FilesystemManager.
func (s *AgentPoolService) SkillManagerForUser(userID string) (skillsManager.Manager, error) {
	svc, err := s.SkillsServiceForUser(userID)
	if err != nil {
		return nil, err
	}
	fs := skillsManager.NewFilesystemManager(svc)

	// In distributed mode, wrap with PostgreSQL sync
	if s.skillStore != nil {
		return skillsManager.NewDistributedManager(fs, s.skillStore, userID), nil
	}
	return fs, nil
}

// --- ForUser Jobs ---

// JobServiceForUser returns the agent job service for a user.
func (s *AgentPoolService) JobServiceForUser(userID string) (*AgentJobService, error) {
	if s.userServices == nil || userID == "" {
		return nil, fmt.Errorf("no user services manager or empty user ID")
	}
	return s.userServices.GetJobs(userID)
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

// loadSkillsForUser loads full skill info (name, description, content) for a user.
// Used by dispatchChat and the scheduler to enrich NATS events.
func (s *AgentPoolService) loadSkillsForUser(userID string) ([]agents.SkillInfo, error) {
	mgr, err := s.SkillManagerForUser(userID)
	if err != nil {
		return nil, err
	}
	allSkills, err := mgr.List()
	if err != nil {
		return nil, err
	}
	var skills []agents.SkillInfo
	for _, sk := range allSkills {
		desc := ""
		if sk.Metadata != nil && sk.Metadata.Description != "" {
			desc = sk.Metadata.Description
		}
		if desc == "" {
			d := sk.Content
			if len(d) > 200 {
				d = d[:200] + "..."
			}
			desc = d
		}
		skills = append(skills, agents.SkillInfo{
			Name:        sk.Name,
			Description: desc,
			Content:     sk.Content,
		})
	}
	return skills, nil
}

// buildSkillProvider returns a SkillContentProvider closure for the scheduler.
func (s *AgentPoolService) buildSkillProvider() agents.SkillContentProvider {
	return func(userID string) ([]agents.SkillInfo, error) {
		return s.loadSkillsForUser(userID)
	}
}

// natsPublisherAdapter wraps NATSClient (Publish-only) to satisfy agents.NATSPublisher.
type natsPublisherAdapter struct {
	client NATSClient
}

func (a *natsPublisherAdapter) Publish(subject string, data any) error {
	return a.client.Publish(subject, data)
}

