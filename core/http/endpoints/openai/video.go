package openai

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
)

func VideoEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input == nil {
			return echo.ErrBadRequest
		}
		var raw map[string]interface{}
		body := make([]byte, 0)
		if c.Request().Body != nil {
			c.Request().Body.Read(body)
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &raw)
		}
		// Build VideoRequest using shared mapper
		vr := MapOpenAIToVideo(input, raw)
		// Place VideoRequest into context so localai.VideoEndpoint can consume it
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, vr)
		// Delegate to existing localai handler
		return localai.VideoEndpoint(cl, ml, appConfig)(c)
	}
}

// VideoEndpoint godoc
// @Summary Generate a video from an OpenAI-compatible request
// @Description Accepts an OpenAI-style request and delegates to the LocalAI video generator
// @Tags openai
// @Accept json
// @Produce json
// @Param request body schema.OpenAIRequest true "OpenAI-style request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /v1/videos [post]

func MapOpenAIToVideo(input *schema.OpenAIRequest, raw map[string]interface{}) *schema.VideoRequest {
	vr := &schema.VideoRequest{}
	if input == nil {
		return vr
	}

	if input.Model != "" {
		vr.Model = input.Model
	}

	// Prompt mapping
	switch p := input.Prompt.(type) {
	case string:
		vr.Prompt = p
	case []interface{}:
		if len(p) > 0 {
			if s, ok := p[0].(string); ok {
				vr.Prompt = s
			}
		}
	}

	// Size
	size := input.Size
	if size == "" && raw != nil {
		if v, ok := raw["size"].(string); ok {
			size = v
		}
	}
	if size != "" {
		parts := strings.SplitN(size, "x", 2)
		if len(parts) == 2 {
			if wi, err := strconv.Atoi(parts[0]); err == nil {
				vr.Width = int32(wi)
			}
			if hi, err := strconv.Atoi(parts[1]); err == nil {
				vr.Height = int32(hi)
			}
		}
	}

	// seconds -> num frames
	secondsStr := ""
	if raw != nil {
		if v, ok := raw["seconds"].(string); ok {
			secondsStr = v
		} else if v, ok := raw["seconds"].(float64); ok {
			secondsStr = fmt.Sprintf("%v", int(v))
		}
	}
	fps := int32(30)
	if raw != nil {
		if rawFPS, ok := raw["fps"]; ok {
			switch rf := rawFPS.(type) {
			case float64:
				fps = int32(rf)
			case string:
				if fi, err := strconv.Atoi(rf); err == nil {
					fps = int32(fi)
				}
			}
		}
	}
	if secondsStr != "" {
		if secF, err := strconv.Atoi(secondsStr); err == nil {
			vr.FPS = fps
			vr.NumFrames = int32(secF) * fps
		}
	}

	// input_reference
	if raw != nil {
		if v, ok := raw["input_reference"].(string); ok {
			vr.StartImage = v
		}
	}

	// response format
	if input.ResponseFormat != nil {
		if rf, ok := input.ResponseFormat.(string); ok {
			vr.ResponseFormat = rf
		}
	}

	if input.Step != 0 {
		vr.Step = int32(input.Step)
	}

	return vr
}
