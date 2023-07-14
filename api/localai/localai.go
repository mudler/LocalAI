package localai

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	config "github.com/go-skynet/LocalAI/api/config"

	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/gofiber/fiber/v2"
)

type TTSRequest struct {
	Model string `json:"model" yaml:"model"`
	Input string `json:"input" yaml:"input"`
}

func generateUniqueFileName(dir, baseName, ext string) string {
	counter := 1
	fileName := baseName + ext

	for {
		filePath := filepath.Join(dir, fileName)
		_, err := os.Stat(filePath)
		if os.IsNotExist(err) {
			return fileName
		}

		counter++
		fileName = fmt.Sprintf("%s_%d%s", baseName, counter, ext)
	}
}

func TTSEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(TTSRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		piperModel, err := o.Loader.BackendLoader(
			model.WithBackendString(model.PiperBackend),
			model.WithModelFile(input.Model),
			model.WithContext(o.Context),
			model.WithAssetDir(o.AssetsDestination))
		if err != nil {
			return err
		}

		if piperModel == nil {
			return fmt.Errorf("could not load piper model")
		}

		if err := os.MkdirAll(o.AudioDir, 0755); err != nil {
			return fmt.Errorf("failed creating audio directory: %s", err)
		}

		fileName := generateUniqueFileName(o.AudioDir, "piper", ".wav")
		filePath := filepath.Join(o.AudioDir, fileName)

		modelPath := filepath.Join(o.Loader.ModelPath, input.Model)

		if err := utils.VerifyPath(modelPath, o.Loader.ModelPath); err != nil {
			return err
		}

		if _, err := piperModel.TTS(context.Background(), &proto.TTSRequest{
			Text:  input.Input,
			Model: modelPath,
			Dst:   filePath,
		}); err != nil {
			return err
		}

		return c.Download(filePath)
	}
}
