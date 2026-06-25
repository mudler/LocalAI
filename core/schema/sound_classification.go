package schema

// SoundClassification is one scored sound-event tag. Score is the
// per-class probability (multi-label, independent), Index is the class
// index in the model ontology, and Label is the human-readable AudioSet
// class name (e.g. "Baby cry, infant cry").
type SoundClassification struct {
	Index int     `json:"index"`
	Label string  `json:"label"`
	Score float32 `json:"score"`
}

// SoundClassificationResult is the JSON response of the
// /v1/audio/classification endpoint: the model name and the scored tags
// in score-descending order.
type SoundClassificationResult struct {
	Model      string                `json:"model"`
	Detections []SoundClassification `json:"detections"`
}
