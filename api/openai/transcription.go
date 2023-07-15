package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"

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
			model.WithContext(o.Context),
			model.WithThreads(uint32(config.Threads)),
			model.WithAssetDir(o.AssetsDestination))
		if err != nil {
			return err
		}

		if whisperModel == nil {
			return fmt.Errorf("could not load whisper model")
		}

		tr, err := whisperModel.AudioTranscription(context.Background(), &proto.TranscriptRequest{
			Dst:      dst,
			Language: input.Language,
			Threads:  uint32(config.Threads),
		})
		if err != nil {
			return err
		}

		log.Debug().Msgf("Trascribed: %+v", tr)
		// TODO: handle different outputs here
		return c.Status(http.StatusOK).JSON(tr)
	}
}
