package schema

import "time"

type TranscriptionSegment struct {
	Id      int                 `json:"id"`
	Start   time.Duration       `json:"start"`
	End     time.Duration       `json:"end"`
	Text    string              `json:"text"`
	Tokens  []int               `json:"tokens"`
	Speaker string              `json:"speaker,omitempty"`
	Words   []TranscriptionWord `json:"words,omitempty"`
}

type TranscriptionWord struct {
	Start time.Duration `json:"start"`
	End   time.Duration `json:"end"`
	Text  string        `json:"text"`
}

type TranscriptionResult struct {
	Segments []TranscriptionSegment `json:"segments,omitempty"`
	Words    []TranscriptionWord    `json:"words,omitempty"`
	Text     string                 `json:"text"`
	Language string                 `json:"language,omitempty"`
	Duration float64                `json:"duration,omitempty"`
	// Eou reports that the decode ended on the model's end-of-utterance
	// special token (emitted by streaming-EOU models such as
	// parakeet_realtime_eou_120m-v1; always false elsewhere). The marker
	// itself never appears in Text.
	Eou bool `json:"eou,omitempty"`
}

type TranscriptionSegmentSeconds struct {
	Id      int                        `json:"id"`
	Start   float64                    `json:"start"`
	End     float64                    `json:"end"`
	Text    string                     `json:"text"`
	Tokens  []int                      `json:"tokens"`
	Speaker string                     `json:"speaker,omitempty"`
	Words   []TranscriptionWordSeconds `json:"words,omitempty"`
}

type TranscriptionWordSeconds struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type TranscriptionResultSeconds struct {
	Segments []TranscriptionSegmentSeconds `json:"segments,omitempty"`
	Words    []TranscriptionWordSeconds    `json:"words,omitempty"`
	Text     string                        `json:"text"`
	Language string                        `json:"language,omitempty"`
	Duration float64                       `json:"duration,omitempty"`
}
