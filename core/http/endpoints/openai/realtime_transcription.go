package openai

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

// emitPrecomputedTranscription emits the transcription events for a turn
// whose transcript already exists (semantic_vad's live stream, or the
// retranscribe gate's batch decode): optional delta replays followed by the
// completed event — the same contract emitTranscription produces, sharing
// one itemID — without running the backend again.
func emitPrecomputedTranscription(t Transport, itemID string, deltas []string, transcript string) error {
	for _, d := range deltas {
		if d == "" {
			continue
		}
		if err := t.SendEvent(types.ConversationItemInputAudioTranscriptionDeltaEvent{
			ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
			ItemID:          itemID,
			ContentIndex:    0,
			Delta:           d,
		}); err != nil {
			return err
		}
	}
	return t.SendEvent(types.ConversationItemInputAudioTranscriptionCompletedEvent{
		ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
		ItemID:          itemID,
		ContentIndex:    0,
		Transcript:      transcript,
	})
}

// emitTranscription transcribes a committed utterance and emits the transcription
// events for it, returning the final transcript text. With
// pipeline.streaming.transcription enabled it streams each transcript fragment as
// a conversation.item.input_audio_transcription.delta as the backend produces it,
// then a completed event; otherwise it transcribes the whole utterance and emits
// a single completed event. delta and completed events share itemID.
func emitTranscription(ctx context.Context, t Transport, session *Session, itemID, audioPath string) (string, error) {
	cfg := session.InputAudioTranscription
	diarize := session.ModelConfig != nil && session.ModelConfig.Pipeline.Diarization

	if session.ModelConfig != nil && session.ModelConfig.Pipeline.StreamTranscription() {
		final, err := session.ModelInterface.TranscribeStream(ctx, audioPath, cfg.Language, false, diarize, cfg.Prompt, func(delta string) {
			_ = t.SendEvent(types.ConversationItemInputAudioTranscriptionDeltaEvent{
				ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
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
			if diarize {
				if err := emitSpeakerSegments(t, itemID, final); err != nil {
					return "", err
				}
			}
		}
		if err := t.SendEvent(types.ConversationItemInputAudioTranscriptionCompletedEvent{
			ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
			ItemID:          itemID,
			ContentIndex:    0,
			Transcript:      transcript,
		}); err != nil {
			return "", err
		}
		return transcript, nil
	}

	// Unary fallback: transcribe the whole utterance, emit one completed event.
	tr, err := session.ModelInterface.Transcribe(ctx, audioPath, cfg.Language, false, diarize, cfg.Prompt)
	if err != nil {
		return "", err
	}
	if tr == nil {
		return "", fmt.Errorf("transcribe result is nil")
	}
	if diarize {
		if err := emitSpeakerSegments(t, itemID, tr); err != nil {
			return "", err
		}
	}
	if err := t.SendEvent(types.ConversationItemInputAudioTranscriptionCompletedEvent{
		ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
		ItemID:          itemID,
		ContentIndex:    0,
		Transcript:      tr.Text,
	}); err != nil {
		return "", err
	}
	return tr.Text, nil
}

// emitSpeakerSegments forwards each speaker-labelled segment of a committed
// turn's transcript as a conversation.item.input_audio_transcription.segment
// event (pipeline.diarization), before the turn's completed event. Times are
// relative to the turn's audio and speaker labels are only consistent within
// the turn, as on the live path. Segments without a speaker are skipped.
func emitSpeakerSegments(t Transport, itemID string, tr *schema.TranscriptionResult) error {
	for _, seg := range tr.Segments {
		if seg.Speaker == "" {
			continue
		}
		if err := t.SendEvent(types.ConversationItemInputAudioTranscriptionSegmentEvent{
			ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
			ItemID:          itemID,
			ContentIndex:    0,
			ID:              fmt.Sprintf("seg_%d", seg.Id),
			Speaker:         seg.Speaker,
			Start:           seg.Start.Seconds(),
			End:             seg.End.Seconds(),
			Text:            seg.Text,
		}); err != nil {
			return err
		}
	}
	return nil
}
