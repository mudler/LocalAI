package localai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/core/backend"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/httpclient"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

const maxVideoInputBytes = 128 << 20

func newVideoDownloadClient() *http.Client {
	client := httpclient.NewWithTimeout(30*time.Second, httpclient.WithFollowRedirects())
	checkRedirect := client.CheckRedirect
	// Media CDNs commonly redirect, so validate every hop rather than trusting
	// only the URL supplied by the caller. Keep the shared redirect policy too;
	// it bounds the chain and strips credentials on cross-origin hops.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := utils.ValidateExternalURL(req.URL.String()); err != nil {
			return fmt.Errorf("redirect URL validation failed: %w", err)
		}
		return checkRedirect(req, via)
	}
	return client
}

var videoDownloadClient = newVideoDownloadClient()

func openVideoMedia(ctx context.Context, ref string) (io.ReadCloser, int64, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		if err := utils.ValidateExternalURL(ref); err != nil {
			return nil, 0, fmt.Errorf("URL validation failed: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("creating download request: %w", err)
		}
		resp, err := videoDownloadClient.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("downloading media: %w", err)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			_ = resp.Body.Close()
			return nil, 0, fmt.Errorf("media URL returned HTTP %d", resp.StatusCode)
		}
		return resp.Body, resp.ContentLength, nil
	}

	encoded := ref
	if strings.HasPrefix(ref, "data:") {
		comma := strings.IndexByte(ref, ',')
		if comma < 0 || !strings.Contains(strings.ToLower(ref[:comma]), ";base64") {
			return nil, 0, fmt.Errorf("data URI must contain a base64 payload")
		}
		encoded = ref[comma+1:]
	}
	if encoded == "" {
		return nil, 0, fmt.Errorf("media payload is empty")
	}

	decodedSize := int64(base64.StdEncoding.DecodedLen(len(encoded)))
	return io.NopCloser(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))), decodedSize, nil
}

func stageVideoMedia(ctx context.Context, directory, ref string) (string, error) {
	return stageVideoMediaWithLimit(ctx, directory, ref, maxVideoInputBytes)
}

func stageVideoMediaWithLimit(ctx context.Context, directory, ref string, maxBytes int64) (string, error) {
	if ref == "" {
		return "", nil
	}

	source, declaredSize, err := openVideoMedia(ctx, ref)
	if err != nil {
		return "", err
	}
	defer func() { _ = source.Close() }()
	if declaredSize > maxBytes {
		return "", fmt.Errorf("media exceeds the %d-byte limit", maxBytes)
	}

	output, err := os.CreateTemp(directory, "video-input-*")
	if err != nil {
		return "", fmt.Errorf("creating staged media file: %w", err)
	}
	outputPath := output.Name()
	keep := false
	defer func() {
		_ = output.Close()
		if !keep {
			_ = os.Remove(outputPath)
		}
	}()

	written, err := io.Copy(output, io.LimitReader(source, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("decoding media: %w", err)
	}
	if written > maxBytes {
		return "", fmt.Errorf("media exceeds the %d-byte limit", maxBytes)
	}
	if err := output.Close(); err != nil {
		return "", fmt.Errorf("closing staged media file: %w", err)
	}
	keep = true
	return outputPath, nil
}

//

/*
*

	curl http://localhost:8080/v1/images/generations \
	  -H "Content-Type: application/json" \
	  -d '{
	    "prompt": "A cute baby sea otter",
	    "n": 1,
	    "size": "512x512"
	  }'

*
*/
// VideoEndpoint
// @Summary Creates a video from a prompt and optional image or audio conditioning.
// @Tags video
// @Param request body schema.VideoRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /video [post]
func VideoEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VideoRequest)
		if !ok || input.Model == "" {
			xlog.Error("Video Endpoint - Invalid Input")
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			xlog.Error("Video Endpoint - Invalid Config")
			return echo.ErrBadRequest
		}

		stageInput := func(name, ref string) (string, error) {
			path, err := stageVideoMedia(c.Request().Context(), appConfig.GeneratedContentDir, ref)
			if err != nil {
				return "", echo.NewHTTPError(
					http.StatusBadRequest,
					fmt.Sprintf("invalid %s: %v", name, err),
				)
			}
			return path, nil
		}

		src, err := stageInput("start_image", input.StartImage)
		if err != nil {
			return err
		}
		if src != "" {
			defer func() { _ = os.Remove(src) }()
		}

		endSrc, err := stageInput("end_image", input.EndImage)
		if err != nil {
			return err
		}
		if endSrc != "" {
			defer func() { _ = os.Remove(endSrc) }()
		}

		audioSrc, err := stageInput("audio", input.Audio)
		if err != nil {
			return err
		}
		if audioSrc != "" {
			defer func() { _ = os.Remove(audioSrc) }()
		}

		xlog.Debug("Parameter Config", "config", config)

		switch config.Backend {
		case "stablediffusion":
			config.Backend = model.StableDiffusionGGMLBackend
		case "":
			config.Backend = model.StableDiffusionGGMLBackend
		}

		width := input.Width
		height := input.Height

		if width == 0 {
			width = 512
		}
		if height == 0 {
			height = 512
		}

		b64JSON := input.ResponseFormat == "b64_json"

		tempDir := ""
		if !b64JSON {
			tempDir = filepath.Join(appConfig.GeneratedContentDir, "videos")
			if err := os.MkdirAll(tempDir, 0o750); err != nil {
				return err
			}
		}
		// Create a temporary file
		outputFile, err := os.CreateTemp(tempDir, "b64")
		if err != nil {
			return err
		}
		if err := outputFile.Close(); err != nil {
			_ = os.Remove(outputFile.Name())
			return err
		}

		// TODO: use mime type to determine the extension
		output := outputFile.Name() + ".mp4"

		// Rename the temporary file
		err = os.Rename(outputFile.Name(), output)
		if err != nil {
			_ = os.Remove(outputFile.Name())
			return err
		}
		preserveOutput := false
		defer func() {
			if !preserveOutput {
				_ = os.Remove(output)
			}
		}()

		baseURL := middleware.BaseURL(c)

		xlog.Debug("VideoEndpoint: Calling VideoGeneration",
			"num_frames", input.NumFrames,
			"fps", input.FPS,
			"cfg_scale", input.CFGScale,
			"step", input.Step,
			"seed", input.Seed,
			"width", width,
			"height", height,
			"negative_prompt", input.NegativePrompt)

		fn, err := backend.VideoGeneration(
			backend.VideoGenerationOptions{
				Height:         height,
				Width:          width,
				Prompt:         input.Prompt,
				NegativePrompt: input.NegativePrompt,
				StartImage:     src,
				EndImage:       endSrc,
				Audio:          audioSrc,
				Destination:    output,
				NumFrames:      input.NumFrames,
				FPS:            input.FPS,
				Seed:           input.Seed,
				CFGScale:       input.CFGScale,
				Step:           input.Step,
				Params:         input.Params,
			},
			ml,
			*config,
			appConfig,
		)
		if err != nil {
			return mapBackendError(err)
		}
		if err := fn(); err != nil {
			return mapBackendError(err)
		}

		item := &schema.Item{}

		if b64JSON {
			data, err := os.ReadFile(output)
			if err != nil {
				return err
			}
			item.B64JSON = base64.StdEncoding.EncodeToString(data)
		} else {
			base := filepath.Base(output)
			item.URL, err = url.JoinPath(baseURL, "generated-videos", base)
			if err != nil {
				return err
			}
			preserveOutput = true
		}

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Data:    []schema.Item{*item},
		}

		jsonResult, _ := json.Marshal(resp)
		xlog.Debug("Response", "response", string(jsonResult))

		// Return the prediction in the response body
		return c.JSON(200, resp)
	}
}
