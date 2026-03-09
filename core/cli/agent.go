package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/xlog"
)

type AgentCMD struct {
	Run AgentRunCMD `cmd:"" help:"Run an agent in standalone mode" default:"withargs"`
}

type AgentRunCMD struct {
	// Positional argument: agent name or path to a JSON configuration file
	AgentRef string `arg:"" required:"" help:"Agent name (from registry) or path to a JSON agent configuration file"`

	// API connection settings for the agent to use
	APIURL string `env:"LOCALAI_AGENT_POOL_API_URL" help:"API URL for the agent to use (e.g. http://localhost:8080)" group:"agent"`
	APIKey string `env:"LOCALAI_AGENT_POOL_API_KEY" help:"API key for the agent" group:"agent"`

	// Agent pool settings
	DefaultModel          string `env:"LOCALAI_AGENT_POOL_DEFAULT_MODEL" help:"Default model for the agent" group:"agent"`
	MultimodalModel       string `env:"LOCALAI_AGENT_POOL_MULTIMODAL_MODEL" help:"Multimodal model for the agent" group:"agent"`
	TranscriptionModel    string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL" help:"Transcription model for the agent" group:"agent"`
	TranscriptionLanguage string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE" help:"Transcription language for the agent" group:"agent"`
	TTSModel              string `env:"LOCALAI_AGENT_POOL_TTS_MODEL" help:"TTS model for the agent" group:"agent"`
	StateDir              string `env:"LOCALAI_AGENT_POOL_STATE_DIR" default:"agent-state" help:"State directory for the agent" group:"agent"`
	Timeout               string `env:"LOCALAI_AGENT_POOL_TIMEOUT" default:"5m" help:"Agent timeout" group:"agent"`
	EnableSkills          bool   `env:"LOCALAI_AGENT_POOL_ENABLE_SKILLS" default:"false" help:"Enable skills service" group:"agent"`
	EnableLogs            bool   `env:"LOCALAI_AGENT_POOL_ENABLE_LOGS" default:"false" help:"Enable agent logging" group:"agent"`
	CustomActionsDir      string `env:"LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR" help:"Custom actions directory" group:"agent"`

	// Registry settings
	AgentHubURL string `env:"LOCALAI_AGENT_HUB_URL" default:"https://agenthub.localai.io" help:"Agent hub URL for registry lookups" group:"registry"`
}

func (a *AgentRunCMD) Run(ctx *cliContext.Context) error {
	agentConfig, err := a.resolveAgentConfig()
	if err != nil {
		return fmt.Errorf("failed to resolve agent configuration: %w", err)
	}

	// Apply CLI overrides to the agent config
	a.applyOverrides(agentConfig)

	if agentConfig.Name == "" {
		return fmt.Errorf("agent configuration must have a name")
	}

	xlog.Info("Starting agent in standalone mode", "name", agentConfig.Name)

	appConfig := config.NewApplicationConfig(
		config.WithContext(context.Background()),
		config.WithAPIAddress(":0"), // not serving HTTP
		config.WithAgentPoolAPIURL(agentConfig.APIURL),
		config.WithAgentPoolAPIKey(agentConfig.APIKey),
		config.WithAgentPoolStateDir(a.StateDir),
		config.WithAgentPoolTimeout(a.Timeout),
	)

	if a.DefaultModel != "" {
		config.WithAgentPoolDefaultModel(a.DefaultModel)(appConfig)
	}
	if a.MultimodalModel != "" {
		config.WithAgentPoolMultimodalModel(a.MultimodalModel)(appConfig)
	}
	if a.TranscriptionModel != "" {
		config.WithAgentPoolTranscriptionModel(a.TranscriptionModel)(appConfig)
	}
	if a.TranscriptionLanguage != "" {
		config.WithAgentPoolTranscriptionLanguage(a.TranscriptionLanguage)(appConfig)
	}
	if a.TTSModel != "" {
		config.WithAgentPoolTTSModel(a.TTSModel)(appConfig)
	}
	if a.EnableSkills {
		config.EnableAgentPoolSkills(appConfig)
	}
	if a.EnableLogs {
		config.EnableAgentPoolLogs(appConfig)
	}
	if a.CustomActionsDir != "" {
		config.WithAgentPoolCustomActionsDir(a.CustomActionsDir)(appConfig)
	}

	svc, err := services.NewAgentPoolService(appConfig)
	if err != nil {
		return fmt.Errorf("failed to create agent pool service: %w", err)
	}

	if err := svc.Start(appConfig.Context); err != nil {
		return fmt.Errorf("failed to start agent pool service: %w", err)
	}
	defer svc.Stop()

	if err := svc.CreateAgent(agentConfig); err != nil {
		return fmt.Errorf("failed to create agent %q: %w", agentConfig.Name, err)
	}

	xlog.Info("Agent started successfully", "name", agentConfig.Name)

	// Optionally, if the user specifies a prompt, we will ask the agent directly and exit.
	// No background service in that case.

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	xlog.Info("Shutting down agent", "name", agentConfig.Name)
	return nil
}

// resolveAgentConfig determines whether AgentRef is a local JSON file or a registry name,
// and returns the parsed agent configuration.
func (a *AgentRunCMD) resolveAgentConfig() (*state.AgentConfig, error) {
	// Check if the reference is a local file
	if isJSONFile(a.AgentRef) {
		return a.loadFromFile(a.AgentRef)
	}

	// Try as a registry name
	return a.loadFromRegistry(a.AgentRef)
}

// loadFromFile reads and validates an agent configuration from a JSON file.
func (a *AgentRunCMD) loadFromFile(path string) (*state.AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent config file %q: %w", path, err)
	}

	var cfg state.AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse agent config file %q: %w", path, err)
	}

	return &cfg, nil
}

// loadFromRegistry fetches an agent configuration from the agent hub registry.
func (a *AgentRunCMD) loadFromRegistry(name string) (*state.AgentConfig, error) {
	hubURL := strings.TrimRight(a.AgentHubURL, "/")
	endpoint := fmt.Sprintf("%s/agents/%s.json", hubURL, name)

	xlog.Info("Fetching agent configuration from registry", "name", name, "url", endpoint)

	resp, err := http.Get(endpoint) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent %q from registry: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("agent %q not found in registry at %s", name, hubURL)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("registry returned status %d for agent %q: %s", resp.StatusCode, name, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read registry response for agent %q: %w", name, err)
	}

	var cfg state.AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse registry response for agent %q: %w", name, err)
	}

	if cfg.Name == "" {
		cfg.Name = name
	}

	return &cfg, nil
}

// applyOverrides applies CLI flag values to the agent config when they are set,
// allowing users to override values from the file or registry.
func (a *AgentRunCMD) applyOverrides(cfg *state.AgentConfig) {
	if a.APIURL != "" && cfg.APIURL == "" {
		cfg.APIURL = a.APIURL
	}
	if a.APIKey != "" && cfg.APIKey == "" {
		cfg.APIKey = a.APIKey
	}
	if a.DefaultModel != "" && cfg.Model == "" {
		cfg.Model = a.DefaultModel
	}
	if a.MultimodalModel != "" && cfg.MultimodalModel == "" {
		cfg.MultimodalModel = a.MultimodalModel
	}
	if a.TranscriptionModel != "" && cfg.TranscriptionModel == "" {
		cfg.TranscriptionModel = a.TranscriptionModel
	}
	if a.TranscriptionLanguage != "" && cfg.TranscriptionLanguage == "" {
		cfg.TranscriptionLanguage = a.TranscriptionLanguage
	}
	if a.TTSModel != "" && cfg.TTSModel == "" {
		cfg.TTSModel = a.TTSModel
	}
}

// isJSONFile returns true if the path looks like a reference to a JSON file.
func isJSONFile(ref string) bool {
	if strings.HasSuffix(ref, ".json") {
		return true
	}
	// Check if the file exists on disk (handles paths without .json extension)
	info, err := os.Stat(ref)
	return err == nil && !info.IsDir()
}
