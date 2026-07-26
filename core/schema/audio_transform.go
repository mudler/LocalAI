package schema

// @Description Audio transform request body — multipart form-data only.
// `audio` (the primary input file) is required; `reference` (auxiliary
// signal: loopback for echo cancellation, target speaker for voice
// conversion, etc.) is optional. Backend-specific tuning lives in the
// `params[<key>]=<value>` form fields, collected into a generic map so
// the schema doesn't bake in any one transform's vocabulary.
//
// The `form` tags on the two snake_case fields are load bearing. This request
// arrives as multipart/form-data, and echo's binder falls back to the FIELD
// NAME when a form tag is missing, matching it only case-insensitively:
// "Format" never matches "response_format" and "SampleRate" never matches
// "sample_rate", so both documented form fields were silently ignored until
// these tags were added. `model` worked all along because its field name and
// its form key differ only in case.
type AudioTransformRequest struct {
	BasicModelRequest
	Format     string            `json:"response_format,omitempty" yaml:"response_format,omitempty" form:"response_format"` // wav | mp3 | ogg | flac
	SampleRate int               `json:"sample_rate,omitempty" yaml:"sample_rate,omitempty" form:"sample_rate"`             // desired output sample rate; 0 = backend default
	Params     map[string]string `json:"params,omitempty" yaml:"params,omitempty"`                                         // backend-specific tuning
}

// AudioTransformStreamControl is the JSON envelope used on the
// /audio/transformations/stream WebSocket. The first frame on a new
// connection MUST be a session.update; subsequent frames are binary PCM.
// Server may emit error / session.closed text frames.
type AudioTransformStreamControl struct {
	Type         string            `json:"type"`
	Model        string            `json:"model,omitempty"`
	SampleFormat string            `json:"sample_format,omitempty"`
	SampleRate   int               `json:"sample_rate,omitempty"`
	FrameSamples int               `json:"frame_samples,omitempty"`
	Params       map[string]string `json:"params,omitempty"`
	Reset        bool              `json:"reset,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// AudioTransformStreamControl Type values.
const (
	AudioTransformCtrlSessionUpdate = "session.update"
	AudioTransformCtrlSessionClose  = "session.close"
	AudioTransformCtrlSessionClosed = "session.closed"
	AudioTransformCtrlError         = "error"
)

// AudioTransformStreamControl SampleFormat values (mirror the proto enum
// names so the wire format stays self-describing).
const (
	AudioTransformSampleFormatS16LE = "S16_LE"
	AudioTransformSampleFormatF32LE = "F32_LE"
)

// LocalVQE param keys — backend-specific but referenced by both the
// HTTP layer (form-field shortcuts, defaults) and the localvqe backend
// itself. Hoisted so renames stay in lockstep.
const (
	AudioTransformParamNoiseGate          = "noise_gate"
	AudioTransformParamNoiseGateThreshold = "noise_gate_threshold_dbfs"
)
