package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

// defaultSoundDetectionTopK is the number of scored tags requested per
// committed utterance when the session does not pin its own top_k.
const defaultSoundDetectionTopK = 5

// emitSoundDetection classifies a committed utterance into sound-event tags and
// emits a conversation.item.sound_detection event for it. It mirrors
// emitTranscription's unary path: it calls the session's sound-event
// classifier, maps the scored tags onto the server event, and sends it over
// the transport. Sound detection is additive to transcription: its result is
// emitted independently and a failure here is the caller's to log, never a
// reason to abort the turn.
func emitSoundDetection(ctx context.Context, t Transport, session *Session, itemID, audioPath string) error {
	topK := session.SoundDetectionTopK
	if topK <= 0 {
		topK = defaultSoundDetectionTopK
	}

	result, err := session.ModelInterface.SoundDetection(ctx, audioPath, topK, session.SoundDetectionThreshold)
	if err != nil {
		return err
	}

	detections := make([]types.SoundDetectionTag, 0)
	if result != nil {
		for _, d := range result.Detections {
			detections = append(detections, types.SoundDetectionTag{
				Label: d.Label,
				Score: d.Score,
				Index: d.Index,
			})
		}
	}

	return t.SendEvent(types.ConversationItemSoundDetectionEvent{
		ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
		ItemID:          itemID,
		ContentIndex:    0,
		Detections:      detections,
	})
}
