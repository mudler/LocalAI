package backend

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

// AudioTransformOptions carries per-request tuning for the unary transform.
type AudioTransformOptions struct {
	// Params is forwarded verbatim to the backend (e.g. LocalVQE reads
	// params["noise_gate"] / params["noise_gate_threshold_dbfs"]).
	Params map[string]string
}

// AudioTransformOutputs are the on-disk paths of the persisted artifacts —
// the user-visible Dst plus copies of the inputs the backend actually saw.
// Inputs are persisted because the React UI history needs to display past
// runs, and rejecting them once the temp dir is cleaned up would defeat
// the point.
type AudioTransformOutputs struct {
	Dst           string
	AudioPath     string
	ReferencePath string
}

// ModelAudioTransform runs the unary AudioTransform RPC and returns the
// generated output path plus the persisted input paths. `audioPath` is
// required; `referencePath` is optional (empty => backend zero-fills the
// reference channel).
func ModelAudioTransform(
	audioPath, referencePath string,
	opts AudioTransformOptions,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (AudioTransformOutputs, *proto.AudioTransformResult, error) {
	mopts := ModelOptions(modelConfig, appConfig)
	transformModel, err := loader.Load(mopts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return AudioTransformOutputs{}, nil, err
	}
	if transformModel == nil {
		return AudioTransformOutputs{}, nil, fmt.Errorf("could not load audio-transform model %q", modelConfig.Model)
	}

	audioDir := filepath.Join(appConfig.GeneratedContentDir, "audio")
	if err := os.MkdirAll(audioDir, 0750); err != nil {
		return AudioTransformOutputs{}, nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	dst := filepath.Join(audioDir, utils.GenerateUniqueFileName(audioDir, "transform", ".wav"))

	persistedAudio, err := persistAudioInput(audioPath, audioDir, "transform-input", ".wav")
	if err != nil {
		return AudioTransformOutputs{}, nil, fmt.Errorf("persist input audio: %w", err)
	}
	persistedRef := ""
	if referencePath != "" {
		persistedRef, err = persistAudioInput(referencePath, audioDir, "transform-ref", ".wav")
		if err != nil {
			return AudioTransformOutputs{}, nil, fmt.Errorf("persist reference: %w", err)
		}
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
	}

	res, err := transformModel.AudioTransform(context.Background(), &proto.AudioTransformRequest{
		AudioPath:     audioPath,
		ReferencePath: referencePath,
		Dst:           dst,
		Params:        opts.Params,
	})

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		data := map[string]any{
			"audio_path":     audioPath,
			"reference_path": referencePath,
			"dst":            dst,
			"params":         opts.Params,
		}
		if err == nil && res != nil {
			data["sample_rate"] = res.SampleRate
			data["samples"] = res.Samples
			data["reference_provided"] = res.ReferenceProvided
			if snippet := trace.AudioSnippet(dst); snippet != nil {
				maps.Copy(data, snippet)
			}
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceAudioTransform,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(filepath.Base(audioPath), 200),
			Error:     errStr,
			Data:      data,
		})
	}

	if err != nil {
		return AudioTransformOutputs{}, nil, err
	}
	return AudioTransformOutputs{
		Dst:           dst,
		AudioPath:     persistedAudio,
		ReferencePath: persistedRef,
	}, res, nil
}

// ModelAudioTransformStream opens the bidirectional AudioTransformStream RPC
// and returns the underlying stream client. The caller is responsible for
// sending the initial Config message, subsequent Frame messages, and for
// calling CloseSend when input is done. The returned stream's Recv reports
// EOF when the backend has finished emitting frames.
func ModelAudioTransformStream(
	ctx context.Context,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (grpc.AudioTransformStreamClient, error) {
	mopts := ModelOptions(modelConfig, appConfig)
	transformModel, err := loader.Load(mopts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if transformModel == nil {
		return nil, fmt.Errorf("could not load audio-transform model %q", modelConfig.Model)
	}
	return transformModel.AudioTransformStream(ctx)
}

// persistAudioInput copies a transient input file (typically a multipart
// upload that lives in an os.TempDir slated for cleanup) into the long-lived
// GeneratedContentDir under a unique name, so the React UI can replay it
// from history.
func persistAudioInput(srcPath, dir, prefix, ext string) (string, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	dst := filepath.Join(dir, utils.GenerateUniqueFileName(dir, prefix, ext))
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, src); err != nil {
		return "", err
	}
	return dst, nil
}
