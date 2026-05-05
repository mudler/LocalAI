package openai

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"
)

// DiarizationEndpoint runs offline speaker diarization on an uploaded
// audio file and returns "who spoke when". Backends with a pure
// diarization pipeline (sherpa-onnx + pyannote) emit only segmentation;
// backends that produce diarization as a by-product of ASR (vibevoice.cpp)
// can additionally fill in the per-segment transcript when the caller
// passes `include_text=true`.
//
// Response formats follow transcription's: `json` (default, segments only),
// `verbose_json` (adds speaker summary and per-segment text), and `rttm`
// (NIST RTTM, the standard interchange format used by pyannote/dscore).
//
// @Summary Identify speakers in audio (who spoke when).
// @Tags audio
// @accept multipart/form-data
// @Param model formData string true "model"
// @Param file formData file true "audio file"
// @Param num_speakers formData int false "exact speaker count (>0 forces; 0 = auto)"
// @Param min_speakers formData int false "lower bound when auto-detecting"
// @Param max_speakers formData int false "upper bound when auto-detecting"
// @Param clustering_threshold formData number false "clustering distance threshold when num_speakers is unknown"
// @Param min_duration_on formData number false "discard segments shorter than this (seconds)"
// @Param min_duration_off formData number false "merge gaps shorter than this (seconds)"
// @Param language formData string false "audio language hint (only meaningful for backends that bundle ASR)"
// @Param include_text formData boolean false "include per-segment transcript when the backend supports it"
// @Param response_format formData string false "json (default), verbose_json, or rttm"
// @Success 200 {object} schema.DiarizationResult
// @Router /v1/audio/diarization [post]
func DiarizationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		modelConfig, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || modelConfig == nil {
			return echo.ErrBadRequest
		}

		req := backend.DiarizationRequest{
			Language:    input.Language,
			IncludeText: parseFormBool(c, "include_text", false),
		}
		req.NumSpeakers = int32(parseFormInt(c, "num_speakers", 0))
		req.MinSpeakers = int32(parseFormInt(c, "min_speakers", 0))
		req.MaxSpeakers = int32(parseFormInt(c, "max_speakers", 0))
		req.ClusteringThreshold = float32(parseFormFloat(c, "clustering_threshold", 0))
		req.MinDurationOn = float32(parseFormFloat(c, "min_duration_on", 0))
		req.MinDurationOff = float32(parseFormFloat(c, "min_duration_off", 0))

		responseFormat := schema.DiarizationResponseFormatType(strings.ToLower(c.FormValue("response_format")))
		if responseFormat == "" {
			responseFormat = schema.DiarizationResponseFormatJson
		}

		file, err := c.FormFile("file")
		if err != nil {
			return err
		}
		f, err := file.Open()
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		dir, err := os.MkdirTemp("", "diarize")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()

		dst := filepath.Join(dir, path.Base(file.Filename))
		dstFile, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dstFile, f); err != nil {
			xlog.Debug("Audio file copying error", "filename", file.Filename, "dst", dst, "error", err)
			_ = dstFile.Close()
			return err
		}
		_ = dstFile.Close()
		req.Audio = dst

		result, err := backend.ModelDiarization(req, ml, *modelConfig, appConfig)
		if err != nil {
			return err
		}

		switch responseFormat {
		case schema.DiarizationResponseFormatRTTM:
			c.Response().Header().Set(echo.HeaderContentType, "text/plain; charset=utf-8")
			return c.String(http.StatusOK, renderRTTM(result, file.Filename))
		case schema.DiarizationResponseFormatJson:
			// Default JSON: drop the heavy per-speaker summary and any
			// optional per-segment text so simple consumers see a tight
			// payload. verbose_json keeps everything.
			result.Speakers = nil
			for i := range result.Segments {
				result.Segments[i].Text = ""
			}
			return c.JSON(http.StatusOK, result)
		case schema.DiarizationResponseFormatJsonVerbose:
			return c.JSON(http.StatusOK, result)
		default:
			return errors.New("invalid response_format (expected: json, verbose_json, rttm)")
		}
	}
}

// renderRTTM emits NIST RTTM rows. Each row:
// SPEAKER <file> 1 <start> <duration> <NA> <NA> <speaker> <NA> <NA>
// Field separators are spaces; one row per segment.
func renderRTTM(r *schema.DiarizationResult, sourceFile string) string {
	id := strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
	// filepath.Base("") returns "." — treat both as a missing source name and
	// fall back to a stable placeholder so the RTTM row stays parseable.
	if id == "" || id == "." {
		id = "audio"
	}
	var sb strings.Builder
	for _, seg := range r.Segments {
		dur := seg.End - seg.Start
		if dur < 0 {
			dur = 0
		}
		fmt.Fprintf(&sb, "SPEAKER %s 1 %.3f %.3f <NA> <NA> %s <NA> <NA>\n",
			id, seg.Start, dur, seg.Speaker)
	}
	return sb.String()
}

func parseFormInt(c echo.Context, key string, def int) int {
	if v := c.FormValue(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func parseFormFloat(c echo.Context, key string, def float64) float64 {
	if v := c.FormValue(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func parseFormBool(c echo.Context, key string, def bool) bool {
	if v := c.FormValue(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
