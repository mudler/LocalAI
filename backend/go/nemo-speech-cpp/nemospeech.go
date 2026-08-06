package main

import (
	"sync"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// family is the model family selected at load time from the GGUF architecture.
type family int

const (
	familyUnknown family = iota
	familyASR
	familyDiarization
	familyTTS
	familyNMT
)

func (f family) String() string {
	switch f {
	case familyASR:
		return "asr"
	case familyDiarization:
		return "diarization"
	case familyTTS:
		return "tts"
	case familyNMT:
		return "nmt"
	}
	return "unknown"
}

// NemoSpeech is one loaded model. Exactly one of the handles is non-zero,
// matching fam.
type NemoSpeech struct {
	base.SingleThread

	fam  family
	opts loadOptions

	// engineMu serializes calls into the C runtime for this model.
	engineMu sync.Mutex

	recognizer uintptr
	diarizer   uintptr
	synth      uintptr
	translator uintptr
}

func (n *NemoSpeech) Load(opts *pb.ModelOptions) error {
	return nil // Task 5 implements this
}
