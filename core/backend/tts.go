package backend

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	laudio "github.com/mudler/LocalAI/pkg/audio"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

// newTTSRequest assembles the gRPC TTSRequest from the per-request inputs. The
// optional instructions string is only attached when non-empty so backends can
// distinguish "no per-request instruction" (fall back to YAML) from an explicit
// empty one. params is forwarded as-is (nil when unset).
func newTTSRequest(text, modelPath, voice, dst, language, instructions string, params map[string]string) *proto.TTSRequest {
	req := &proto.TTSRequest{
		Text:     text,
		Model:    modelPath,
		Voice:    voice,
		Dst:      dst,
		Language: &language,
		Params:   params,
	}
	if instructions != "" {
		req.Instructions = &instructions
	}
	return req
}

func ModelTTS(
	ctx context.Context,
	text,
	voice,
	language,
	instructions string,
	params map[string]string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (string, *proto.Result, error) {
	// model.WithContext carries the request context into the load so distributed
	// routing decisions reach the request's X-LocalAI-Node holder via
	// distributedhdr.Stamp. context.WithoutCancel keeps those values but drops
	// the request's cancellation, so a slow first load still completes and
	// caches if the client disconnects instead of aborting the LoadModel RPC and
	// tearing down the backend process (issue #10636). Inference below keeps the
	// cancellable ctx, so a disconnect still stops generation.
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(context.WithoutCancel(ctx)))
	ttsModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return "", nil, err
	}

	if ttsModel == nil {
		return "", nil, fmt.Errorf("could not load tts model %q", modelConfig.Model)
	}

	audioDir := filepath.Join(appConfig.GeneratedContentDir, "audio")
	if err := os.MkdirAll(audioDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed creating audio directory: %s", err)
	}

	fileName := utils.GenerateUniqueFileName(audioDir, "tts", ".wav")
	filePath := filepath.Join(audioDir, fileName)

	// We join the model name to the model path here. This seems to only be done for TTS and is HIGHLY suspect.
	// This should be addressed in a follow up PR soon.
	// Copying it over nearly verbatim, as TTS backends are not functional without this.
	modelPath := ""
	// Checking first that it exists and is not outside ModelPath
	// TODO: we should actually first check if the modelFile is looking like
	// a FS path
	mp := filepath.Join(loader.ModelPath, modelConfig.Model)
	if _, err := os.Stat(mp); err == nil {
		if err := utils.VerifyPath(mp, appConfig.SystemState.Model.ModelsPath); err != nil {
			return "", nil, err
		}
		modelPath = mp
	} else {
		modelPath = modelConfig.Model // skip this step if it fails?????
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
	}

	ttsRequest := newTTSRequest(text, modelPath, voice, filePath, language, instructions, params)

	res, err := ttsModel.TTS(ctx, ttsRequest)

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		} else if !res.Success {
			errStr = fmt.Sprintf("TTS error: %s", res.Message)
		}

		data := map[string]any{
			"text":     text,
			"voice":    voice,
			"language": language,
		}
		if err == nil && res.Success {
			if snippet := trace.AudioSnippet(filePath, appConfig.TracingMaxBodyBytes); snippet != nil {
				maps.Copy(data, snippet)
			}
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceTTS,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(text, 200),
			Error:     errStr,
			Data:      data,
		})
	}

	if err != nil {
		return "", nil, err
	}

	// return RPC error if any
	if !res.Success {
		return "", nil, fmt.Errorf("error during TTS: %s", res.Message)
	}

	return filePath, res, err
}

func ModelTTSStream(
	ctx context.Context,
	text,
	voice,
	language,
	instructions string,
	params map[string]string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
	audioCallback func([]byte) error,
) error {
	// See ModelTTS above: WithoutCancel decouples the load from request
	// cancellation while preserving routing values (issue #10636).
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(context.WithoutCancel(ctx)))
	ttsModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return err
	}

	if ttsModel == nil {
		return fmt.Errorf("could not load tts model %q", modelConfig.Model)
	}

	// We join the model name to the model path here. This seems to only be done for TTS and is HIGHLY suspect.
	// This should be addressed in a follow up PR soon.
	// Copying it over nearly verbatim, as TTS backends are not functional without this.
	modelPath := ""
	// Checking first that it exists and is not outside ModelPath
	// TODO: we should actually first check if the modelFile is looking like
	// a FS path
	mp := filepath.Join(loader.ModelPath, modelConfig.Model)
	if _, err := os.Stat(mp); err == nil {
		if err := utils.VerifyPath(mp, appConfig.SystemState.Model.ModelsPath); err != nil {
			return err
		}
		modelPath = mp
	} else {
		modelPath = modelConfig.Model // skip this step if it fails?????
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
	}

	var sampleRate uint32 = 16000 // default
	headerSent := false
	var callbackErr error

	// Collect up to 30s of audio for tracing
	var snippetPCM []byte
	var totalPCMBytes int
	snippetCapped := false

	// Streaming TTS writes to the HTTP response, not a file, so dst is empty.
	ttsRequest := newTTSRequest(text, modelPath, voice, "", language, instructions, params)

	err = ttsModel.TTSStream(ctx, ttsRequest, func(reply *proto.Reply) {
		// First message contains sample rate info
		if !headerSent && len(reply.Message) > 0 {
			var info map[string]any
			if json.Unmarshal(reply.Message, &info) == nil {
				if sr, ok := info["sample_rate"].(float64); ok {
					sampleRate = uint32(sr)
				}
			}
			// Send WAV header with placeholder size (0xFFFFFFFF for streaming)
			header := laudio.WAVHeader{
				ChunkID:       [4]byte{'R', 'I', 'F', 'F'},
				ChunkSize:     0xFFFFFFFF, // Unknown size for streaming
				Format:        [4]byte{'W', 'A', 'V', 'E'},
				Subchunk1ID:   [4]byte{'f', 'm', 't', ' '},
				Subchunk1Size: 16,
				AudioFormat:   1, // PCM
				NumChannels:   1, // Mono
				SampleRate:    sampleRate,
				ByteRate:      sampleRate * 2, // SampleRate * BlockAlign
				BlockAlign:    2,              // 16-bit = 2 bytes
				BitsPerSample: 16,
				Subchunk2ID:   [4]byte{'d', 'a', 't', 'a'},
				Subchunk2Size: 0xFFFFFFFF, // Unknown size for streaming
			}

			var buf bytes.Buffer
			if writeErr := binary.Write(&buf, binary.LittleEndian, header); writeErr != nil {
				callbackErr = writeErr
				return
			}

			if writeErr := audioCallback(buf.Bytes()); writeErr != nil {
				callbackErr = writeErr
				return
			}
			headerSent = true
		}

		// Stream audio chunks
		if len(reply.Audio) > 0 {
			if writeErr := audioCallback(reply.Audio); writeErr != nil {
				callbackErr = writeErr
			}
			// Accumulate PCM for tracing snippet
			totalPCMBytes += len(reply.Audio)
			if appConfig.EnableTracing && !snippetCapped {
				maxBytes := int(sampleRate) * 2 * trace.MaxSnippetSeconds // 16-bit mono
				if len(snippetPCM)+len(reply.Audio) <= maxBytes {
					snippetPCM = append(snippetPCM, reply.Audio...)
				} else {
					remaining := maxBytes - len(snippetPCM)
					if remaining > 0 {
						// Align to sample boundary (2 bytes per sample)
						remaining = remaining &^ 1
						snippetPCM = append(snippetPCM, reply.Audio[:remaining]...)
					}
					snippetCapped = true
				}
			}
		}
	})

	resultErr := err
	if callbackErr != nil {
		resultErr = callbackErr
	}

	if appConfig.EnableTracing {
		errStr := ""
		if resultErr != nil {
			errStr = resultErr.Error()
		}

		data := map[string]any{
			"text":      text,
			"voice":     voice,
			"language":  language,
			"streaming": true,
		}
		if resultErr == nil && len(snippetPCM) > 0 {
			if snippet := trace.AudioSnippetFromPCM(snippetPCM, int(sampleRate), totalPCMBytes, appConfig.TracingMaxBodyBytes); snippet != nil {
				maps.Copy(data, snippet)
			}
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceTTS,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(text, 200),
			Error:     errStr,
			Data:      data,
		})
	}

	if callbackErr != nil {
		return callbackErr
	}
	return err
}
