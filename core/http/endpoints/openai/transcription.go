package openai

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/go-skynet/LocalAI/core/backend"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"

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
func TranscriptEndpoint(fce *fiberContext.FiberContextExtractor, tbs *backend.TranscriptionBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		// TODO: Investigate this file copy stuff later - potentially belongs in service.

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

		request.File = dst

		responseChannel := tbs.Transcribe(request)
		rawResponse := <-responseChannel

		if rawResponse.Error != nil {
			return rawResponse.Error
		}
		log.Debug().Msgf("Transcribed: %+v", rawResponse.Value)
		// TODO: handle different outputs here
		return c.Status(http.StatusOK).JSON(rawResponse.Value)
	}
}
