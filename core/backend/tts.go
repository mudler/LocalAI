package backend

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	laudio "github.com/mudler/LocalAI/pkg/audio"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

func ModelTTS(
	text,
	voice,
	language string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (string, *proto.Result, error) {
	opts := ModelOptions(modelConfig, appConfig)
	ttsModel, err := loader.Load(opts...)
	if err != nil {
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

	res, err := ttsModel.TTS(context.Background(), &proto.TTSRequest{
		Text:     text,
		Model:    modelPath,
		Voice:    voice,
		Dst:      filePath,
		Language: &language,
	})
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
	text,
	voice,
	language string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
	audioCallback func([]byte) error,
) error {
	opts := ModelOptions(modelConfig, appConfig)
	ttsModel, err := loader.Load(opts...)
	if err != nil {
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

	var sampleRate uint32 = 16000 // default
	headerSent := false
	var callbackErr error

	err = ttsModel.TTSStream(context.Background(), &proto.TTSRequest{
		Text:     text,
		Model:    modelPath,
		Voice:    voice,
		Language: &language,
	}, func(reply *proto.Reply) {
		// First message contains sample rate info
		if !headerSent && len(reply.Message) > 0 {
			var info map[string]interface{}
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
		}
	})

	if callbackErr != nil {
		return callbackErr
	}
	return err
}
