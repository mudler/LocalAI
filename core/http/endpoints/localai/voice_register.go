package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/model"
)

// VoiceRegisterEndpoint enrolls a speaker into the 1:N identification store.
// @Summary Register a speaker for 1:N identification.
// @Description Supply either audio or speaker_profiles plus an explicit numeric speaker_slot. The selected model must expose matching trusted encoder metadata for portable enrollment. Registrations are global and ephemeral, with a fresh ID for each request.
// @Tags voice-recognition
// @Param request body schema.VoiceRegisterRequest true "query params"
// @Success 200 {object} schema.VoiceRegisterResponse "Response"
// @Router /v1/voice/register [post]
func VoiceRegisterEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, registry voicerecognition.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VoiceRegisterRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}
		if input.Name == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "name is required")
		}

		var embedding []float32
		var encoder, family, weights string
		if input.SpeakerProfiles != nil {
			if input.Audio != "" || input.SpeakerSlot == nil {
				return echo.NewHTTPError(http.StatusBadRequest, "speaker_profiles requires speaker_slot and excludes audio")
			}
			trusted, err := backend.ModelSpeakerEncoder(c.Request().Context(), ml, *cfg, appConfig)
			if err != nil {
				return mapBackendError(err)
			}
			selected, err := input.SpeakerProfiles.Select(*input.SpeakerSlot, trusted)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			embedding, encoder, family = selected.Embedding, trusted.Identity, trusted.Family
		} else {
			if input.SpeakerSlot != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "speaker_slot requires speaker_profiles")
			}
			audio, cleanup, err := decodeAudioInput(input.Audio)
			if err != nil {
				return err
			}
			defer cleanup()
			res, err := backend.VoiceEmbed(c.Request().Context(), audio, ml, appConfig, *cfg)
			if err != nil {
				return mapBackendError(err)
			}
			embedding, encoder = res.GetEmbedding(), res.GetModel()
			// Fingerprint reported by the voice backend. Empty from a backend that
			// cannot report it: the voice then stays unfingerprinted.
			family, weights = res.GetEncoderFamily(), res.GetEncoderWeights()
		}
		// The family comes from the loaded encoder (portable route) or from the
		// voice backend that embedded the audio. A backend that cannot report
		// one leaves it empty and the voice stays unfingerprinted.
		meta := voiceMetadata(input.Name, input.Labels, encoder, family, weights)
		stored, err := registry.Register(c.Request().Context(), embedding, meta)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, schema.VoiceRegisterResponse{
			ID:           stored.ID,
			Name:         stored.Name,
			RegisteredAt: stored.RegisteredAt,
		})
	}
}

// voiceMetadata is what a registration stores next to the embedding. Model is
// the speaker encoder that produced it, so a consumer with a different encoder
// can tell the vectors are not comparable. family and weights fingerprint the
// encoder when it reported them (see voicerecognition.Metadata), "" otherwise.
func voiceMetadata(name string, labels map[string]string, embedderModel, family, weights string) voicerecognition.Metadata {
	return voicerecognition.Metadata{Name: name, Labels: labels, Model: embedderModel, EncoderFamily: family, EncoderWeights: weights}
}
