// SPDX-License-Identifier: MIT
package localai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

func validateAnimationRequest(input *schema.Model3DAnimationRequest, cfg *config.ModelConfig) error {
	if input.ResponseFormat != "" && input.ResponseFormat != "url" && input.ResponseFormat != "b64_json" {
		return fmt.Errorf("response_format must be url or b64_json")
	}
	for _, operation := range cfg.ThreeDOperations() {
		if operation.Endpoint != "/3d/animate" || !animationInputsMatch(input.Inputs, operation.Inputs) {
			continue
		}
		parameters := make(map[string]schema.ThreeDParameter, len(operation.Parameters))
		for _, parameter := range operation.Parameters {
			parameters[parameter.Name] = parameter
		}
		for name, value := range input.Params {
			parameter, ok := parameters[name]
			if !ok {
				return fmt.Errorf("unsupported animation parameter %q", name)
			}
			if err := parameter.Validate(value); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("the selected model does not support these animation inputs; consult its three_d_operations capabilities")
}

func animationInputsMatch(inputs map[string]schema.AnimationInput, requirements []schema.ThreeDInput) bool {
	known := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		known[requirement.Name] = true
		input, present := inputs[requirement.Name]
		if !present && !requirement.Required {
			continue
		}
		if !present || input.Type != requirement.Type || strings.TrimSpace(input.Data) == "" {
			return false
		}
		if input.Type == "text" && (!utf8.ValidString(input.Data) || strings.ContainsRune(input.Data, 0) ||
			(requirement.MaxBytes > 0 && len(input.Data) > requirement.MaxBytes)) {
			return false
		}
	}
	for name := range inputs {
		if !known[name] {
			return false
		}
	}
	return true
}

// Model3DAnimationEndpoint creates an animation using the selected model's inputs.
// @Summary Creates a 3D animation (binary glTF / GLB).
// @Tags 3d
// @Param request body schema.Model3DAnimationRequest true "Named conditioning inputs and model-specific parameters"
// @Success 200 {object} schema.OpenAIResponse
// @Router /3d/animate [post]
func Model3DAnimationEndpoint(ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.Model3DAnimationRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}
		if err := validateAnimationRequest(input, cfg); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		request := &pb.Animate3DRequest{Inputs: make(map[string]*pb.AnimationInput), Params: input.Params}
		var staged []string
		defer func() {
			for _, path := range staged {
				_ = os.Remove(path)
			}
		}()
		for name, value := range input.Inputs {
			data := value.Data
			if value.Type != "text" {
				path, err := stageVideoMediaWithLimit(c.Request().Context(), appConfig.GeneratedContentDir, data, 32<<20)
				if err != nil {
					return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid input %q: %v", name, err))
				}
				staged = append(staged, path)
				data = path
			}
			request.Inputs[name] = &pb.AnimationInput{Type: value.Type, Data: data}
		}
		directory := filepath.Join(appConfig.GeneratedContentDir, "3d")
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return err
		}
		file, err := os.CreateTemp(directory, "animation-*.glb")
		if err != nil {
			return err
		}
		preserve := false
		defer func() {
			if !preserve {
				_ = os.Remove(file.Name())
			}
		}()
		if err := file.Close(); err != nil {
			return err
		}
		request.Dst = file.Name()
		responseMetadata, err := backend.Model3DAnimation(c.Request().Context(), request, ml, *cfg, appConfig)
		if err != nil {
			return mapBackendError(err)
		}
		item := schema.Item{}
		if input.ResponseFormat == "b64_json" {
			data, err := os.ReadFile(file.Name())
			if err != nil {
				return err
			}
			item.B64JSON = base64.StdEncoding.EncodeToString(data)
		} else {
			item.URL, err = url.JoinPath(middleware.BaseURL(c), "generated-3d", filepath.Base(file.Name()))
			if err != nil {
				return err
			}
			preserve = true
		}
		response := schema.OpenAIResponse{ID: uuid.NewString(), Model: input.Model, Created: int(time.Now().Unix()), Data: []schema.Item{item}}
		metadataErr := middleware.StampResponseMetadata(c, input.Model, responseMetadata)
		if metadataErr != nil {
			xlog.Warn("ignoring invalid animation response metadata", "model", input.Model, "error", metadataErr)
		} else {
			response.Metadata = json.RawMessage(responseMetadata)
		}
		return c.JSON(http.StatusOK, response)
	}
}
