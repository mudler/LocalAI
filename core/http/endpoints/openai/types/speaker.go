package types

// Speaker is the recognized speaker for a committed audio turn. It is a LocalAI
// extension to the OpenAI Realtime schema, carried on the user conversation item
// and surfaced via the conversation.item.speaker event. Confidence is a 0..100
// score relative to the match threshold (same formula as /v1/voice/identify).
type Speaker struct {
	Name       string  `json:"name,omitempty"`
	ID         string  `json:"id,omitempty"`
	Confidence float32 `json:"confidence"`
	Distance   float32 `json:"distance"`
	Matched    bool    `json:"matched"`
}
