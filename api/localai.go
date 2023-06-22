package api

import (
	"fmt"
	"os"
	"path/filepath"

	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/tts"
	"github.com/go-skynet/LocalAI/pkg/utils"
	llama "github.com/go-skynet/go-llama.cpp"
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

func ttsEndpoint(cm *ConfigMerger, o *Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(TTSRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		piperModel, err := o.loader.BackendLoader(model.PiperBackend, input.Model, []llama.ModelOption{}, uint32(0), o.assetsDestination)
		if err != nil {
			return err
		}

		if piperModel == nil {
			return fmt.Errorf("could not load piper model")
		}

		w, ok := piperModel.(*tts.Piper)
		if !ok {
			return fmt.Errorf("loader returned non-piper object %+v", w)
		}

		if err := os.MkdirAll(o.audioDir, 0755); err != nil {
			return err
		}

		fileName := generateUniqueFileName(o.audioDir, "piper", ".wav")
		filePath := filepath.Join(o.audioDir, fileName)

		modelPath := filepath.Join(o.loader.ModelPath, input.Model)

		if err := utils.VerifyPath(modelPath, o.loader.ModelPath); err != nil {
			return err
		}

		if err := w.TTS(input.Input, modelPath, filePath); err != nil {
			return err
		}

		return c.Download(filePath)
	}
}
