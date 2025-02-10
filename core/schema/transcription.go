package schema

import "time"

type TranscriptionSegment struct {
	Id     int           `json:"id"`
	Start  time.Duration `json:"start"`
	End    time.Duration `json:"end"`
	Text   string        `json:"text"`
	Tokens []int         `json:"tokens"`
}

type TranscriptionResult struct {
	Segments []TranscriptionSegment `json:"segments"`
	Text     string                 `json:"text"`
}
