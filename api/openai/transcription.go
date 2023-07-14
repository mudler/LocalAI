package openai

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	model "github.com/go-skynet/LocalAI/pkg/model"
	whisperutil "github.com/go-skynet/LocalAI/pkg/whisper"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// https://platform.openai.com/docs/api-reference/audio/create
func TranscriptEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		m, input, err := readInput(c, o.Loader, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(m, input, cm, o.Loader, o.Debug, o.Threads, o.ContextSize, o.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
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

		whisperModel, err := o.Loader.BackendLoader(
			model.WithBackendString(model.WhisperBackend),
			model.WithModelFile(config.Model),
			model.WithThreads(uint32(config.Threads)),
			model.WithAssetDir(o.AssetsDestination))
		if err != nil {
			return err
		}

		if whisperModel == nil {
			return fmt.Errorf("could not load whisper model")
		}

		w, ok := whisperModel.(whisper.Model)
		if !ok {
			return fmt.Errorf("loader returned non-whisper object")
		}

		tr, err := whisperutil.Transcript(w, dst, input.Language, uint(config.Threads))
		if err != nil {
			return err
		}

		log.Debug().Msgf("Trascribed: %+v", tr)
		// TODO: handle different outputs here
		return c.Status(http.StatusOK).JSON(fiber.Map{"text": tr})
	}
}
