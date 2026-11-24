package schema

// @Description Audio transform request body — multipart form-data only.
// `audio` (the primary input file) is required; `reference` (auxiliary
// signal: loopback for echo cancellation, target speaker for voice
// conversion, etc.) is optional. Backend-specific tuning lives in the
// `params[<key>]=<value>` form fields, collected into a generic map so
// the schema doesn't bake in any one transform's vocabulary.
//
// The `form` tags on the two snake_case fields are load bearing, and NOT for
// the reason first written here. echo's binder has no field-name fallback at
// all: bindData is documented as binding "ONLY fields in destination struct
// that have EXPLICIT tag", and a field whose tag is empty and whose kind is not
// a struct is skipped outright with a `continue`
// (labstack/echo/v4@v4.15.1 bind.go). So an untagged Format or SampleRate is
// not matched loosely, it is not looked at, which is why both documented form
// fields were silently ignored until these tags were added.
//
// `model` is not the counter-example it looks like. It is not bound by the
// binder either: it arrives because setModelNameFromRequest asks for it by
// name, c.FormValue("model") (core/http/middleware/request.go).
//
// SampleRate is validated in the handler, not here: it is interpolated into
// ffmpeg's -ar and an unbounded value is a disk-exhaustion hazard. See
// minAudioTransformSampleRate in
// core/http/endpoints/localai/audio_transform.go.
type AudioTransformRequest struct {
	BasicModelRequest
	Format     string            `json:"response_format,omitempty" yaml:"response_format,omitempty" form:"response_format"` // wav | mp3 | ogg | flac
	SampleRate int               `json:"sample_rate,omitempty" yaml:"sample_rate,omitempty" form:"sample_rate"`             // desired output sample rate; 0 = backend default, otherwise 8000..192000
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
