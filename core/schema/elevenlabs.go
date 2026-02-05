package schema

type ElevenLabsTTSRequest struct {
	Text         string `json:"text" yaml:"text"`
	ModelID      string `json:"model_id" yaml:"model_id"`
	LanguageCode string `json:"language_code" yaml:"language_code"`
}

type ElevenLabsSoundGenerationRequest struct {
	Text        string   `json:"text" yaml:"text"`
	ModelID     string   `json:"model_id" yaml:"model_id"`
	Duration    *float32 `json:"duration_seconds,omitempty" yaml:"duration_seconds,omitempty"`
	Temperature *float32 `json:"prompt_influence,omitempty" yaml:"prompt_influence,omitempty"`
	DoSample    *bool    `json:"do_sample,omitempty" yaml:"do_sample,omitempty"`
	// Advanced mode
	Think        *bool  `json:"think,omitempty" yaml:"think,omitempty"`
	Caption      string `json:"caption,omitempty" yaml:"caption,omitempty"`
	Lyrics       string `json:"lyrics,omitempty" yaml:"lyrics,omitempty"`
	BPM          *int   `json:"bpm,omitempty" yaml:"bpm,omitempty"`
	Keyscale     string `json:"keyscale,omitempty" yaml:"keyscale,omitempty"`
	Language     string `json:"language,omitempty" yaml:"language,omitempty"`
	VocalLanguage string `json:"vocal_language,omitempty" yaml:"vocal_language,omitempty"`
	Timesignature string `json:"timesignature,omitempty" yaml:"timesignature,omitempty"`
	// Simple mode: use text as description; optional instrumental / vocal_language
	Instrumental *bool `json:"instrumental,omitempty" yaml:"instrumental,omitempty"`
}

func (elttsr *ElevenLabsTTSRequest) ModelName(s *string) string {
	if s != nil {
		elttsr.ModelID = *s
	}
	return elttsr.ModelID
}

func (elsgr *ElevenLabsSoundGenerationRequest) ModelName(s *string) string {
	if s != nil {
		elsgr.ModelID = *s
	}
	return elsgr.ModelID
}
