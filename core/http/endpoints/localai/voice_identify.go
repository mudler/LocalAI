package localai

import (
	"cmp"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// defaultVoiceIdentifyThreshold is the cosine-distance cutoff applied
// when the client does not specify one. Tuned for ECAPA-TDNN on
// VoxCeleb (EER ~1.9%). Other recognizers (WeSpeaker, ERes2Net) may
// need overrides.
const defaultVoiceIdentifyThreshold = float32(0.25)

// VoiceIdentifyEndpoint runs 1:N identification against the registered store.
// @Summary Identify a speaker against the registered database (1:N recognition).
// @Tags voice-recognition
// @Param request body schema.VoiceIdentifyRequest true "query params"
// @Success 200 {object} schema.VoiceIdentifyResponse "Response"
// @Router /v1/voice/identify [post]
func VoiceIdentifyEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, registry voicerecognition.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VoiceIdentifyRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		audio, cleanup, err := decodeAudioInput(input.Audio)
		if err != nil {
			return err
		}
		defer cleanup()

		topK := cmp.Or(input.TopK, 5)
		threshold := cmp.Or(input.Threshold, defaultVoiceIdentifyThreshold)

		xlog.Debug("VoiceIdentify", "model", cfg.Name, "topK", topK, "threshold", threshold)
		embed, err := backend.VoiceEmbed(c.Request().Context(), audio, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		matches, err := registry.Identify(c.Request().Context(), embed.GetEmbedding(), topK)
		if err != nil {
			return err
		}

		// Portable vectors require exact loaded-weight identity. Legacy audio
		// registrations retain their filename-tag compatibility behavior.
		var trusted schema.SpeakerEncoder
		var trustedErr error
		for _, m := range matches {
			if strings.HasPrefix(m.Metadata.Model, "sha256:") {
				trusted, trustedErr = backend.ModelSpeakerEncoder(c.Request().Context(), ml, *cfg, appConfig)
				break
			}
		}
		filtered := matches[:0]
		for _, m := range matches {
			if strings.HasPrefix(m.Metadata.Model, "sha256:") {
				if trustedErr != nil || m.Metadata.Model != trusted.Identity || len(embed.GetEmbedding()) != trusted.Dimension {
					continue
				}
			} else if m.Metadata.EncoderFamily != "" && embed.GetEncoderFamily() != "" {
				// Both sides carry a fingerprint: the embedding space decides, not
				// the file name.
				if m.Metadata.EncoderFamily != embed.GetEncoderFamily() {
					continue
				}
			} else if m.Metadata.Model != "" && voicerecognition.EncoderTag(m.Metadata.Model) != voicerecognition.EncoderTag(embed.GetModel()) {
				continue
			}
			filtered = append(filtered, m)
		}
		matches = filtered
		response := schema.VoiceIdentifyResponse{
			Matches: make([]schema.VoiceIdentifyMatch, len(matches)),
		}
		for i, m := range matches {
			confidence := (1 - m.Distance/threshold) * 100
			if confidence < 0 {
				confidence = 0
			}
			if confidence > 100 {
				confidence = 100
			}
			response.Matches[i] = schema.VoiceIdentifyMatch{
				ID:         m.ID,
				Name:       m.Metadata.Name,
				Labels:     m.Metadata.Labels,
				Distance:   m.Distance,
				Confidence: confidence,
				Match:      m.Distance <= threshold,
			}
		}
		return c.JSON(http.StatusOK, response)
	}
}
