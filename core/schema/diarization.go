package schema

// DiarizationSegment is one continuous span of speech attributed to a
// single speaker. Times are in seconds. Speaker is the normalized label
// (SPEAKER_NN, zero-padded, stable across segments); Label preserves the
// raw backend-emitted identifier for clients that already track their
// own speaker dictionary.
type DiarizationSegment struct {
	Id      int     `json:"id"`
	Speaker string  `json:"speaker"`
	Label   string  `json:"label,omitempty"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text,omitempty"`
}

// DiarizationSpeaker summarizes one speaker across the whole audio so
// clients can build per-speaker UIs (timeline strips, talk-time charts)
// without re-aggregating the segment list.
type DiarizationSpeaker struct {
	Id                  string  `json:"id"`
	Label               string  `json:"label,omitempty"`
	TotalSpeechDuration float64 `json:"total_speech_duration"`
	SegmentCount        int     `json:"segment_count"`
}

// DiarizationResult is the JSON payload returned by /v1/audio/diarization.
// Speakers and segment text are omitted when empty so the default `json`
// response stays minimal; verbose_json keeps both populated.
type DiarizationResult struct {
	Task        string               `json:"task"`
	Duration    float64              `json:"duration,omitempty"`
	Language    string               `json:"language,omitempty"`
	NumSpeakers int                  `json:"num_speakers"`
	Segments    []DiarizationSegment `json:"segments"`
	Speakers    []DiarizationSpeaker `json:"speakers,omitempty"`
}

// DiarizationResponseFormatType mirrors transcription's response_format
// pattern: json (default, no per-segment text), verbose_json (adds
// speakers summary + text when available), and rttm (NIST RTTM rows).
type DiarizationResponseFormatType string

const (
	DiarizationResponseFormatJson        DiarizationResponseFormatType = "json"
	DiarizationResponseFormatJsonVerbose DiarizationResponseFormatType = "verbose_json"
	DiarizationResponseFormatRTTM        DiarizationResponseFormatType = "rttm"
)
