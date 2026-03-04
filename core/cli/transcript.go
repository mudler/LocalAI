package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/format"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

type TranscriptCMD struct {
	Filename string `arg:"" name:"file" help:"Audio file to transcribe" type:"path"`

	Backend          string                                 `short:"b" default:"whisper" help:"Backend to run the transcription model"`
	Model            string                                 `short:"m" required:"" help:"Model name to run the TTS"`
	Language         string                                 `short:"l" help:"Language of the audio file"`
	Translate        bool                                   `short:"c" help:"Translate the transcription to English"`
	Diarize          bool                                   `short:"d" help:"Mark speaker turns"`
	Threads          int                                    `short:"t" default:"1" help:"Number of threads used for parallel computation"`
	BackendsPath     string                                 `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"storage"`
	ModelsPath       string                                 `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	BackendGalleries string                                 `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends" default:"${backends}"`
	Prompt           string                                 `short:"p" help:"Previous transcribed text or words that hint at what the model should expect"`
	ResponseFormat   schema.TranscriptionResponseFormatType `short:"f" default:"" help:"Response format for Whisper models, can be one of (txt, lrc, srt, vtt, json, verbose_json)"`
	PrettyPrint      bool                                   `help:"Used with response_format json or verbose_json for pretty printing"`
}

func (t *TranscriptCMD) Run(ctx *cliContext.Context) error {
	systemState, err := system.GetSystemState(
		system.WithBackendPath(t.BackendsPath),
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
	ml := model.NewModelLoader(systemState)

	if err := gallery.RegisterBackends(systemState, ml); err != nil {
		xlog.Error("error registering external backends", "error", err)
	}

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
			xlog.Error("unable to stop all grpc processes", "error", err)
		}
	}()

	tr, err := backend.ModelTranscription(t.Filename, t.Language, t.Translate, t.Diarize, t.Prompt, ml, c, opts)
	if err != nil {
		return err
	}

	switch t.ResponseFormat {
	case schema.TranscriptionResponseFormatLrc, schema.TranscriptionResponseFormatSrt, schema.TranscriptionResponseFormatVtt, schema.TranscriptionResponseFormatText:
		fmt.Println(format.TranscriptionResponse(tr, t.ResponseFormat))
	case schema.TranscriptionResponseFormatJson:
		tr.Segments = nil
		fallthrough
	case schema.TranscriptionResponseFormatJsonVerbose:
		var mtr []byte
		var err error
		if t.PrettyPrint {
			mtr, err = json.MarshalIndent(tr, "", "    ")
		} else {
			mtr, err = json.Marshal(tr)
		}
		if err != nil {
			return err
		}
		fmt.Println(string(mtr))
	default:
		for _, segment := range tr.Segments {
			fmt.Println(segment.Start.String(), "-", strings.TrimSpace(segment.Text))
		}
	}
	return nil
}
