package schema

import "time"

type WhisperSegment struct {
	Id     int           `json:"id"`
	Start  time.Duration `json:"start"`
	End    time.Duration `json:"end"`
	Text   string        `json:"text"`
	Tokens []int         `json:"tokens"`
}

type WhisperResult struct {
	Segments []WhisperSegment `json:"segments"`
	Text     string           `json:"text"`
}
