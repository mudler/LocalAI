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

// Reference inputs are bounded so one request cannot stage an unbounded number
// of files. A reference frame is a single image, so its limit is lower than the
// one for audio or keyframes.
const (
	maxVideoRefsPerKind     = 16
	maxVideoRefFrames       = 512
	maxVideoRefFrameBytes   = 32 << 20
	videoRefFrameNameFormat = "%06d"
)

// Media CDNs commonly redirect; WithExternalOnly validates every hop and the
// dialled address, and the shared redirect policy bounds the chain and strips
// credentials on cross-origin hops.
func newVideoDownloadClient() *http.Client {
	return httpclient.NewWithTimeout(30*time.Second, httpclient.WithFollowRedirects(), httpclient.WithExternalOnly())
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

// stagedVideoRefs holds the staged reference inputs of one request.
type stagedVideoRefs struct {
	images []string
	videos []backend.VideoReferenceOptions
	audios []string
	// paths are the files and directories to remove once the request is done.
	paths []string
}

func (s *stagedVideoRefs) cleanup() {
	for _, p := range s.paths {
		_ = os.RemoveAll(p)
	}
}

// stageVideoReferences stages ref_images, ref_videos and ref_audios with the
// same path as start_image/end_image/audio (stageVideoMedia). A reference video
// becomes a private directory of frames renamed to a zero-padded index, so the
// backend can read them in order. On error everything staged so far is removed.
func stageVideoReferences(ctx context.Context, directory string, input *schema.VideoRequest) (refs *stagedVideoRefs, err error) {
	refs = &stagedVideoRefs{}
	defer func() {
		if err != nil {
			refs.cleanup()
			refs = nil
		}
	}()

	if len(input.RefImages) > maxVideoRefsPerKind || len(input.RefVideos) > maxVideoRefsPerKind || len(input.RefAudios) > maxVideoRefsPerKind {
		return refs, echo.NewHTTPError(http.StatusBadRequest,
			fmt.Sprintf("too many reference inputs: at most %d of each kind", maxVideoRefsPerKind))
	}
	invalid := func(name string, cause error) error {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid %s: %v", name, cause))
	}

	for i, ref := range input.RefImages {
		if ref == "" {
			return refs, invalid(fmt.Sprintf("ref_images[%d]", i), fmt.Errorf("empty reference"))
		}
		path, serr := stageVideoMedia(ctx, directory, ref)
		if serr != nil {
			return refs, invalid(fmt.Sprintf("ref_images[%d]", i), serr)
		}
		refs.paths = append(refs.paths, path)
		refs.images = append(refs.images, path)
	}
	for i, ref := range input.RefAudios {
		if ref == "" {
			return refs, invalid(fmt.Sprintf("ref_audios[%d]", i), fmt.Errorf("empty reference"))
		}
		path, serr := stageVideoMedia(ctx, directory, ref)
		if serr != nil {
			return refs, invalid(fmt.Sprintf("ref_audios[%d]", i), serr)
		}
		refs.paths = append(refs.paths, path)
		refs.audios = append(refs.audios, path)
	}
	for i, video := range input.RefVideos {
		name := fmt.Sprintf("ref_videos[%d]", i)
		if len(video.Frames) == 0 {
			return refs, invalid(name, fmt.Errorf("frames is empty"))
		}
		if len(video.Frames) > maxVideoRefFrames {
			return refs, invalid(name, fmt.Errorf("at most %d frames are accepted", maxVideoRefFrames))
		}
		if video.FPS < 0 {
			return refs, invalid(name, fmt.Errorf("fps must not be negative"))
		}
		dir, merr := os.MkdirTemp(directory, "video-ref-frames-*")
		if merr != nil {
			return refs, fmt.Errorf("creating reference frame directory: %w", merr)
		}
		refs.paths = append(refs.paths, dir)
		for j, frame := range video.Frames {
			if frame == "" {
				return refs, invalid(fmt.Sprintf("%s.frames[%d]", name, j), fmt.Errorf("empty reference"))
			}
			path, serr := stageVideoMediaWithLimit(ctx, dir, frame, maxVideoRefFrameBytes)
			if serr != nil {
				return refs, invalid(fmt.Sprintf("%s.frames[%d]", name, j), serr)
			}
			if rerr := os.Rename(path, filepath.Join(dir, fmt.Sprintf(videoRefFrameNameFormat, j))); rerr != nil {
				_ = os.Remove(path)
				return refs, fmt.Errorf("naming reference frame: %w", rerr)
			}
		}
		entry := backend.VideoReferenceOptions{FramesDir: dir, FPS: video.FPS}
		if video.Audio != "" {
			path, serr := stageVideoMedia(ctx, directory, video.Audio)
			if serr != nil {
				return refs, invalid(name+".audio", serr)
			}
			refs.paths = append(refs.paths, path)
			entry.Audio = path
		}
		refs.videos = append(refs.videos, entry)
	}
	return refs, nil
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

		refs, err := stageVideoReferences(c.Request().Context(), appConfig.GeneratedContentDir, input)
		if err != nil {
			return err
		}
		defer refs.cleanup()

		xlog.Debug("Parameter Config", "config", config)

		switch config.Backend {
		case "stablediffusion":
			config.Backend = model.StableDiffusionGGMLBackend
		case "":
			config.Backend = model.StableDiffusionGGMLBackend
		}

		// Unset geometry is passed through as 0 so the BACKEND supplies its own
		// default canvas. Every video backend already does: stablediffusion-ggml
		// falls back to 512x512, diffusers to 1280x720, longcat-video to 832x480,
		// vllm-cpp to MiniMax-H3's trained 1344x768. Forcing 512x512 here made a
		// request that asked for nothing render at a size three of the four were
		// never trained at, and there is no size that is right for all of them.
		width := input.Width
		height := input.Height

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
				RefImages:      refs.images,
				RefVideos:      refs.videos,
				RefAudios:      refs.audios,
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
