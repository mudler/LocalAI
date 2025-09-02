package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

type TranscriptCMD struct {
	Filename string `arg:""`

	Backend    string `short:"b" default:"whisper" help:"Backend to run the transcription model"`
	Model      string `short:"m" required:"" help:"Model name to run the TTS"`
	Language   string `short:"l" help:"Language of the audio file"`
	Translate  bool   `short:"c" help:"Translate the transcription to english"`
	Diarize    bool   `short:"d" help:"Mark speaker turns"`
	Threads    int    `short:"t" default:"1" help:"Number of threads used for parallel computation"`
	ModelsPath string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
}

func (t *TranscriptCMD) Run(ctx *cliContext.Context) error {
	systemState, err := system.GetSystemState(
		system.WithModelPath(t.ModelsPath),
	)
	if err != nil {
		return err
	}
	opts := &config.ApplicationConfig{
		SystemState: systemState,
		Context:     context.Background(),
	}

	cl := config.NewModelConfigLoader(t.ModelsPath)
	ml := model.NewModelLoader(systemState, opts.SingleBackend)
	if err := cl.LoadModelConfigsFromPath(t.ModelsPath); err != nil {
		return err
	}

	c, exists := cl.GetModelConfig(t.Model)
	if !exists {
		return errors.New("model not found")
	}

	c.Threads = &t.Threads

	defer func() {
		err := ml.StopAllGRPC()
		if err != nil {
			log.Error().Err(err).Msg("unable to stop all grpc processes")
		}
	}()

	tr, err := backend.ModelTranscription(t.Filename, t.Language, t.Translate, t.Diarize, ml, c, opts)
	if err != nil {
		return err
	}
	for _, segment := range tr.Segments {
		fmt.Println(segment.Start.String(), "-", segment.Text)
	}
	return nil
}
