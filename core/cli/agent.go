package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAGI/core/state"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	"github.com/mudler/xlog"
)

type AgentCMD struct {
	Run  AgentRunCMD  `cmd:"" help:"Run an agent standalone (without the full LocalAI server)"`
	List AgentListCMD `cmd:"" help:"List agents in the pool registry"`
}

type AgentRunCMD struct {
	Name string `arg:"" optional:"" help:"Agent name to run from the pool registry (pool.json)"`

	Config string `short:"c" help:"Path to a JSON agent config file (alternative to loading by name)" type:"path"`
	Prompt string `short:"p" help:"Run in foreground mode: send a single prompt and print the response"`

	// Agent pool settings (mirrors RunCMD agent flags)
	APIURL                string `env:"LOCALAI_AGENT_POOL_API_URL" help:"API URL for the agent to call (e.g. http://127.0.0.1:8080)" group:"agents"`
	APIKey                string `env:"LOCALAI_AGENT_POOL_API_KEY" help:"API key for the agent" group:"agents"`
	DefaultModel          string `env:"LOCALAI_AGENT_POOL_DEFAULT_MODEL" help:"Default model for the agent" group:"agents"`
	MultimodalModel       string `env:"LOCALAI_AGENT_POOL_MULTIMODAL_MODEL" help:"Multimodal model for the agent" group:"agents"`
	TranscriptionModel    string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL" help:"Transcription model for the agent" group:"agents"`
	TranscriptionLanguage string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE" help:"Transcription language for the agent" group:"agents"`
	TTSModel              string `env:"LOCALAI_AGENT_POOL_TTS_MODEL" help:"TTS model for the agent" group:"agents"`
	StateDir              string `env:"LOCALAI_AGENT_POOL_STATE_DIR" default:"agents" help:"State directory containing pool.json" type:"path" group:"agents"`
	Timeout               string `env:"LOCALAI_AGENT_POOL_TIMEOUT" default:"5m" help:"Agent timeout" group:"agents"`
	EnableSkills          bool   `env:"LOCALAI_AGENT_POOL_ENABLE_SKILLS" default:"false" help:"Enable skills service" group:"agents"`
	EnableLogs            bool   `env:"LOCALAI_AGENT_POOL_ENABLE_LOGS" default:"false" help:"Enable agent logging" group:"agents"`
	CustomActionsDir      string `env:"LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR" help:"Custom actions directory" group:"agents"`
}

func (r *AgentRunCMD) Run(ctx *cliContext.Context) error {
	if r.Name == "" && r.Config == "" {
		return fmt.Errorf("either an agent name or --config must be provided")
	}

	agentConfig, err := r.loadAgentConfig()
	if err != nil {
		return err
	}

	// Override agent config fields from CLI flags when provided
	r.applyOverrides(agentConfig)

	xlog.Info("Starting standalone agent", "name", agentConfig.Name)

	appConfig := r.buildAppConfig()

	poolService, err := services.NewAgentPoolService(appConfig)
	if err != nil {
		return fmt.Errorf("failed to create agent pool service: %w", err)
	}

	if err := poolService.Start(appConfig.Context); err != nil {
		return fmt.Errorf("failed to start agent pool service: %w", err)
	}
	defer poolService.Stop()

	pool := poolService.Pool()

	// Start the agent standalone (does not persist to pool.json)
	if err := pool.StartAgentStandalone(agentConfig.Name, agentConfig); err != nil {
		return fmt.Errorf("failed to start agent %q: %w", agentConfig.Name, err)
	}

	ag := pool.GetAgent(agentConfig.Name)
	if ag == nil {
		return fmt.Errorf("agent %q not found after start", agentConfig.Name)
	}

	// Foreground mode: send a single prompt and exit
	if r.Prompt != "" {
		xlog.Info("Sending prompt to agent", "agent", agentConfig.Name)
		result := ag.Ask(coreTypes.WithText(r.Prompt))
		if result == nil {
			return fmt.Errorf("agent returned no result")
		}
		if result.Error != nil {
			return fmt.Errorf("agent error: %w", result.Error)
		}
		fmt.Println(result.Response)
		return nil
	}

	// Background mode: run until interrupted
	xlog.Info("Agent running in background mode. Press Ctrl+C to stop.", "agent", agentConfig.Name)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	xlog.Info("Shutting down agent", "agent", agentConfig.Name)
	return nil
}

func (r *AgentRunCMD) loadAgentConfig() (*state.AgentConfig, error) {
	// Load from JSON config file
	if r.Config != "" {
		data, err := os.ReadFile(r.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %q: %w", r.Config, err)
		}
		var cfg state.AgentConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file %q: %w", r.Config, err)
		}
		if cfg.Name == "" {
			return nil, fmt.Errorf("agent config must have a name")
		}
		return &cfg, nil
	}

	// Load from pool.json by name
	poolFile := r.StateDir + "/pool.json"
	data, err := os.ReadFile(poolFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read pool registry %q: %w", poolFile, err)
	}

	var pool map[string]state.AgentConfig
	if err := json.Unmarshal(data, &pool); err != nil {
		return nil, fmt.Errorf("failed to parse pool registry %q: %w", poolFile, err)
	}

	cfg, ok := pool[r.Name]
	if !ok {
		available := make([]string, 0, len(pool))
		for name := range pool {
			available = append(available, name)
		}
		return nil, fmt.Errorf("agent %q not found in pool registry. Available agents: %v", r.Name, available)
	}

	cfg.Name = r.Name
	return &cfg, nil
}

func (r *AgentRunCMD) applyOverrides(cfg *state.AgentConfig) {
	if r.APIURL != "" {
		cfg.APIURL = r.APIURL
	}
	if r.APIKey != "" {
		cfg.APIKey = r.APIKey
	}
	if r.DefaultModel != "" && cfg.Model == "" {
		cfg.Model = r.DefaultModel
	}
	if r.MultimodalModel != "" && cfg.MultimodalModel == "" {
		cfg.MultimodalModel = r.MultimodalModel
	}
	if r.TranscriptionModel != "" && cfg.TranscriptionModel == "" {
		cfg.TranscriptionModel = r.TranscriptionModel
	}
	if r.TranscriptionLanguage != "" && cfg.TranscriptionLanguage == "" {
		cfg.TranscriptionLanguage = r.TranscriptionLanguage
	}
	if r.TTSModel != "" && cfg.TTSModel == "" {
		cfg.TTSModel = r.TTSModel
	}
}

func (r *AgentRunCMD) buildAppConfig() *config.ApplicationConfig {
	appConfig := &config.ApplicationConfig{
		Context: context.Background(),
	}
	appConfig.AgentPool = config.AgentPoolConfig{
		Enabled:               true,
		APIURL:                r.APIURL,
		APIKey:                r.APIKey,
		DefaultModel:          r.DefaultModel,
		MultimodalModel:       r.MultimodalModel,
		TranscriptionModel:    r.TranscriptionModel,
		TranscriptionLanguage: r.TranscriptionLanguage,
		TTSModel:              r.TTSModel,
		StateDir:              r.StateDir,
		Timeout:               r.Timeout,
		EnableSkills:          r.EnableSkills,
		EnableLogs:            r.EnableLogs,
		CustomActionsDir:      r.CustomActionsDir,
	}
	return appConfig
}

type AgentListCMD struct {
	StateDir string `env:"LOCALAI_AGENT_POOL_STATE_DIR" default:"agents" help:"State directory containing pool.json" type:"path" group:"agents"`
}

func (r *AgentListCMD) Run(ctx *cliContext.Context) error {
	poolFile := r.StateDir + "/pool.json"
	data, err := os.ReadFile(poolFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No agents found (pool.json does not exist)")
			return nil
		}
		return fmt.Errorf("failed to read pool registry %q: %w", poolFile, err)
	}

	var pool map[string]state.AgentConfig
	if err := json.Unmarshal(data, &pool); err != nil {
		return fmt.Errorf("failed to parse pool registry %q: %w", poolFile, err)
	}

	if len(pool) == 0 {
		fmt.Println("No agents found in pool registry")
		return nil
	}

	fmt.Printf("Agents in %s:\n", poolFile)
	for name, cfg := range pool {
		model := cfg.Model
		if model == "" {
			model = "(default)"
		}
		desc := cfg.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Printf("  - %s [model: %s] %s\n", name, model, desc)
	}
	return nil
}
