package cambai

import (
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// TranscriptionEndpoint handles CAMB AI transcription (POST /apis/transcribe).
// Runs synchronously but returns results in CAMB AI's async task format.
func TranscriptionEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		input, _ := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.CambAITranscriptionRequest)

		language := ""
		if input != nil && input.LanguageID > 0 {
			language = schema.CambAILanguageCodeFromID(input.LanguageID)
		}

		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, schema.CambAIErrorResponse{
				Detail: "Audio file is required. Upload as multipart form field 'file'.",
			})
		}

		f, err := file.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		dir, err := os.MkdirTemp("", "cambai-transcribe")
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
		dstFile.Close()

		xlog.Debug("CAMB AI transcription request", "file", dst, "language", language)

		tr, err := backend.ModelTranscription(dst, language, false, false, "", ml, *cfg, appConfig)
		if err != nil {
			return err
		}

		taskID := uuid.New().String()

		return c.JSON(http.StatusOK, schema.CambAITaskStatusResponse{
			Status: "SUCCESS",
			RunID:  taskID,
			Output: schema.CambAITranscriptionResponse{
				Text:     tr.Text,
				Language: language,
			},
		})
	}
}
