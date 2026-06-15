package openai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

// emitTranscription transcribes a committed utterance and emits the transcription
// events for it, returning the final transcript text. With
// pipeline.streaming.transcription enabled it streams each transcript fragment as
// a conversation.item.input_audio_transcription.delta as the backend produces it,
// then a completed event; otherwise it transcribes the whole utterance and emits
// a single completed event. delta and completed events share itemID.
func emitTranscription(ctx context.Context, t Transport, session *Session, itemID, audioPath string) (string, error) {
	cfg := session.InputAudioTranscription

	if session.ModelConfig != nil && session.ModelConfig.Pipeline.StreamTranscription() {
		final, err := session.ModelInterface.TranscribeStream(ctx, audioPath, cfg.Language, false, false, cfg.Prompt, func(delta string) {
			_ = t.SendEvent(types.ConversationItemInputAudioTranscriptionDeltaEvent{
				ServerEventBase: types.ServerEventBase{EventID: uuid.New().String()},
				ItemID:          itemID,
				ContentIndex:    0,
				Delta:           delta,
			})
		})
		if err != nil {
			return "", err
		}
		transcript := ""
		if final != nil {
			transcript = final.Text
		}
		if err := t.SendEvent(types.ConversationItemInputAudioTranscriptionCompletedEvent{
			ServerEventBase: types.ServerEventBase{EventID: uuid.New().String()},
			ItemID:          itemID,
			ContentIndex:    0,
			Transcript:      transcript,
		}); err != nil {
			return "", err
		}
		return transcript, nil
	}

	// Unary fallback: transcribe the whole utterance, emit one completed event.
	tr, err := session.ModelInterface.Transcribe(ctx, audioPath, cfg.Language, false, false, cfg.Prompt)
	if err != nil {
		return "", err
	}
	if tr == nil {
		return "", fmt.Errorf("transcribe result is nil")
	}
	if err := t.SendEvent(types.ConversationItemInputAudioTranscriptionCompletedEvent{
		ServerEventBase: types.ServerEventBase{EventID: uuid.New().String()},
		ItemID:          itemID,
		ContentIndex:    0,
		Transcript:      tr.Text,
	}); err != nil {
		return "", err
	}
	return tr.Text, nil
}
