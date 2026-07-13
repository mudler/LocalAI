package localai

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/xlog"
)

const maxVoiceProfileJSONBytes = voiceprofile.MaxAudioBytes*4/3 + 2*1024*1024

// VoiceProfileListResponse is returned by the profile library endpoint.
type VoiceProfileListResponse struct {
	Data []voiceprofile.Profile `json:"data"`
}

// CreateVoiceProfileRequest is the JSON alternative to multipart creation.
// It is primarily used by the LocalAI admin MCP client; the browser sends a
// multipart audio field to avoid base64 overhead.
type CreateVoiceProfileRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	Language         string `json:"language,omitempty"`
	Transcript       string `json:"transcript"`
	AudioBase64      string `json:"audio_base64"`
	ConsentConfirmed bool   `json:"consent_confirmed"`
}

func voiceProfileError(code int, message string) schema.ErrorResponse {
	return schema.ErrorResponse{Error: &schema.APIError{
		Code:    code,
		Message: message,
		Type:    "voice_profile_error",
	}}
}

func writeVoiceProfileError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, voiceprofile.ErrNotFound):
		return c.JSON(http.StatusNotFound, voiceProfileError(http.StatusNotFound, "voice profile not found"))
	case errors.Is(err, voiceprofile.ErrAudioTooLarge):
		return c.JSON(http.StatusRequestEntityTooLarge, voiceProfileError(http.StatusRequestEntityTooLarge, err.Error()))
	case errors.Is(err, voiceprofile.ErrInvalidInput),
		errors.Is(err, voiceprofile.ErrConsentRequired),
		errors.Is(err, voiceprofile.ErrUnsupportedWAV):
		return c.JSON(http.StatusBadRequest, voiceProfileError(http.StatusBadRequest, err.Error()))
	default:
		xlog.Error("Voice profile operation failed", "error", err)
		return c.JSON(http.StatusInternalServerError, voiceProfileError(http.StatusInternalServerError, "voice profile operation failed"))
	}
}

func isRequestBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return true
	}
	var httpError *echo.HTTPError
	return errors.As(err, &httpError) && errors.As(httpError.Internal, &maxBytesError)
}

// ListVoiceProfilesEndpoint lists reusable cloning references. The route is
// available to users with the Audio Speech/TTS feature so they can select a
// profile; profile mutation remains admin-only.
//
// @Summary List voice profiles
// @Description List saved voice-cloning references without exposing filesystem paths.
// @Tags audio,voice-profiles
// @Produce json
// @Success 200 {object} VoiceProfileListResponse
// @Failure 500 {object} schema.ErrorResponse
// @Router /api/voice-profiles [get]
func ListVoiceProfilesEndpoint(store *voiceprofile.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if store == nil {
			return writeVoiceProfileError(c, errors.New("voice profile store is unavailable"))
		}
		profiles, err := store.List(c.Request().Context())
		if err != nil {
			return writeVoiceProfileError(c, err)
		}
		return c.JSON(http.StatusOK, VoiceProfileListResponse{Data: profiles})
	}
}

// CreateVoiceProfileEndpoint validates and persists a reusable cloning
// reference. It accepts multipart/form-data (audio field) or JSON with a
// base64-encoded 16-bit PCM WAV. Admin-only.
//
// @Summary Create a voice profile
// @Description Save a consent-confirmed PCM WAV reference clip and exact transcript for voice cloning. Admin-only.
// @Tags audio,voice-profiles
// @Accept multipart/form-data
// @Accept json
// @Produce json
// @Param name formData string true "Display name"
// @Param description formData string false "Optional description"
// @Param language formData string false "Optional language tag"
// @Param transcript formData string true "Exact transcript of the reference clip"
// @Param consent_confirmed formData bool true "Confirms authorization to clone the voice"
// @Param audio formData file true "16-bit PCM WAV, preferably mono 24 kHz, 1-120 seconds, up to 50 MiB"
// @Success 201 {object} voiceprofile.Profile
// @Failure 400 {object} schema.ErrorResponse
// @Failure 413 {object} schema.ErrorResponse
// @Router /api/voice-profiles [post]
func CreateVoiceProfileEndpoint(store *voiceprofile.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if store == nil {
			return writeVoiceProfileError(c, errors.New("voice profile store is unavailable"))
		}

		contentType := c.Request().Header.Get(echo.HeaderContentType)
		if strings.HasPrefix(contentType, echo.MIMEMultipartForm) {
			c.Request().Body = http.MaxBytesReader(c.Response().Writer, c.Request().Body, voiceprofile.MaxAudioBytes+2*1024*1024)
			fileHeader, err := c.FormFile("audio")
			if err != nil {
				if isRequestBodyTooLarge(err) {
					return writeVoiceProfileError(c, voiceprofile.ErrAudioTooLarge)
				}
				return writeVoiceProfileError(c, fmt.Errorf("%w: audio is required", voiceprofile.ErrInvalidInput))
			}
			if fileHeader.Size > voiceprofile.MaxAudioBytes {
				return writeVoiceProfileError(c, voiceprofile.ErrAudioTooLarge)
			}
			audio, err := fileHeader.Open()
			if err != nil {
				return writeVoiceProfileError(c, fmt.Errorf("open uploaded audio: %w", err))
			}
			defer func() { _ = audio.Close() }()

			consent, _ := strconv.ParseBool(c.FormValue("consent_confirmed"))
			profile, err := store.Create(c.Request().Context(), voiceprofile.CreateInput{
				Name:             c.FormValue("name"),
				Description:      c.FormValue("description"),
				Language:         c.FormValue("language"),
				Transcript:       c.FormValue("transcript"),
				ConsentConfirmed: consent,
			}, audio)
			if err != nil {
				return writeVoiceProfileError(c, err)
			}
			return c.JSON(http.StatusCreated, profile)
		}

		c.Request().Body = http.MaxBytesReader(c.Response().Writer, c.Request().Body, maxVoiceProfileJSONBytes)
		var request CreateVoiceProfileRequest
		if err := c.Bind(&request); err != nil {
			if isRequestBodyTooLarge(err) {
				return writeVoiceProfileError(c, voiceprofile.ErrAudioTooLarge)
			}
			return writeVoiceProfileError(c, fmt.Errorf("%w: invalid JSON body", voiceprofile.ErrInvalidInput))
		}
		if base64.StdEncoding.DecodedLen(len(request.AudioBase64)) > int(voiceprofile.MaxAudioBytes) {
			return writeVoiceProfileError(c, voiceprofile.ErrAudioTooLarge)
		}
		audio := base64.NewDecoder(base64.StdEncoding, strings.NewReader(request.AudioBase64))
		profile, err := store.Create(c.Request().Context(), voiceprofile.CreateInput{
			Name:             request.Name,
			Description:      request.Description,
			Language:         request.Language,
			Transcript:       request.Transcript,
			ConsentConfirmed: request.ConsentConfirmed,
		}, audio)
		if err != nil {
			return writeVoiceProfileError(c, err)
		}
		return c.JSON(http.StatusCreated, profile)
	}
}

// ServeVoiceProfileAudioEndpoint streams an authenticated preview with range
// request support. The response is private and never cacheable by shared
// proxies because the clip is biometric source material.
//
// @Summary Preview voice profile audio
// @Description Stream the saved reference WAV for an authenticated TTS user.
// @Tags audio,voice-profiles
// @Produce audio/x-wav
// @Param id path string true "Voice profile UUID"
// @Success 200 {string} binary
// @Failure 404 {object} schema.ErrorResponse
// @Router /api/voice-profiles/{id}/audio [get]
func ServeVoiceProfileAudioEndpoint(store *voiceprofile.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if store == nil {
			return writeVoiceProfileError(c, errors.New("voice profile store is unavailable"))
		}
		file, profile, err := store.OpenAudio(c.Request().Context(), c.Param("id"))
		if err != nil {
			return writeVoiceProfileError(c, err)
		}
		defer func() { _ = file.Close() }()

		c.Response().Header().Set(echo.HeaderContentType, "audio/wav")
		c.Response().Header().Set(echo.HeaderCacheControl, "private, no-store")
		c.Response().Header().Set("X-Content-Type-Options", "nosniff")
		c.Response().Header().Set(echo.HeaderContentDisposition, `inline; filename="reference.wav"`)
		http.ServeContent(c.Response().Writer, c.Request(), "reference.wav", profile.UpdatedAt, file)
		return nil
	}
}

// DeleteVoiceProfileEndpoint permanently removes a profile. Admin-only.
//
// @Summary Delete a voice profile
// @Description Permanently remove a saved voice-cloning profile. Admin-only.
// @Tags audio,voice-profiles
// @Param id path string true "Voice profile UUID"
// @Success 204
// @Failure 404 {object} schema.ErrorResponse
// @Router /api/voice-profiles/{id} [delete]
func DeleteVoiceProfileEndpoint(store *voiceprofile.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if store == nil {
			return writeVoiceProfileError(c, errors.New("voice profile store is unavailable"))
		}
		if err := store.Delete(c.Request().Context(), c.Param("id")); err != nil {
			return writeVoiceProfileError(c, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}
