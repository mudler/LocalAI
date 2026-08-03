package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
)

var moderationCategories = []string{
	"harassment",
	"harassment/threatening",
	"hate",
	"hate/threatening",
	"illicit",
	"illicit/violent",
	"self-harm",
	"self-harm/intent",
	"self-harm/instructions",
	"sexual",
	"sexual/minors",
	"violence",
	"violence/graphic",
}

type moderationGenerator func(context.Context, string, *config.ModelConfig) (string, backend.TokenUsage, error)

type generatedModeration struct {
	Categories     map[string]bool    `json:"categories"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

// ModerationEndpoint implements the text input subset of OpenAI's moderation
// API using any LocalAI completion model and constrained JSON generation.
// @Summary Classify text for potentially harmful content.
// @Tags moderation
// @Param request body schema.ModerationRequest true "query params"
// @Success 200 {object} schema.ModerationResponse "Response"
// @Router /v1/moderations [post]
func ModerationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return moderationEndpoint(func(ctx context.Context, input string, cfg *config.ModelConfig) (string, backend.TokenUsage, error) {
		prompt := moderationPrompt(input)
		var messages schema.Messages
		if cfg.TemplateConfig.UseTokenizerTemplate {
			messages = schema.Messages{{Role: "user", Content: prompt}}
			prompt = ""
		} else if evaluator != nil {
			if rendered, err := evaluator.EvaluateTemplateForPrompt(templates.CompletionPromptTemplate, *cfg, templates.PromptTemplateData{Input: prompt, SystemPrompt: cfg.SystemPrompt}); err == nil {
				prompt = rendered
			}
		}

		predict, err := backend.ModelInferenceFunc(ctx, prompt, messages, nil, nil, nil, ml, cfg, cl, appConfig, nil, "", "", nil, nil, nil, nil)
		if err != nil {
			return "", backend.TokenUsage{}, err
		}
		response, err := predict()
		return response.Response, response.Usage, err
	})
}

func moderationEndpoint(generate moderationGenerator) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.ModerationRequest)
		if !ok || input == nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid moderation request")
		}
		if len(input.Input) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "input must contain at least one text string")
		}
		if generate == nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "moderation generator is unavailable")
		}

		modelConfig, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || modelConfig == nil {
			return echo.NewHTTPError(http.StatusBadRequest, "moderation model configuration is unavailable")
		}

		grammar, err := moderationGrammar()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to build moderation grammar").SetInternal(err)
		}
		cfg := *modelConfig
		cfg.Grammar = grammar
		maxTokens := 512
		cfg.Maxtokens = &maxTokens

		results := make([]schema.ModerationResult, 0, len(input.Input))
		promptTokens, completionTokens := 0, 0
		for _, text := range input.Input {
			if strings.TrimSpace(text) == "" {
				return echo.NewHTTPError(http.StatusBadRequest, "input strings must not be empty")
			}
			raw, usage, err := generate(c.Request().Context(), text, &cfg)
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "moderation inference failed").SetInternal(err)
			}
			result, err := parseModerationResult(raw)
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "invalid moderation result: "+err.Error()).SetInternal(err)
			}
			results = append(results, result)
			promptTokens += usage.Prompt
			completionTokens += usage.Completion
		}

		middleware.StampUsage(c, input.Model, promptTokens, completionTokens)
		return c.JSON(http.StatusOK, schema.ModerationResponse{
			ID:      "modr-" + uuid.NewString(),
			Model:   input.Model,
			Results: results,
		})
	}
}

func moderationPrompt(input string) string {
	encoded, _ := json.Marshal(input)
	return `Classify the supplied text for safety moderation. Return only the JSON object required by the grammar.
For each category, set the boolean to true only when the text matches that category. Give each category a confidence score from 0 to 1.
The categories are harassment, harassment/threatening, hate, hate/threatening, illicit, illicit/violent, self-harm, self-harm/intent, self-harm/instructions, sexual, sexual/minors, violence, and violence/graphic.
Text to classify: ` + string(encoded)
}

func moderationGrammar() (string, error) {
	boolProperties := map[string]any{}
	scoreProperties := map[string]any{}
	for _, category := range moderationCategories {
		boolProperties[category] = map[string]any{"type": "boolean"}
		scoreProperties[category] = map[string]any{"type": "number"}
	}
	structure := functions.JSONFunctionStructure{AnyOf: []functions.Item{{
		Type: "object",
		Properties: map[string]any{
			"categories": map[string]any{
				"type":                 "object",
				"properties":           boolProperties,
				"required":             moderationCategories,
				"additionalProperties": false,
			},
			"category_scores": map[string]any{
				"type":                 "object",
				"properties":           scoreProperties,
				"required":             moderationCategories,
				"additionalProperties": false,
			},
		},
	}}}
	return structure.Grammar()
}

func parseModerationResult(raw string) (schema.ModerationResult, error) {
	var generated generatedModeration
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &generated); err != nil {
		return schema.ModerationResult{}, err
	}

	result := schema.ModerationResult{
		Categories:                make(map[string]bool, len(moderationCategories)),
		CategoryScores:            make(map[string]float64, len(moderationCategories)),
		CategoryAppliedInputTypes: make(map[string][]string, len(moderationCategories)),
	}
	for _, category := range moderationCategories {
		flagged, exists := generated.Categories[category]
		if !exists {
			return schema.ModerationResult{}, fmt.Errorf("missing category %q", category)
		}
		score, exists := generated.CategoryScores[category]
		if !exists || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return schema.ModerationResult{}, fmt.Errorf("category %q has an invalid score", category)
		}
		result.Categories[category] = flagged
		result.CategoryScores[category] = score
		result.CategoryAppliedInputTypes[category] = []string{"text"}
		result.Flagged = result.Flagged || flagged
	}
	return result, nil
}
