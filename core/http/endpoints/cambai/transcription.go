package cambai

import (
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

var transcriptionTaskResults = sync.Map{}

// TranscriptionEndpoint handles CAMB AI transcription (POST /apis/transcribe).
// The SDK sends multipart form with optional file upload and/or media_url.
// Returns {"task_id": "..."} matching OrchestratorPipelineCallResult.
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
		// SDK sends language as multipart form field too
		if language == "" {
			if langField := c.FormValue("language"); langField != "" {
				language = langField
			}
		}

		// Try file upload first (field "file" or "media_file")
		var audioPath string
		for _, fieldName := range []string{"file", "media_file"} {
			file, err := c.FormFile(fieldName)
			if err != nil {
				continue
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
				dstFile.Close()
				return err
			}
			dstFile.Close()
			audioPath = dst
			break
		}

		// Fall back to media_url form field
		if audioPath == "" {
			mediaURL := c.FormValue("media_url")
			if mediaURL == "" {
				mediaURL = c.FormValue("audio_url")
			}
			if mediaURL != "" {
				audioPath = mediaURL
			}
		}

		if audioPath == "" {
			return c.JSON(http.StatusBadRequest, schema.CambAIErrorResponse{
				Detail: "Either a file upload or media_url is required.",
			})
		}

		xlog.Debug("CAMB AI transcription request", "path", audioPath, "language", language)

		tr, err := backend.ModelTranscription(audioPath, language, false, false, "", ml, *cfg, appConfig)
		if err != nil {
			return err
		}

		taskID := uuid.New().String()
		transcriptionTaskResults.Store(taskID, tr.Text)

		return c.JSON(http.StatusOK, schema.CambAITaskResponse{
			TaskID: taskID,
			Status: "SUCCESS",
			RunID:  taskID,
		})
	}
}
