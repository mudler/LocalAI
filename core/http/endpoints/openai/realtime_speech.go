package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/sound"
)

// emitSpeech synthesizes text and sends the audio to the client. When the
// pipeline opts into TTS streaming it forwards each PCM chunk as its own
// response.output_audio.delta as soon as the backend produces it; otherwise it
// synthesizes the whole utterance and sends it as a single delta.
//
// It deliberately does NOT emit transcript or audio-done events: the caller owns
// those so a streamed reply can be split into several spoken segments that share
// one response/item.
//
// It returns the PCM audio (at the session output rate) accumulated across all
// chunks, which the caller base64-encodes onto the conversation item. For WebRTC
// the audio goes over the RTP track instead, so the returned slice is empty.
func emitSpeech(ctx context.Context, t Transport, session *Session, responseID, itemID, text string) ([]byte, error) {
	if text == "" {
		return nil, nil
	}

	_, isWebRTC := t.(*WebRTCTransport)

	var wsAudio []byte // PCM at the session output rate, accumulated for the item record

	// sendChunk hands one PCM buffer to the transport: WebRTC consumes the raw
	// PCM directly (it resamples internally); WebSocket gets base64 PCM at the
	// session output rate via a JSON delta event.
	sendChunk := func(pcm []byte, sampleRate int) error {
		if len(pcm) == 0 {
			return nil
		}
		if err := t.SendAudio(ctx, pcm, sampleRate); err != nil {
			return err
		}
		if isWebRTC {
			return nil
		}
		wsPCM := pcm
		if sampleRate != 0 && sampleRate != session.OutputSampleRate {
			samples := sound.BytesToInt16sLE(pcm)
			resampled := sound.ResampleInt16(samples, sampleRate, session.OutputSampleRate)
			wsPCM = sound.Int16toBytesLE(resampled)
		}
		wsAudio = append(wsAudio, wsPCM...)
		return t.SendEvent(types.ResponseOutputAudioDeltaEvent{
			ServerEventBase: types.ServerEventBase{},
			ResponseID:      responseID,
			ItemID:          itemID,
			OutputIndex:     0,
			ContentIndex:    0,
			Delta:           base64.StdEncoding.EncodeToString(wsPCM),
		})
	}

	language := ""
	if session.InputAudioTranscription != nil {
		language = session.InputAudioTranscription.Language
	}

	if session.ModelConfig != nil && session.ModelConfig.Pipeline.StreamTTS() {
		if err := session.ModelInterface.TTSStream(ctx, text, session.Voice, language, sendChunk); err != nil {
			return nil, err
		}
		return wsAudio, nil
	}

	// Unary fallback: synthesize the whole utterance to a file, then emit once.
	audioFilePath, res, err := session.ModelInterface.TTS(ctx, text, session.Voice, language)
	if err != nil {
		return nil, err
	}
	if res != nil && !res.Success {
		return nil, fmt.Errorf("tts generation failed: %s", res.Message)
	}
	defer func() { _ = os.Remove(audioFilePath) }()

	audioBytes, err := os.ReadFile(audioFilePath)
	if err != nil {
		return nil, fmt.Errorf("read tts audio: %w", err)
	}
	pcm, sampleRate := laudio.ParseWAV(audioBytes)
	if sampleRate == 0 {
		sampleRate = session.OutputSampleRate
	}
	if err := sendChunk(pcm, sampleRate); err != nil {
		return nil, err
	}
	return wsAudio, nil
}
