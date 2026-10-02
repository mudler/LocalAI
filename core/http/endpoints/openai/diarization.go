package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	model "github.com/mudler/LocalAI/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

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
// @Description JSON accepts model, file (raw base64 audio), include_text, include_speaker_profiles and response_format. Profiles require voice-recognition permission and json or verbose_json; unsupported backends return 501.
// @Tags audio
// @accept multipart/form-data,json
// @Param model formData string true "model"
// @Param file formData file true "audio file"
// @Param num_speakers formData int false "exact speaker count (>0 forces; 0 = auto)"
// @Param min_speakers formData int false "lower bound when auto-detecting"
// @Param max_speakers formData int false "upper bound when auto-detecting"
// @Param clustering_threshold formData number false "clustering distance threshold when num_speakers is unknown"
// @Param min_duration_on formData number false "discard segments shorter than this (seconds)"
// @Param min_duration_off formData number false "merge gaps shorter than this (seconds)"
// @Param language formData string false "audio language hint (only meaningful for backends that bundle ASR)"
// @Param include_speaker_profiles formData boolean false "export portable biometric profiles (voice-recognition permission; JSON formats only)"
// @Param include_text formData boolean false "include per-segment transcript when the backend supports it"
// @Param response_format formData string false "json (default), verbose_json, or rttm"
// @Success 200 {object} schema.DiarizationResult
// @Router /v1/audio/diarization [post]
func DiarizationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, registry voicerecognition.Registry, authDB ...*gorm.DB) echo.HandlerFunc {
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
			Language:               input.Language,
			IncludeText:            parseFormBool(c, "include_text", input.IncludeText),
			IncludeSpeakerProfiles: parseFormBool(c, "include_speaker_profiles", input.IncludeSpeakerProfiles),
		}
		if req.IncludeSpeakerProfiles {
			var db *gorm.DB
			if len(authDB) > 0 {
				db = authDB[0]
			}
			allowed := false
			err := auth.RequireFeature(db, auth.FeatureVoiceRecognition)(func(c echo.Context) error { allowed = true; return nil })(c)
			if err != nil || !allowed {
				return err
			}
		}
		req.NumSpeakers = int32(parseFormInt(c, "num_speakers", 0))
		req.MinSpeakers = int32(parseFormInt(c, "min_speakers", 0))
		req.MaxSpeakers = int32(parseFormInt(c, "max_speakers", 0))
		req.ClusteringThreshold = float32(parseFormFloat(c, "clustering_threshold", 0))
		req.MinDurationOn = float32(parseFormFloat(c, "min_duration_on", 0))
		req.MinDurationOff = float32(parseFormFloat(c, "min_duration_off", 0))
		attachKnownVoices(c.Request().Context(), &req, modelConfig.Options, registry)

		responseFormat := schema.DiarizationResponseFormatType(strings.ToLower(c.FormValue("response_format")))
		if responseFormat == "" {
			if input.ResponseFormat != nil {
				f, ok := input.ResponseFormat.(string)
				if !ok {
					return echo.NewHTTPError(http.StatusBadRequest, "response_format must be a string")
				}
				responseFormat = schema.DiarizationResponseFormatType(strings.ToLower(f))
			}
		}
		if responseFormat == "" {
			responseFormat = schema.DiarizationResponseFormatJson
		}
		switch responseFormat {
		case schema.DiarizationResponseFormatJson, schema.DiarizationResponseFormatJsonVerbose, schema.DiarizationResponseFormatRTTM:
		default:
			// Checked before the backend runs, for the same reason as in
			// TranscriptEndpoint.
			return echo.NewHTTPError(http.StatusBadRequest, "invalid response_format (expected: json, verbose_json, rttm)")
		}

		if req.IncludeSpeakerProfiles && responseFormat == schema.DiarizationResponseFormatRTTM {
			return echo.NewHTTPError(http.StatusBadRequest, "speaker_profiles requires json or verbose_json")
		}
		var sourceName = "audio.wav"
		var reader io.ReadCloser
		if strings.HasPrefix(c.Request().Header.Get(echo.HeaderContentType), echo.MIMEApplicationJSON) {
			raw, err := base64.StdEncoding.DecodeString(input.File)
			if err != nil || len(raw) == 0 {
				return echo.NewHTTPError(http.StatusBadRequest, "file must be base64 audio")
			}
			reader = io.NopCloser(bytes.NewReader(raw))
		} else {
			file, err := uploadedFile(c, "file")
			if err != nil {
				return err
			}
			f, err := file.Open()
			if err != nil {
				return err
			}
			reader = f
			sourceName = path.Base(file.Filename)
		}
		defer func() { _ = reader.Close() }()

		dir, err := os.MkdirTemp("", "diarize")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()

		dst := filepath.Join(dir, sourceName)
		dstFile, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dstFile, reader); err != nil {
			xlog.Debug("Audio file copying error", "filename", sourceName, "dst", dst, "error", err)
			_ = dstFile.Close()
			return err
		}
		_ = dstFile.Close()
		req.Audio = dst

		result, err := backend.ModelDiarization(c.Request().Context(), req, ml, *modelConfig, appConfig)
		if err != nil {
			if status.Code(err) == codes.Unimplemented {
				return echo.NewHTTPError(http.StatusNotImplemented, status.Convert(err).Message())
			}
			return err
		}
		if !req.IncludeSpeakerProfiles {
			result.SpeakerProfiles = nil
		}

		switch responseFormat {
		case schema.DiarizationResponseFormatRTTM:
			c.Response().Header().Set(echo.HeaderContentType, "text/plain; charset=utf-8")
			return c.String(http.StatusOK, renderRTTM(result, sourceName))
		case schema.DiarizationResponseFormatJson:
			// Default JSON: drop the heavy per-speaker summary and any
			// unrequested per-segment text so simple consumers see a tight
			// payload. verbose_json keeps everything.
			result.Speakers = nil
			for i := range result.Segments {
				if !req.IncludeText {
					result.Segments[i].Text = ""
				}
			}
			return c.JSON(http.StatusOK, result)
		case schema.DiarizationResponseFormatJsonVerbose:
			return c.JSON(http.StatusOK, result)
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "invalid response_format (expected: json, verbose_json, rttm)")
		}
	}
}

// attachKnownVoices names the speakers from the voice registry when the model
// has a speaker model. Only voices made by that model's encoder are sent. A
// missing registry or speaker model, or a registry read error, leaves the
// request unnamed.
func attachKnownVoices(ctx context.Context, req *backend.DiarizationRequest, options []string, registry voicerecognition.Registry) {
	req.KnownVoices = selectKnownVoices(ctx, "diarization", options, registry)
}

// warned remembers the (feature, speaker model) pairs already warned about.
var warned sync.Map

// warnOnce reports true the first time it sees key, false afterwards.
func warnOnce(key string) bool {
	_, loaded := warned.LoadOrStore(key, struct{}{})
	return !loaded
}

// selectKnownVoices returns the registered voices a backend may use to name
// speakers, or nil when the model has no speaker_model, there is no registry,
// or the registry cannot be read. It never fails the caller: unnamed speakers
// are the fallback. feature only prefixes the log messages.
func selectKnownVoices(ctx context.Context, feature string, options []string, registry voicerecognition.Registry) []voicerecognition.KnownVoice {
	sm := voicerecognition.SpeakerModelFromOptions(options)
	if sm == "" || registry == nil {
		return nil
	}
	sel, err := voicerecognition.KnownVoicesFor(ctx, registry, sm)
	if err != nil {
		xlog.Warn(feature+": could not read the voice registry; speakers stay unnamed", "error", err)
		return nil
	}
	if len(sel.Voices) == 0 && sel.OtherEncoder > 0 {
		msg := feature + ": registered voices were made with a different encoder than this model's speaker_model; speakers stay unnamed"
		if warnOnce(feature + "|" + sm) {
			xlog.Warn(msg, "speaker_model", sm, "voices_from_other_encoder", sel.OtherEncoder)
		} else {
			xlog.Debug(msg, "speaker_model", sm, "voices_from_other_encoder", sel.OtherEncoder)
		}
	}
	return sel.Voices
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
