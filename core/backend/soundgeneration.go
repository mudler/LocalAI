package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

func SoundGeneration(
	text string,
	duration *float32,
	temperature *float32,
	doSample *bool,
	sourceFile *string,
	sourceDivisor *int32,
	think *bool,
	caption string,
	lyrics string,
	bpm *int32,
	keyscale string,
	language string,
	timesignature string,
	instrumental *bool,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (string, *proto.Result, error) {

	opts := ModelOptions(modelConfig, appConfig)
	soundGenModel, err := loader.Load(opts...)
	if err != nil {
		return "", nil, err
	}

	if soundGenModel == nil {
		return "", nil, fmt.Errorf("could not load sound generation model")
	}

	if err := os.MkdirAll(appConfig.GeneratedContentDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	audioDir := filepath.Join(appConfig.GeneratedContentDir, "audio")
	if err := os.MkdirAll(audioDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	fileName := utils.GenerateUniqueFileName(audioDir, "sound_generation", ".wav")
	filePath := filepath.Join(audioDir, fileName)
	if filePath, err = filepath.Abs(filePath); err != nil {
		return "", nil, fmt.Errorf("failed resolving sound generation path: %w", err)
	}

	req := &proto.SoundGenerationRequest{
		Text:        text,
		Model:       modelConfig.Model,
		Dst:         filePath,
		Sample:      doSample,
		Duration:    duration,
		Temperature: temperature,
		Src:         sourceFile,
		SrcDivisor:  sourceDivisor,
	}
	if think != nil {
		req.Think = think
	}
	if caption != "" {
		req.Caption = &caption
	}
	if lyrics != "" {
		req.Lyrics = &lyrics
	}
	if bpm != nil {
		req.Bpm = bpm
	}
	if keyscale != "" {
		req.Keyscale = &keyscale
	}
	if language != "" {
		req.Language = &language
	}
	if timesignature != "" {
		req.Timesignature = &timesignature
	}
	if instrumental != nil {
		req.Instrumental = instrumental
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
	}

	res, err := soundGenModel.SoundGeneration(context.Background(), req)

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		} else if res != nil && !res.Success {
			errStr = fmt.Sprintf("sound generation error: %s", res.Message)
		}

		summary := trace.TruncateString(text, 200)
		if summary == "" && caption != "" {
			summary = trace.TruncateString(caption, 200)
		}

		traceData := map[string]any{
			"text":    text,
			"caption": caption,
			"lyrics":  lyrics,
		}
		if duration != nil {
			traceData["duration"] = *duration
		}
		if temperature != nil {
			traceData["temperature"] = *temperature
		}

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceSoundGeneration,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   summary,
			Error:     errStr,
			Data:      traceData,
		})
	}

	if err != nil {
		return "", nil, err
	}
	if res != nil && !res.Success {
		return "", nil, fmt.Errorf("error during sound generation: %s", res.Message)
	}
	return filePath, res, nil
}
