package openai

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	model "github.com/mudler/LocalAI/pkg/model"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// TranscriptEndpoint is the OpenAI Whisper API endpoint https://platform.openai.com/docs/api-reference/audio/create
// @Summary Transcribes audio into the input language.
// @accept multipart/form-data
// @Param model formData string true "model"
// @Param file formData file true "file"
// @Success 200 {object} map[string]string	 "Response"
// @Router /v1/audio/transcriptions [post]
func TranscriptEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		m, input, err := readRequest(c, cl, ml, appConfig, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := mergeRequestWithConfig(m, input, cl, ml, appConfig.Debug, appConfig.Threads, appConfig.ContextSize, appConfig.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request: %w", err)
		}
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
			log.Debug().Msgf("Audio file copying error %+v - %+v - err %+v", file.Filename, dst, err)
			return err
		}

		log.Debug().Msgf("Audio file copied to: %+v", dst)

		tr, err := backend.ModelTranscription(dst, input.Language, input.Translate, ml, *config, appConfig)
		if err != nil {
			return err
		}

		log.Debug().Msgf("Trascribed: %+v", tr)
		// TODO: handle different outputs here
		return c.Status(http.StatusOK).JSON(tr)
	}
}
