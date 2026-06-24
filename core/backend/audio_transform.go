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
	// Stems are the other named outputs the same run produced, in the model's
	// own order and including the one whose content Dst carries. Empty for a
	// single-output transform.
	//
	// A separation backend writes every stem beside Dst from ONE inference.
	// Dropping them here would mean a caller who wants drums as well as vocals
	// has to run the whole separation again per stem, which is precisely what
	// the single run exists to avoid.
	Stems []AudioTransformStem
}

// AudioTransformStem is one named output of a multi-output transform, e.g. the
// "vocals" track of a source separation.
type AudioTransformStem struct {
	Name string
	Dst  string
}

// ModelAudioTransform runs the unary AudioTransform RPC and returns the
// generated output path plus the persisted input paths. `audioPath` is
// required; `referencePath` is optional (empty => backend zero-fills the
// reference channel).
func ModelAudioTransform(
	ctx context.Context,
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
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return AudioTransformOutputs{}, nil, err
	}
	defer release()

	var startTime time.Time
	var traceID string
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
		traceID = trace.BeginBackendTrace(trace.BackendTrace{Timestamp: startTime, Type: trace.BackendTraceAudioTransform, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: trace.TruncateString(filepath.Base(audioPath), 200)})
	}
	defer trace.CancelBackendTrace(traceID)

	res, err := transformModel.AudioTransform(ctx, &proto.AudioTransformRequest{
		ModelIdentity: modelConfig.Model,
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
			if snippet := trace.AudioSnippet(dst, appConfig.TracingMaxBodyBytes); snippet != nil {
				maps.Copy(data, snippet)
			}
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			ID:        traceID,
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
		Stems:         collectStems(res, audioDir),
	}, res, nil
}

// collectStems turns the backend's reported stems into the caller-facing list.
//
// Every path is checked to be a direct child of audioDir, the generated-content
// directory this request handed the backend. A backend is a separate process
// and its response is not this process's data: a stem path pointing at /etc or
// at another user's file would otherwise be served straight back through the
// HTTP layer, which resolves these into URLs. A stem that fails the check is
// dropped rather than fatal, so a well-behaved majority still reaches the
// caller.
func collectStems(res *proto.AudioTransformResult, audioDir string) []AudioTransformStem {
	if res == nil || len(res.GetStems()) == 0 {
		return nil
	}
	stems := make([]AudioTransformStem, 0, len(res.GetStems()))
	for _, stem := range res.GetStems() {
		name, path := stem.GetName(), stem.GetDst()
		if name == "" || path == "" {
			continue
		}
		if filepath.Dir(filepath.Clean(path)) != filepath.Clean(audioDir) {
			continue
		}
		stems = append(stems, AudioTransformStem{Name: name, Dst: path})
	}
	if len(stems) == 0 {
		return nil
	}
	return stems
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
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return nil, err
	}
	stream, err := transformModel.AudioTransformStream(ctx)
	if err != nil {
		release()
		return nil, err
	}
	stream.AddCleanup(release)
	return stream, nil
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
