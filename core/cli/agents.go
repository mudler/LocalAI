package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/xlog"
)

// AgentsCMD is the container for agent-related CLI commands
type AgentsCMD struct {
	Run RunAgentCMD `cmd:"" name:"run" help:"Run an agent in standalone mode"`
}

// RunAgentCMD runs a specific agent with its configuration
type RunAgentCMD struct {
	// Agent name as argument
	AgentName string `arg:"" help:"Name of the agent to run"`

	// JSON file path containing agent configuration
	ConfigFile string `arg:"" optional:"" help:"Path to JSON file containing agent configuration"`

	// Agent Pool Configuration (similar to run.go)
	AgentPoolAPIURL                string `env:"LOCALAI_AGENT_POOL_API_URL" help:"API URL for agents" group:"agents"`
	AgentPoolAPIKey                string `env:"LOCALAI_AGENT_POOL_API_KEY" help:"API key for agents" group:"agents"`
	AgentPoolDefaultModel          string `env:"LOCALAI_AGENT_POOL_DEFAULT_MODEL" help:"Default model for agents" group:"agents"`
	AgentPoolMultimodalModel       string `env:"LOCALAI_AGENT_POOL_MULTIMODAL_MODEL" help:"Default multimodal model for agents" group:"agents"`
	AgentPoolTranscriptionModel    string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL" help:"Default transcription model for agents" group:"agents"`
	AgentPoolTranscriptionLanguage string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE" help:"Default transcription language for agents" group:"agents"`
	AgentPoolTTSModel              string `env:"LOCALAI_AGENT_POOL_TTS_MODEL" help:"Default TTS model for agents" group:"agents"`
	AgentPoolStateDir              string `env:"LOCALAI_AGENT_POOL_STATE_DIR" type:"path" help:"State directory for agent pool" group:"agents"`
	AgentPoolTimeout               string `env:"LOCALAI_AGENT_POOL_TIMEOUT" default:"5m" help:"Default agent timeout" group:"agents"`
	AgentPoolEnableSkills          bool   `env:"LOCALAI_AGENT_POOL_ENABLE_SKILLS" help:"Enable skills service for agents" group:"agents"`
	AgentPoolVectorEngine          string `env:"LOCALAI_AGENT_POOL_VECTOR_ENGINE" default:"chromem" help:"Vector engine type for agent knowledge base" group:"agents"`
	AgentPoolEmbeddingModel        string `env:"LOCALAI_AGENT_POOL_EMBEDDING_MODEL" default:"granite-embedding-107m-multilingual" help:"Embedding model for agent knowledge base" group:"agents"`
	AgentPoolCustomActionsDir      string `env:"LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR" type:"path" help:"Custom actions directory for agents" group:"agents"`
	AgentPoolDatabaseURL           string `env:"LOCALAI_AGENT_POOL_DATABASE_URL" help:"Database URL for agent collections" group:"agents"`
	AgentPoolMaxChunkingSize       int    `env:"LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE" default:"400" help:"Maximum chunking size for knowledge base documents" group:"agents"`
	AgentPoolChunkOverlap          int    `env:"LOCALAI_AGENT_POOL_CHUNK_OVERLAP" default:"0" help:"Chunk overlap size for knowledge base documents" group:"agents"`
	AgentPoolEnableLogs            bool   `env:"LOCALAI_AGENT_POOL_ENABLE_LOGS" help:"Enable agent logging" group:"agents"`
	AgentPoolCollectionDBPath      string `env:"LOCALAI_AGENT_POOL_COLLECTION_DB_PATH" type:"path" help:"Database path for agent collections" group:"agents"`
	AgentHubURL                    string `env:"LOCALAI_AGENT_HUB_URL" default:"https://agenthub.localai.io" help:"URL for the agent hub" group:"agents"`

	// Storage paths
	DataPath                string `env:"LOCALAI_DATA_PATH" type:"path" default:"${basepath}/data" help:"Path for persistent data" group:"storage"`
	LocalaiConfigDir        string `env:"LOCALAI_CONFIG_DIR" type:"path" default:"${basepath}/configuration" help:"Directory for dynamic configuration" group:"storage"`
	Galleries               string `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models" default:"${galleries}"`
	BackendGalleries        string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends"`

	// API settings
	Address         string   `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	APIKeys         []string `env:"LOCALAI_API_KEY,API_KEY" help:"API keys for authentication" group:"api"`
	DisableWebUI    bool     `env:"LOCALAI_DISABLE_WEBUI,DISABLE_WEBUI" default:"true" help:"Disable the web UI (default true for standalone agent mode)" group:"api"`

	// Debug
	Debug bool `env:"LOCALAI_DEBUG,DEBUG" help:"Enable debug logging" group:"debug"`
}

func (r *RunAgentCMD) Run(ctx *cliContext.Context) error {
	xlog.Info("Running agent in standalone mode", "agent", r.AgentName)

	// Load agent configuration from file if provided
	var agentConfig map[string]interface{}
	if r.ConfigFile != "" {
		configPath, err := filepath.Abs(r.ConfigFile)
		if err != nil {
			return fmt.Errorf("invalid config file path: %w", err)
		}

		configData, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("failed to read config file: %w", err)
		}

		if err := json.Unmarshal(configData, &agentConfig); err != nil {
			return fmt.Errorf("failed to parse config file: %w", err)
		}

		xlog.Info("Agent configuration loaded", "file", r.ConfigFile)
	} else {
		xlog.Info("No configuration file provided, using default agent configuration")
	}

	// Set up data path
	dataPath := r.DataPath
	if dataPath == "" {
		dataPath = filepath.Join(os.TempDir(), "localai", "data")
	}
	if err := os.MkdirAll(dataPath, 0750); err != nil {
		return fmt.Errorf("failed to create data path: %w", err)
	}

	// Set up state directory for agent pool
	stateDir := r.AgentPoolStateDir
	if stateDir == "" {
		stateDir = filepath.Join(dataPath, "agents")
	}
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return fmt.Errorf("failed to create agent state directory: %w", err)
	}

	// Build application options
	opts := []config.AppOption{
		config.WithContext(context.Background()),
		config.WithDataPath(dataPath),
		config.WithDynamicConfigDir(r.LocalaiConfigDir),
		config.WithStringGalleries(r.Galleries),
		config.WithBackendGalleries(r.BackendGalleries),
		config.WithApiKeys(r.APIKeys),
		config.WithAPIAddress(r.Address),
		config.WithDebug(ctx.Debug || r.Debug),
		config.DisableWebUI, // Disable web UI for standalone agent mode
	}

	// Agent Pool configuration
	if r.AgentPoolAPIURL != "" {
		opts = append(opts, config.WithAgentPoolAPIURL(r.AgentPoolAPIURL))
	}
	if r.AgentPoolAPIKey != "" {
		opts = append(opts, config.WithAgentPoolAPIKey(r.AgentPoolAPIKey))
	}
	if r.AgentPoolDefaultModel != "" {
		opts = append(opts, config.WithAgentPoolDefaultModel(r.AgentPoolDefaultModel))
	}
	if r.AgentPoolMultimodalModel != "" {
		opts = append(opts, config.WithAgentPoolMultimodalModel(r.AgentPoolMultimodalModel))
	}
	if r.AgentPoolTranscriptionModel != "" {
		opts = append(opts, config.WithAgentPoolTranscriptionModel(r.AgentPoolTranscriptionModel))
	}
	if r.AgentPoolTranscriptionLanguage != "" {
		opts = append(opts, config.WithAgentPoolTranscriptionLanguage(r.AgentPoolTranscriptionLanguage))
	}
	if r.AgentPoolTTSModel != "" {
		opts = append(opts, config.WithAgentPoolTTSModel(r.AgentPoolTTSModel))
	}
	if r.AgentPoolStateDir != "" {
		opts = append(opts, config.WithAgentPoolStateDir(r.AgentPoolStateDir))
	}
	if r.AgentPoolTimeout != "" {
		opts = append(opts, config.WithAgentPoolTimeout(r.AgentPoolTimeout))
	}
	if r.AgentPoolEnableSkills {
		opts = append(opts, config.EnableAgentPoolSkills)
	}
	if r.AgentPoolVectorEngine != "" {
		opts = append(opts, config.WithAgentPoolVectorEngine(r.AgentPoolVectorEngine))
	}
	if r.AgentPoolEmbeddingModel != "" {
		opts = append(opts, config.WithAgentPoolEmbeddingModel(r.AgentPoolEmbeddingModel))
	}
	if r.AgentPoolCustomActionsDir != "" {
		opts = append(opts, config.WithAgentPoolCustomActionsDir(r.AgentPoolCustomActionsDir))
	}
	if r.AgentPoolDatabaseURL != "" {
		opts = append(opts, config.WithAgentPoolDatabaseURL(r.AgentPoolDatabaseURL))
	}
	if r.AgentPoolMaxChunkingSize > 0 {
		opts = append(opts, config.WithAgentPoolMaxChunkingSize(r.AgentPoolMaxChunkingSize))
	}
	if r.AgentPoolChunkOverlap > 0 {
		opts = append(opts, config.WithAgentPoolChunkOverlap(r.AgentPoolChunkOverlap))
	}
	if r.AgentPoolEnableLogs {
		opts = append(opts, config.EnableAgentPoolLogs)
	}
	if r.AgentPoolCollectionDBPath != "" {
		opts = append(opts, config.WithAgentPoolCollectionDBPath(r.AgentPoolCollectionDBPath))
	}
	if r.AgentHubURL != "" {
		opts = append(opts, config.WithAgentHubURL(r.AgentHubURL))
	}

	// Create the application
	app, err := config.NewApplication(opts...)
	if err != nil {
		return fmt.Errorf("failed to create application: %w", err)
	}

	xlog.Info("LocalAI agent standalone mode starting", "agent", r.AgentName, "address", r.Address)

	// Create HTTP API
	appHTTP, err := http.API(app)
	if err != nil {
		xlog.Error("error during HTTP App construction", "error", err)
		return err
	}

	// Register graceful termination
	signals.RegisterGracefulTerminationHandler(func() {
		xlog.Info("Shutting down agent standalone mode")
		if app.AgentPoolService() != nil {
			app.AgentPoolService().Stop()
		}
	})

	// Start the HTTP server
	if err := appHTTP.Start(r.Address); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Keep running until shutdown signal
	select {}
}
