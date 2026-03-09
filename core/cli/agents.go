package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/xlog"
)

// AgentsRunFlags contains flags for running an agent in standalone mode
type AgentsRunFlags struct {
	// AgentName is the name of the agent to run
	AgentName string `arg:"" optional:"" name:"agent" help:"Name of the agent to run"`
	// ConfigFile is the path to the JSON configuration file
	ConfigFile string `arg:"" optional:"" name:"config" help:"Path to JSON configuration file"`

	// Agent pool configuration options (matching AgentPoolConfig)
	AgentPoolStateDir           string `env:"LOCALAI_AGENT_POOL_STATE_DIR" type:"path" help:"State directory for agent pool" group:"agents"`
	AgentPoolAPIURL             string `env:"LOCALAI_AGENT_POOL_API_URL" help:"Default API URL for agents" group:"agents"`
	AgentPoolAPIKey             string `env:"LOCALAI_AGENT_POOL_API_KEY" help:"Default API key for agents" group:"agents"`
	AgentPoolDefaultModel       string `env:"LOCALAI_AGENT_POOL_DEFAULT_MODEL" help:"Default model for agents" group:"agents"`
	AgentPoolMultimodalModel    string `env:"LOCALAI_AGENT_POOL_MULTIMODAL_MODEL" help:"Default multimodal model for agents" group:"agents"`
	AgentPoolTranscriptionModel string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL" help:"Default transcription model for agents" group:"agents"`
	AgentPoolTranscriptionLanguage string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE" help:"Default transcription language for agents" group:"agents"`
	AgentPoolTTSModel           string `env:"LOCALAI_AGENT_POOL_TTS_MODEL" help:"Default TTS model for agents" group:"agents"`
	AgentPoolTimeout            string `env:"LOCALAI_AGENT_POOL_TIMEOUT" default:"5m" help:"Default agent timeout" group:"agents"`
	AgentPoolEnableSkills       bool   `env:"LOCALAI_AGENT_POOL_ENABLE_SKILLS" default:"false" help:"Enable skills service for agents" group:"agents"`
	AgentPoolVectorEngine       string `env:"LOCALAI_AGENT_POOL_VECTOR_ENGINE" default:"chromem" help:"Vector engine type for agent knowledge base" group:"agents"`
	AgentPoolEmbeddingModel     string `env:"LOCALAI_AGENT_POOL_EMBEDDING_MODEL" default:"granite-embedding-107m-multilingual" help:"Embedding model for agent knowledge base" group:"agents"`
	AgentPoolCustomActionsDir   string `env:"LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR" help:"Custom actions directory for agents" group:"agents"`
	AgentPoolDatabaseURL        string `env:"LOCALAI_AGENT_POOL_DATABASE_URL" help:"Database URL for agent collections" group:"agents"`
	AgentPoolMaxChunkingSize    int    `env:"LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE" default:"400" help:"Maximum chunking size for knowledge base documents" group:"agents"`
	AgentPoolChunkOverlap       int    `env:"LOCALAI_AGENT_POOL_CHUNK_OVERLAP" default:"0" help:"Chunk overlap size for knowledge base documents" group:"agents"`
	AgentPoolEnableLogs         bool   `env:"LOCALAI_AGENT_POOL_ENABLE_LOGS" default:"false" help:"Enable agent logging" group:"agents"`
	AgentPoolCollectionDBPath   string `env:"LOCALAI_AGENT_POOL_COLLECTION_DB_PATH" help:"Database path for agent collections" group:"agents"`
	AgentPoolAgentHubURL        string `env:"LOCALAI_AGENT_POOL_AGENT_HUB_URL" help:"URL for the agent hub" group:"agents"`
}

// AgentsCMD is the parent command for agent-related operations
type AgentsCMD struct {
	Run AgentsRunFlags `cmd:"" help:"Run an agent in standalone mode"`
}

func (ar *AgentsRunFlags) Run(ctx *cliContext.Context) error {
	// Validate that either agent name or config file is provided
	if ar.AgentName == "" && ar.ConfigFile == "" {
		return fmt.Errorf("either agent name or config file must be provided\n\nUsage:\n  local-ai agents run <agent-name> [config-file.json]\n  local-ai agents run --config=config-file.json")
	}

	// If config file is provided, validate it exists
	if ar.ConfigFile != "" {
		if _, err := os.Stat(ar.ConfigFile); os.IsNotExist(err) {
			return fmt.Errorf("config file not found: %s", ar.ConfigFile)
		}
	}

	// Load and merge config if provided
	var agentConfig map[string]interface{}
	if ar.ConfigFile != "" {
		configData, err := os.ReadFile(ar.ConfigFile)
		if err != nil {
			return fmt.Errorf("failed to read config file: %w", err)
		}

		if err := json.Unmarshal(configData, &agentConfig); err != nil {
			return fmt.Errorf("failed to parse config file: %w", err)
		}

		xlog.Info("Loaded agent configuration from file", "file", ar.ConfigFile)
	}

	// Determine state directory
	stateDir := ar.AgentPoolStateDir
	if stateDir == "" {
		// Use a default location relative to current directory
		stateDir = "./agents-data"
	}

	// Check if agent name is provided in config file
	agentName := ar.AgentName
	if agentName == "" {
		if name, ok := agentConfig["name"].(string); ok {
			agentName = name
		} else {
			agentName = "default-agent"
		}
	}

	// Ensure state directory exists
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Create application config with agent pool settings
	appConfig := &config.ApplicationConfig{
		Context: context.Background(),
		AgentPool: config.AgentPoolConfig{
			Enabled:                true,
			StateDir:               stateDir,
			APIURL:                 ar.AgentPoolAPIURL,
			APIKey:                 ar.AgentPoolAPIKey,
			DefaultModel:           ar.AgentPoolDefaultModel,
			MultimodalModel:        ar.AgentPoolMultimodalModel,
			TranscriptionModel:     ar.AgentPoolTranscriptionModel,
			TranscriptionLanguage:  ar.AgentPoolTranscriptionLanguage,
			TTSModel:               ar.AgentPoolTTSModel,
			Timeout:                ar.AgentPoolTimeout,
			EnableSkills:           ar.AgentPoolEnableSkills,
			VectorEngine:           ar.AgentPoolVectorEngine,
			EmbeddingModel:         ar.AgentPoolEmbeddingModel,
			CustomActionsDir:       ar.AgentPoolCustomActionsDir,
			DatabaseURL:            ar.AgentPoolDatabaseURL,
			MaxChunkingSize:        ar.AgentPoolMaxChunkingSize,
			ChunkOverlap:           ar.AgentPoolChunkOverlap,
			EnableLogs:             ar.AgentPoolEnableLogs,
			CollectionDBPath:       ar.AgentPoolCollectionDBPath,
			AgentHubURL:            ar.AgentPoolAgentHubURL,
		},
	}

	// Merge config file values if provided
	if len(agentConfig) > 0 {
		if apiURL, ok := agentConfig["api_url"].(string); ok {
			appConfig.AgentPool.APIURL = apiURL
		}
		if apiKey, ok := agentConfig["api_key"].(string); ok {
			appConfig.AgentPool.APIKey = apiKey
		}
		if defaultModel, ok := agentConfig["default_model"].(string); ok {
			appConfig.AgentPool.DefaultModel = defaultModel
		}
		if stateDir, ok := agentConfig["state_dir"].(string); ok {
			appConfig.AgentPool.StateDir = stateDir
		}
	}

	xlog.Info("Starting agent in standalone mode",
		"name", agentName,
		"state_dir", appConfig.AgentPool.StateDir,
		"api_url", appConfig.AgentPool.APIURL,
		"default_model", appConfig.AgentPool.DefaultModel)

	// Create the agent pool service
	agentService, err := services.NewAgentPoolService(appConfig)
	if err != nil {
		return fmt.Errorf("failed to create agent pool service: %w", err)
	}

	// Start the agent pool service
	if err := agentService.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start agent pool service: %w", err)
	}

	xlog.Info("Agent pool service started successfully")
	xlog.Info("Agent running in standalone mode. Press Ctrl+C to stop.")

	// Keep running until interrupted
	<-context.Background().Done()

	xlog.Info("Shutting down agent pool service...")
	if err := agentService.Shutdown(context.Background()); err != nil {
		xlog.Error("Error during agent pool service shutdown", "error", err)
	}

	xlog.Info("Agent pool service shutdown complete")
	return nil
}
