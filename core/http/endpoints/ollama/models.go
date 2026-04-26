package ollama

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
)

const ollamaCompatVersion = "0.9.0"

// ListModelsEndpoint handles Ollama-compatible GET /api/tags
func ListModelsEndpoint(bcl *config.ModelConfigLoader, ml *model.ModelLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelNames, err := galleryop.ListModels(bcl, ml, nil, galleryop.SKIP_IF_CONFIGURED)
		if err != nil {
			return ollamaError(c, 500, fmt.Sprintf("failed to list models: %v", err))
		}

		var models []schema.OllamaModelEntry
		for _, name := range modelNames {
			ollamaName := name
			if !strings.Contains(ollamaName, ":") {
				ollamaName += ":latest"
			}

			digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(name)))

			entry := schema.OllamaModelEntry{
				Name:       ollamaName,
				Model:      ollamaName,
				ModifiedAt: time.Now().UTC(),
				Size:       0,
				Digest:     digest,
				Details:    modelDetailsFromConfig(bcl, name),
			}
			models = append(models, entry)
		}

		return c.JSON(200, schema.OllamaListResponse{Models: models})
	}
}

// ShowModelEndpoint handles Ollama-compatible POST /api/show
func ShowModelEndpoint(bcl *config.ModelConfigLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.OllamaShowRequest
		if err := c.Bind(&req); err != nil {
			return ollamaError(c, 400, "invalid request body")
		}

		name := req.Name
		if name == "" {
			name = req.Model
		}
		if name == "" {
			return ollamaError(c, 400, "name is required")
		}

		// Strip tag suffix for config lookup
		configName := strings.Split(name, ":")[0]

		cfg, exists := bcl.GetModelConfig(configName)
		if !exists {
			return ollamaError(c, 404, fmt.Sprintf("model '%s' not found", name))
		}

		resp := schema.OllamaShowResponse{
			Modelfile:  fmt.Sprintf("FROM %s", cfg.Model),
			Parameters: "",
			Template:   cfg.TemplateConfig.Chat,
			Details:    modelDetailsFromModelConfig(&cfg),
		}

		return c.JSON(200, resp)
	}
}

// ListRunningEndpoint handles Ollama-compatible GET /api/ps
func ListRunningEndpoint(bcl *config.ModelConfigLoader, ml *model.ModelLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		loadedModels := ml.ListLoadedModels()

		var models []schema.OllamaPsEntry
		for _, m := range loadedModels {
			name := m.ID
			ollamaName := name
			if !strings.Contains(ollamaName, ":") {
				ollamaName += ":latest"
			}

			entry := schema.OllamaPsEntry{
				Name:      ollamaName,
				Model:     ollamaName,
				Size:      0,
				Digest:    fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(name))),
				Details:   modelDetailsFromConfig(bcl, name),
				ExpiresAt: time.Now().Add(24 * time.Hour).UTC(),
				SizeVRAM:  0,
			}
			models = append(models, entry)
		}

		return c.JSON(200, schema.OllamaPsResponse{Models: models})
	}
}

// VersionEndpoint handles Ollama-compatible GET /api/version
func VersionEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(200, schema.OllamaVersionResponse{Version: ollamaCompatVersion})
	}
}

// HeartbeatEndpoint handles the Ollama root health check
func HeartbeatEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.String(200, "Ollama is running")
	}
}

func modelDetailsFromConfig(bcl *config.ModelConfigLoader, name string) schema.OllamaModelDetails {
	configName := strings.Split(name, ":")[0]
	cfg, exists := bcl.GetModelConfig(configName)
	if !exists {
		return schema.OllamaModelDetails{}
	}
	return modelDetailsFromModelConfig(&cfg)
}

func modelDetailsFromModelConfig(cfg *config.ModelConfig) schema.OllamaModelDetails {
	return schema.OllamaModelDetails{
		Format: "gguf",
		Family: cfg.Backend,
	}
}
