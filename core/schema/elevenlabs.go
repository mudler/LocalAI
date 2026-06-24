package schema

type ElevenLabsTTSRequest struct {
	Text         string `json:"text" yaml:"text"`
	ModelID      string `json:"model_id" yaml:"model_id"`
	LanguageCode string `json:"language_code" yaml:"language_code"`
}

// DO NOT ADD A SOURCE-AUDIO FIELD TO THIS STRUCT.
//
// SoundGenerationRequest.src on the wire is the input clip for the editing
// routes, and setting it on a stable_audio model CORRUPTS THE HEAP AND ABORTS
// THE PROCESS in the audio-cpp backend's pinned audio.cpp: "free(): invalid
// size" / "munmap_chunk(): invalid pointer", SIGABRT, backend gone. It is
// upstream's bug, not LocalAI's, and it was attributed rather than assumed:
// upstream's own audiocpp_cli, built from the same checkout, aborts identically
// with --audio at exit 134 and completes cleanly without it. The long version
// is on the SoundGeneration handler in backend/cpp/audio-cpp/grpc-server.cpp.
//
// What keeps that off the network today is exactly this omission. The
// ElevenLabs endpoint (core/http/endpoints/elevenlabs/soundgeneration.go)
// passes nil for the source file because this struct has nowhere to put one,
// so the only caller that can set src is core/cli/soundgeneration.go, a local
// CLI. Adding the field here turns a local CLI abort into remotely reachable
// heap corruption with fully attacker-influenced input. Wait for the pin to
// move past the fix.
type ElevenLabsSoundGenerationRequest struct {
	Text        string   `json:"text" yaml:"text"`
	ModelID     string   `json:"model_id" yaml:"model_id"`
	Duration    *float32 `json:"duration_seconds,omitempty" yaml:"duration_seconds,omitempty"`
	Temperature *float32 `json:"prompt_influence,omitempty" yaml:"prompt_influence,omitempty"`
	DoSample    *bool    `json:"do_sample,omitempty" yaml:"do_sample,omitempty"`
	// Advanced mode
	Think         *bool  `json:"think,omitempty" yaml:"think,omitempty"`
	Caption       string `json:"caption,omitempty" yaml:"caption,omitempty"`
	Lyrics        string `json:"lyrics,omitempty" yaml:"lyrics,omitempty"`
	BPM           *int   `json:"bpm,omitempty" yaml:"bpm,omitempty"`
	Keyscale      string `json:"keyscale,omitempty" yaml:"keyscale,omitempty"`
	Language      string `json:"language,omitempty" yaml:"language,omitempty"`
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
