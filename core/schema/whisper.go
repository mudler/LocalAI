package schema

import "time"

type Segment struct {
	Id     int           `json:"id"`
	Start  time.Duration `json:"start"`
	End    time.Duration `json:"end"`
	Text   string        `json:"text"`
	Tokens []int         `json:"tokens"`
}

type Result struct {
	Segments []Segment `json:"segments"`
	Text     string    `json:"text"`
}
