package localai

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/xlog"
)

const maxVoiceProfileJSONBytes = voiceprofile.MaxAudioBytes*voiceprofile.MaxReferences*4/3 + 2*1024*1024

// VoiceProfileListResponse is returned by the profile library endpoint.
type VoiceProfileListResponse struct {
	Data []voiceprofile.Profile `json:"data"`
}

// CreateVoiceProfileRequest is the JSON alternative to multipart creation.
// The browser sends a multipart audio field to avoid base64 overhead.
type CreateVoiceProfileRequest struct {
	Name             string                               `json:"name"`
	Description      string                               `json:"description,omitempty"`
	Language         string                               `json:"language,omitempty"`
	Transcript       string                               `json:"transcript"`
	AudioBase64      string                               `json:"audio_base64"`
	ConsentConfirmed bool                                 `json:"consent_confirmed"`
	References       []CreateVoiceProfileReferenceRequest `json:"references,omitempty"`
}

type CreateVoiceProfileReferenceRequest struct {
	Transcript  string `json:"transcript"`
	AudioBase64 string `json:"audio_base64"`
}

// VoiceViewer is the user a request acts for, for the ownership rules of the
// voice profiles. Without accounts (no user in the context) the caller is the
// administrator, so every profile stays instance-wide as it always was.
func VoiceViewer(c echo.Context) voiceprofile.Viewer {
	u := auth.GetUser(c)
	if u == nil {
		return voiceprofile.AdminViewer()
	}
	return voiceprofile.Viewer{UserID: u.ID, Admin: u.Role == auth.RoleAdmin}
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
	case errors.Is(err, voiceprofile.ErrForbidden):
		return c.JSON(http.StatusForbidden, voiceProfileError(http.StatusForbidden, err.Error()))
	case errors.Is(err, voiceprofile.ErrQuotaExceeded):
		return c.JSON(http.StatusConflict, voiceProfileError(http.StatusConflict, err.Error()))
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

// ListVoiceProfilesEndpoint lists the reusable cloning references the caller
// may use: their own and the instance-wide ones; an administrator sees all.
// The route is available to users with the Audio Speech/TTS feature.
//
// @Summary List voice profiles
// @Description List the saved voice-cloning references the caller may use (their own and the instance-wide ones; an administrator sees all), without exposing filesystem paths.
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
		profiles, err := store.ListFor(c.Request().Context(), VoiceViewer(c))
		if err != nil {
			return writeVoiceProfileError(c, err)
		}
		return c.JSON(http.StatusOK, VoiceProfileListResponse{Data: profiles})
	}
}

// CreateVoiceProfileEndpoint validates and persists a reusable cloning
// reference. It accepts multipart/form-data (audio field) or JSON with a
// base64-encoded 16-bit PCM WAV. The profile belongs to the caller, except
// that an administrator creates instance-wide profiles. A user who is not an
// administrator may own at most voiceprofile.MaxProfilesPerUser profiles.
//
// @Summary Create a voice profile
// @Description Save a consent-confirmed PCM WAV reference clip and exact transcript for voice cloning. Needs the audio speech feature. The profile belongs to the caller (an administrator creates instance-wide profiles); a user who is not an administrator may own at most 20.
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
// @Failure 403 {object} schema.ErrorResponse
// @Failure 409 {object} schema.ErrorResponse "The per-user profile limit is reached"
// @Failure 413 {object} schema.ErrorResponse
// @Router /api/voice-profiles [post]
func CreateVoiceProfileEndpoint(store *voiceprofile.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if store == nil {
			return writeVoiceProfileError(c, errors.New("voice profile store is unavailable"))
		}

		contentType := c.Request().Header.Get(echo.HeaderContentType)
		if strings.HasPrefix(contentType, echo.MIMEMultipartForm) {
			c.Request().Body = http.MaxBytesReader(c.Response().Writer, c.Request().Body, voiceprofile.MaxAudioBytes*voiceprofile.MaxReferences+2*1024*1024)
			form, err := c.MultipartForm()
			if err != nil {
				if isRequestBodyTooLarge(err) {
					return writeVoiceProfileError(c, voiceprofile.ErrAudioTooLarge)
				}
				return writeVoiceProfileError(c, fmt.Errorf("%w: audio is required", voiceprofile.ErrInvalidInput))
			}
			files := form.File["audio"]
			transcripts := form.Value["transcript"]
			if len(files) == 0 || len(files) != len(transcripts) || len(files) > voiceprofile.MaxReferences {
				return writeVoiceProfileError(c, fmt.Errorf("%w: each audio needs one transcript", voiceprofile.ErrInvalidInput))
			}
			references := make([]voiceprofile.ReferenceInput, 0, len(files))
			for index, fileHeader := range files {
				if fileHeader.Size > voiceprofile.MaxAudioBytes {
					return writeVoiceProfileError(c, voiceprofile.ErrAudioTooLarge)
				}
				audio, err := fileHeader.Open()
				if err != nil {
					return writeVoiceProfileError(c, fmt.Errorf("open uploaded audio: %w", err))
				}
				defer func() { _ = audio.Close() }()
				references = append(references, voiceprofile.ReferenceInput{Transcript: transcripts[index], Audio: audio})
			}

			consent, _ := strconv.ParseBool(c.FormValue("consent_confirmed"))
			profile, err := store.CreateFor(c.Request().Context(), VoiceViewer(c), voiceprofile.CreateInput{
				Name:             c.FormValue("name"),
				Description:      c.FormValue("description"),
				Language:         c.FormValue("language"),
				ConsentConfirmed: consent,
			}, references)
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
		referenceRequests := request.References
		if len(referenceRequests) == 0 {
			referenceRequests = []CreateVoiceProfileReferenceRequest{{Transcript: request.Transcript, AudioBase64: request.AudioBase64}}
		}
		if len(referenceRequests) > voiceprofile.MaxReferences {
			return writeVoiceProfileError(c, voiceprofile.ErrInvalidInput)
		}
		references := make([]voiceprofile.ReferenceInput, 0, len(referenceRequests))
		for _, reference := range referenceRequests {
			if base64.StdEncoding.DecodedLen(len(reference.AudioBase64)) > int(voiceprofile.MaxAudioBytes) {
				return writeVoiceProfileError(c, voiceprofile.ErrAudioTooLarge)
			}
			references = append(references, voiceprofile.ReferenceInput{Transcript: reference.Transcript, Audio: base64.NewDecoder(base64.StdEncoding, strings.NewReader(reference.AudioBase64))})
		}
		profile, err := store.CreateFor(c.Request().Context(), VoiceViewer(c), voiceprofile.CreateInput{
			Name:             request.Name,
			Description:      request.Description,
			Language:         request.Language,
			ConsentConfirmed: request.ConsentConfirmed,
		}, references)
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
		file, profile, err := store.OpenAudioFor(c.Request().Context(), VoiceViewer(c), c.Param("id"))
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

// DeleteVoiceProfileEndpoint permanently removes a profile. The owner and
// administrators may delete it; an instance-wide profile only an administrator.
// The profile of another user answers 404.
//
// @Summary Delete a voice profile
// @Description Permanently remove a saved voice-cloning profile. The owner or an administrator may delete it; an instance-wide profile only an administrator. The profile of another user answers 404.
// @Tags audio,voice-profiles
// @Param id path string true "Voice profile UUID"
// @Success 204
// @Failure 403 {object} schema.ErrorResponse
// @Failure 404 {object} schema.ErrorResponse
// @Router /api/voice-profiles/{id} [delete]
func DeleteVoiceProfileEndpoint(store *voiceprofile.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if store == nil {
			return writeVoiceProfileError(c, errors.New("voice profile store is unavailable"))
		}
		if err := store.DeleteFor(c.Request().Context(), VoiceViewer(c), c.Param("id")); err != nil {
			return writeVoiceProfileError(c, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}
