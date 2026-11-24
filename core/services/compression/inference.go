package compression

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
)

const summarizerInstruction = `Summarize the following conversation for an AI agent to continue coherently. Preserve names, numbers, decisions, URLs, error messages, tool names, and tool results. Drop pleasantries and repetition.`

type InferenceSummarizer struct {
	configs *config.ModelConfigLoader
	models  *model.ModelLoader
	app     *config.ApplicationConfig
}

func NewInferenceSummarizer(configs *config.ModelConfigLoader, models *model.ModelLoader, app *config.ApplicationConfig) *InferenceSummarizer {
	return &InferenceSummarizer{configs: configs, models: models, app: app}
}

func (s *InferenceSummarizer) Summarize(ctx context.Context, modelName string, messages []schema.Message, maxTokens int) (string, int, error) {
	cfg, err := s.configs.LoadModelConfigFileByNameDefaultOptions(modelName, s.app)
	if err != nil {
		return "", 0, fmt.Errorf("load compressor model %q: %w", modelName, err)
	}
	if cfg == nil {
		return "", 0, fmt.Errorf("load compressor model %q: configuration not found", modelName)
	}
	runtimeCfg := *cfg
	cfg = &runtimeCfg
	cfg.Maxtokens = &maxTokens
	temperature := 0.0
	cfg.Temperature = &temperature
	payload, err := json.Marshal(messages)
	if err != nil {
		return "", 0, fmt.Errorf("encode conversation: %w", err)
	}
	prompt := fmt.Sprintf("%s\n\nMaximum summary length: %d tokens.\n\nConversation:\n%s", summarizerInstruction, maxTokens, payload)
	fn, err := backend.ModelInference(ctx, prompt, nil, nil, nil, nil, s.models, cfg, s.configs, s.app, nil, "", "", nil, nil, nil, nil)
	if err != nil {
		return "", 0, err
	}
	response, err := fn()
	if err != nil {
		return "", 0, err
	}
	summary := strings.TrimSpace(functions.ContentFromChatDeltas(response.ChatDeltas))
	if summary == "" {
		summary = strings.TrimSpace(response.Response)
	}
	if summary == "" {
		return "", 0, fmt.Errorf("compressor model %q returned an empty summary", modelName)
	}
	return summary, response.Usage.Completion, nil
}
