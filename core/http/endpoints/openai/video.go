package openai

import (
	"encoding/json"
	"fmt"
	"io"
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

		// Try to get raw body - the middleware may have already consumed it
		// Try GetBody() first (for requests that support it)
		var raw map[string]interface{}
		if c.Request().GetBody != nil {
			if body, err := c.Request().GetBody(); err == nil {
				if bodyData, err := io.ReadAll(body); err == nil && len(bodyData) > 0 {
					_ = json.Unmarshal(bodyData, &raw)
				}
			}
		}

		// If we didn't get raw data, try to extract from request body directly
		// (may be nil if already consumed)
		if len(raw) == 0 && c.Request().Body != nil {
			if bodyData, err := io.ReadAll(c.Request().Body); err == nil && len(bodyData) > 0 {
				_ = json.Unmarshal(bodyData, &raw)
				// Restore body for potential downstream use
				c.Request().Body = io.NopCloser(strings.NewReader(string(bodyData)))
			}
		}

		fmt.Printf("MapOpenAIToVideo: Raw map has %d keys: %v\n", len(raw), raw)

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

	// FPS parsing
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
	vr.FPS = fps

	// num_frames parsing (direct or calculated from seconds)
	if raw != nil {
		if rawNumFrames, ok := raw["num_frames"]; ok {
			switch nf := rawNumFrames.(type) {
			case float64:
				vr.NumFrames = int32(nf)
			case string:
				if nfi, err := strconv.Atoi(nf); err == nil {
					vr.NumFrames = int32(nfi)
				}
			}
		}
	}

	// seconds -> num frames (if num_frames not already set)
	secondsStr := ""
	if raw != nil && vr.NumFrames == 0 {
		if v, ok := raw["seconds"].(string); ok {
			secondsStr = v
		} else if v, ok := raw["seconds"].(float64); ok {
			secondsStr = fmt.Sprintf("%v", int(v))
		}
		if secondsStr != "" {
			if secF, err := strconv.Atoi(secondsStr); err == nil {
				vr.NumFrames = int32(secF) * fps
			}
		}
	}

	// negative_prompt
	if raw != nil {
		if v, ok := raw["negative_prompt"].(string); ok {
			vr.NegativePrompt = v
		}
	}

	// cfg_scale
	if raw != nil {
		if rawCFGScale, ok := raw["cfg_scale"]; ok {
			switch cs := rawCFGScale.(type) {
			case float64:
				vr.CFGScale = float32(cs)
			case string:
				if csf, err := strconv.ParseFloat(cs, 32); err == nil {
					vr.CFGScale = float32(csf)
				}
			}
		}
	}

	// seed
	if raw != nil {
		if rawSeed, ok := raw["seed"]; ok {
			switch s := rawSeed.(type) {
			case float64:
				vr.Seed = int32(s)
			case string:
				if si, err := strconv.Atoi(s); err == nil {
					vr.Seed = int32(si)
				}
			}
		}
	}

	// start_image
	if raw != nil {
		if v, ok := raw["start_image"].(string); ok {
			vr.StartImage = v
		}
	}

	// end_image
	if raw != nil {
		if v, ok := raw["end_image"].(string); ok {
			vr.EndImage = v
		}
	}

	// input_reference (alias for start_image)
	if raw != nil && vr.StartImage == "" {
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
	} else if raw != nil {
		// Also check raw for step
		if rawStep, ok := raw["step"]; ok {
			switch st := rawStep.(type) {
			case float64:
				vr.Step = int32(st)
			case string:
				if sti, err := strconv.Atoi(st); err == nil {
					vr.Step = int32(sti)
				}
			}
		}
	}

	// Debug: Log the parsed values
	fmt.Printf("MapOpenAIToVideo: Parsed values - num_frames: %d, fps: %d, cfg_scale: %f, step: %d, seed: %d, negative_prompt: %s\n",
		vr.NumFrames, vr.FPS, vr.CFGScale, vr.Step, vr.Seed, vr.NegativePrompt)

	return vr
}
