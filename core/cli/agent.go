package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/template"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/xlog"
)

// AgentCMD is the top-level command group for agent operations.
type AgentCMD struct {
	Run AgentRunCMD `cmd:"" help:"Run an agent task. Specify a task name to look up from the task registry, or use --config to provide a JSON configuration file"`
}

// AgentRunConfig is the JSON configuration format for running an agent from a config file.
type AgentRunConfig struct {
	Name       string            `json:"name"`
	Model      string            `json:"model"`
	Prompt     string            `json:"prompt"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

// AgentRunCMD executes an agent task.
type AgentRunCMD struct {
	TaskName string `arg:"" optional:"" help:"Name of the task to run from the task registry"`

	Config     string `short:"c" help:"Path to a JSON configuration file defining the agent task" type:"path"`
	ModelsPath string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	TasksDir   string `env:"LOCALAI_CONFIG_DIR" type:"path" default:"${basepath}/configuration" help:"Directory containing agent_tasks.json" group:"storage"`

	// LocalAI API connection (for the LLM backend)
	APIURL string `env:"LOCALAI_API_URL" default:"http://127.0.0.1:8080" help:"LocalAI API base URL for model inference" group:"api"`
	APIKey string `env:"LOCALAI_API_KEY" help:"API key for LocalAI" group:"api"`

	// Template parameters
	Param []string `short:"p" help:"Template parameters in key=value format (can be repeated)" group:"execution"`
}

func (a *AgentRunCMD) Run(ctx *cliContext.Context) error {
	if a.TaskName == "" && a.Config == "" {
		return fmt.Errorf("either a task name or --config must be provided")
	}
	if a.TaskName != "" && a.Config != "" {
		return fmt.Errorf("specify either a task name or --config, not both")
	}

	// Parse parameters from --param flags
	params := parseParams(a.Param)

	if a.Config != "" {
		return a.runFromConfig(params)
	}

	return a.runFromRegistry(params)
}

// runFromRegistry looks up a task by name from agent_tasks.json and runs it.
func (a *AgentRunCMD) runFromRegistry(params map[string]string) error {
	tasksFile := a.TasksDir + "/agent_tasks.json"
	if _, err := os.Stat(tasksFile); os.IsNotExist(err) {
		return fmt.Errorf("task registry not found: %s", tasksFile)
	}

	data, err := os.ReadFile(tasksFile)
	if err != nil {
		return fmt.Errorf("failed to read task registry: %w", err)
	}

	var tf schema.TasksFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return fmt.Errorf("failed to parse task registry: %w", err)
	}

	var task *schema.Task
	for i := range tf.Tasks {
		if tf.Tasks[i].Name == a.TaskName || tf.Tasks[i].ID == a.TaskName {
			task = &tf.Tasks[i]
			break
		}
	}
	if task == nil {
		return fmt.Errorf("task %q not found in registry", a.TaskName)
	}

	if !task.Enabled {
		return fmt.Errorf("task %q is disabled", a.TaskName)
	}

	return a.executeTask(task.Model, task.Prompt, params)
}

// runFromConfig reads a JSON config file and runs the agent task defined in it.
func (a *AgentRunCMD) runFromConfig(params map[string]string) error {
	data, err := os.ReadFile(a.Config)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg AgentRunConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Model == "" {
		return fmt.Errorf("config file must specify a model")
	}
	if cfg.Prompt == "" {
		return fmt.Errorf("config file must specify a prompt")
	}

	// Merge config-level parameters with CLI parameters (CLI takes precedence)
	merged := make(map[string]string)
	for k, v := range cfg.Parameters {
		merged[k] = v
	}
	for k, v := range params {
		merged[k] = v
	}

	return a.executeTask(cfg.Model, cfg.Prompt, merged)
}

// executeTask sets up the model infrastructure and runs the agent with cogito.
func (a *AgentRunCMD) executeTask(modelName, promptTemplate string, params map[string]string) error {
	// Build the prompt from the template
	prompt, err := buildPromptFromTemplate(promptTemplate, params)
	if err != nil {
		return fmt.Errorf("failed to build prompt: %w", err)
	}

	systemState, err := system.GetSystemState(
		system.WithModelPath(a.ModelsPath),
	)
	if err != nil {
		return err
	}

	appConfig := &config.ApplicationConfig{
		SystemState: systemState,
		Context:     context.Background(),
	}

	cl := config.NewModelConfigLoader(a.ModelsPath)
	ml := model.NewModelLoader(systemState)
	_ = templates.NewEvaluator(a.ModelsPath)

	defer func() {
		if err := ml.StopAllGRPC(); err != nil {
			xlog.Error("unable to stop all grpc processes", "error", err)
		}
	}()

	if err := cl.LoadModelConfigsFromPath(a.ModelsPath); err != nil {
		return fmt.Errorf("failed to load model configs: %w", err)
	}

	modelConfig, err := cl.LoadModelConfigFileByNameDefaultOptions(modelName, appConfig)
	if err != nil {
		return fmt.Errorf("failed to load model config for %q: %w", modelName, err)
	}

	// Validate MCP configuration
	if modelConfig.MCP.Servers == "" && modelConfig.MCP.Stdio == "" {
		return fmt.Errorf("model %q has no MCP servers configured (MCP configuration is required for agent execution)", modelName)
	}

	remote, stdio, err := modelConfig.MCP.MCPConfigFromYAML()
	if err != nil {
		return fmt.Errorf("failed to parse MCP config: %w", err)
	}

	sessions, err := mcpTools.SessionsFromMCPConfig(modelConfig.Name, remote, stdio)
	if err != nil {
		return fmt.Errorf("failed to create MCP sessions: %w", err)
	}

	if len(sessions) == 0 {
		return fmt.Errorf("no working MCP servers found for model %q", modelName)
	}

	// Create cogito fragment with the prompt
	fragment := cogito.NewEmptyFragment()
	fragment = fragment.AddMessage("user", prompt)

	// Create LLM client
	defaultLLM := clients.NewLocalAILLM(modelConfig.Name, a.APIKey, a.APIURL)

	// Build cogito options
	cogitoOpts := modelConfig.BuildCogitoOptions()
	cogitoOpts = append(
		cogitoOpts,
		cogito.WithContext(context.Background()),
		cogito.WithMCPs(sessions...),
		cogito.WithStatusCallback(func(status string) {
			xlog.Debug("Agent status", "status", status)
		}),
		cogito.WithReasoningCallback(func(reasoning string) {
			fmt.Fprintf(os.Stderr, "[reasoning] %s\n", reasoning)
		}),
		cogito.WithToolCallBack(func(t *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
			fmt.Fprintf(os.Stderr, "[tool_call] %s\n", t.Name)
			return cogito.ToolCallDecision{Approved: true}
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			fmt.Fprintf(os.Stderr, "[tool_result] %s: %s\n", t.Name, truncate(t.Result, 200))
		}),
	)

	startTime := time.Now()
	xlog.Info("Executing agent task", "model", modelName)

	f, err := cogito.ExecuteTools(defaultLLM, fragment, cogitoOpts...)
	if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
		return fmt.Errorf("agent execution failed: %w", err)
	}

	elapsed := time.Since(startTime)
	result := f.LastMessage().Content

	fmt.Println(result)
	xlog.Info("Agent task completed", "duration", elapsed.Round(time.Millisecond))

	return nil
}

// buildPromptFromTemplate applies Go template parameters to a prompt string.
func buildPromptFromTemplate(templateStr string, params map[string]string) (string, error) {
	tmpl, err := template.New("prompt").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("invalid prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute prompt template: %w", err)
	}

	return buf.String(), nil
}

// parseParams converts a slice of "key=value" strings into a map.
func parseParams(pairs []string) map[string]string {
	params := make(map[string]string)
	for _, pair := range pairs {
		for i := 0; i < len(pair); i++ {
			if pair[i] == '=' {
				params[pair[:i]] = pair[i+1:]
				break
			}
		}
	}
	return params
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
