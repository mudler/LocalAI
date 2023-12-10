package openai

import (
	"fmt"
	"net/http"
	"os"
	"path"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// https://platform.openai.com/docs/api-reference/audio/create
func TranscriptEndpoint(cl *backend.ConfigLoader, ml *model.ModelLoader, so *datamodel.StartupOptions) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		modelName, input, err := readInput(c, so, ml, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		// retrieve the file data from the request
		file, err := c.FormFile("file")
		if err != nil {
			return err
		}

		dst, err := utils.CreateTempFileFromMultipartFile(file, "", "transcription") // 3rd param formerly whisper
		if err != nil {
			return err
		}

		log.Debug().Msgf("Audio file copied to: %+v", dst)
		defer os.RemoveAll(path.Dir(dst))

		tr, err := backend.TranscriptionOpenAIRequest(modelName, input, dst, cl, ml, so)
		if err != nil {
			return fmt.Errorf("error generating transcription request: +%w", err)
		}
		log.Debug().Msgf("Trascribed: %+v", tr)
		// TODO: handle different outputs here
		return c.Status(http.StatusOK).JSON(tr)
	}
}
