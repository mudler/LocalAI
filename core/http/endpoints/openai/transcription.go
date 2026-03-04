package openai

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/format"
	model "github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"
)

// TranscriptEndpoint is the OpenAI Whisper API endpoint https://platform.openai.com/docs/api-reference/audio/create
// @Summary Transcribes audio into the input language.
// @accept multipart/form-data
// @Param model formData string true "model"
// @Param file formData file true "file"
// @Success 200 {object} map[string]string	 "Response"
// @Router /v1/audio/transcriptions [post]
func TranscriptEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return echo.ErrBadRequest
		}

		diarize := c.FormValue("diarize") != "false"
		prompt := c.FormValue("prompt")
		responseFormat := schema.TranscriptionResponseFormatType(c.FormValue("response_format"))

		// retrieve the file data from the request
		file, err := c.FormFile("file")
		if err != nil {
			return err
		}
		f, err := file.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		dir, err := os.MkdirTemp("", "whisper")

		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)

		dst := filepath.Join(dir, path.Base(file.Filename))
		dstFile, err := os.Create(dst)
		if err != nil {
			return err
		}

		if _, err := io.Copy(dstFile, f); err != nil {
			xlog.Debug("Audio file copying error", "filename", file.Filename, "dst", dst, "error", err)
			return err
		}

		xlog.Debug("Audio file copied", "dst", dst)

		tr, err := backend.ModelTranscription(dst, input.Language, input.Translate, diarize, prompt, ml, *config, appConfig)
		if err != nil {
			return err
		}

		xlog.Debug("Transcribed", "transcription", tr)

		switch responseFormat {
		case schema.TranscriptionResponseFormatLrc, schema.TranscriptionResponseFormatText, schema.TranscriptionResponseFormatSrt, schema.TranscriptionResponseFormatVtt:
			return c.String(http.StatusOK, format.TranscriptionResponse(tr, responseFormat))
		case schema.TranscriptionResponseFormatJson:
			tr.Segments = nil
			fallthrough
		case schema.TranscriptionResponseFormatJsonVerbose, "": // maintain backwards compatibility
			return c.JSON(http.StatusOK, tr)
		default:
			return errors.New("invalid response_format")
		}
	}
}
