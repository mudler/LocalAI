package schema

import "fmt"

// CambAI TTS streaming request (POST /apis/tts-stream)
type CambAITTSStreamRequest struct {
	Text                string                        `json:"text"`
	VoiceID             int                           `json:"voice_id"`
	Language            string                        `json:"language"`
	SpeechModel         string                        `json:"speech_model"`
	OutputConfiguration *CambAIOutputConfiguration    `json:"output_configuration,omitempty"`
	InferenceOptions    *CambAITTSInferenceOptions    `json:"inference_options,omitempty"`
}

type CambAIOutputConfiguration struct {
	Format     string `json:"format,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
}

type CambAITTSInferenceOptions struct {
	Speed       *float32 `json:"speed,omitempty"`
	Pitch       *float32 `json:"pitch,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
}

func (r *CambAITTSStreamRequest) ModelName(s *string) string {
	if s != nil {
		r.SpeechModel = *s
	}
	return r.SpeechModel
}

// CambAI async TTS request (POST /apis/tts)
type CambAITTSRequest struct {
	Text       string `json:"text"`
	VoiceID    int    `json:"voice_id"`
	LanguageID int    `json:"language"`
	Model      string `json:"model,omitempty"`
}

func (r *CambAITTSRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// CambAI translated TTS request (POST /apis/translated-tts)
type CambAITranslatedTTSRequest struct {
	Text             string `json:"text"`
	VoiceID          int    `json:"voice_id"`
	SourceLanguageID int    `json:"source_language"`
	TargetLanguageID int    `json:"target_language"`
	Model            string `json:"model,omitempty"`
}

func (r *CambAITranslatedTTSRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// CambAI translation request (POST /apis/translate)
type CambAITranslationRequest struct {
	Texts            []string `json:"texts"`
	SourceLanguageID int      `json:"source_language"`
	TargetLanguageID int      `json:"target_language"`
	Model            string   `json:"model,omitempty"`
}

func (r *CambAITranslationRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// CambAI translation stream request (POST /apis/translation/stream)
type CambAITranslationStreamRequest struct {
	Text             string `json:"text"`
	SourceLanguageID int    `json:"source_language"`
	TargetLanguageID int    `json:"target_language"`
	Model            string `json:"model,omitempty"`
}

func (r *CambAITranslationStreamRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// CambAI transcription request (POST /apis/transcribe)
type CambAITranscriptionRequest struct {
	LanguageID int    `json:"language,omitempty"`
	MediaURL   string `json:"media_url,omitempty"`
	Model      string `json:"model,omitempty"`
}

func (r *CambAITranscriptionRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// CambAI text-to-sound request (POST /apis/text-to-sound)
type CambAITextToSoundRequest struct {
	Prompt   string   `json:"prompt"`
	Duration *float32 `json:"duration,omitempty"`
	Model    string   `json:"model,omitempty"`
}

func (r *CambAITextToSoundRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// CambAI create custom voice request (POST /apis/create-custom-voice)
type CambAICreateCustomVoiceRequest struct {
	VoiceName string `json:"voice_name"`
	Model     string `json:"model,omitempty"`
}

func (r *CambAICreateCustomVoiceRequest) ModelName(s *string) string {
	if s != nil {
		r.Model = *s
	}
	return r.Model
}

// Response types

type CambAITaskResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
}

type CambAITaskStatusResponse struct {
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
	Output any    `json:"output,omitempty"`
}

type CambAIVoice struct {
	ID     int    `json:"id"`
	Name   string `json:"voice_name"`
	Gender string `json:"gender,omitempty"`
	Age    string `json:"age,omitempty"`
}

type CambAIListVoicesResponse struct {
	Voices []CambAIVoice `json:"voices"`
}

type CambAICreateCustomVoiceResponse struct {
	VoiceID int `json:"voice_id"`
}

type CambAIErrorResponse struct {
	Detail string `json:"detail"`
}

type CambAITranslationResponse struct {
	Translation []string `json:"translation"`
	SourceLang  int      `json:"source_language"`
	TargetLang  int      `json:"target_language"`
}

type CambAITranscriptionResponse struct {
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
}

// CambAILanguageIDToCode maps CAMB AI integer language IDs to BCP-47 codes.
// This is a subset covering the most common languages.
var CambAILanguageIDToCode = map[int]string{
	1:   "en",
	2:   "ko",
	3:   "nl",
	4:   "tr",
	5:   "uk",
	6:   "pl",
	7:   "ta",
	8:   "vi",
	9:   "sv",
	10:  "id",
	11:  "ms",
	12:  "ja",
	13:  "zh",
	14:  "bn",
	15:  "th",
	16:  "tl",
	17:  "he",
	18:  "pt-br",
	19:  "pt",
	20:  "ru",
	21:  "ca",
	22:  "te",
	23:  "ml",
	24:  "kn",
	25:  "gu",
	26:  "mr",
	27:  "hi",
	28:  "da",
	29:  "fi",
	30:  "no",
	31:  "hu",
	32:  "sk",
	33:  "cs",
	34:  "el",
	35:  "ro",
	36:  "bg",
	37:  "sr",
	38:  "hr",
	39:  "sl",
	40:  "mk",
	41:  "et",
	42:  "lt",
	43:  "lv",
	44:  "sw",
	45:  "ar",
	46:  "ur",
	47:  "fa",
	48:  "af",
	49:  "my",
	50:  "bs",
	51:  "si",
	52:  "ne",
	53:  "km",
	54:  "es",
	55:  "cy",
	56:  "is",
	57:  "pa",
	58:  "as",
	59:  "ga",
	60:  "am",
	61:  "az",
	62:  "uz",
	63:  "ka",
	64:  "sq",
	65:  "mn",
	66:  "la",
	67:  "gl",
	68:  "eu",
	69:  "it",
	70:  "de",
	71:  "nn",
	72:  "lo",
	73:  "yo",
	74:  "ig",
	75:  "ha",
	76:  "fr",
	77:  "zu",
	78:  "xh",
	79:  "so",
	80:  "mt",
	81:  "eo",
	82:  "jw",
	83:  "su",
	84:  "ps",
	85:  "sd",
	86:  "mg",
	87:  "hy",
	88:  "lb",
	89:  "be",
	90:  "tt",
	91:  "tg",
	92:  "ky",
	93:  "tk",
	94:  "ha",
	95:  "sn",
	96:  "ln",
	97:  "rw",
	98:  "ny",
	99:  "ts",
	100: "tn",
	101: "st",
	102: "ss",
	103: "nd",
	104: "ve",
}

// CambAILanguageCodeFromID converts a CAMB AI language ID to a BCP-47 code.
func CambAILanguageCodeFromID(id int) string {
	if code, ok := CambAILanguageIDToCode[id]; ok {
		return code
	}
	return fmt.Sprintf("lang-%d", id)
}
