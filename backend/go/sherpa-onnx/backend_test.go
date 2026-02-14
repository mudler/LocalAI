package main

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

func TestSherpaBackendStruct(t *testing.T) {
	b := &SherpaBackend{}
	if b.Locking() {
		t.Fatal("new backend should not be locking")
	}
}

func TestLoadNonExistentModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sherpa-test-nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	nonExistentModel := filepath.Join(tmpDir, "non-existent-model.onnx")
	backend := &SherpaBackend{}

	err = backend.Load(&pb.ModelOptions{
		ModelFile: nonExistentModel,
	})

	if err == nil {
		t.Fatal("Expected error when loading non-existent model, but got nil")
	}
}

func TestTTSWithoutLoadingModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sherpa-test-tts")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	outputFile := filepath.Join(tmpDir, "output.wav")
	backend := &SherpaBackend{}

	err = backend.TTS(&pb.TTSRequest{
		Text: "This should fail because no model is loaded",
		Dst:  outputFile,
	})

	if err == nil {
		t.Fatal("Expected error when calling TTS without loading model first, but got nil")
	}
}

func TestASRWithoutLoadingModel(t *testing.T) {
	backend := &SherpaBackend{}

	_, err := backend.AudioTranscription(&pb.TranscriptRequest{
		Dst: "/tmp/nonexistent.wav",
	})

	if err == nil {
		t.Fatal("Expected error when calling AudioTranscription without loading model first")
	}
}

func TestIsASRType(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"asr", true},
		{"ASR", true},
		{"Asr", true},
		{"transcription", true},
		{"Transcription", true},
		{"transcribe", true},
		{"Transcribe", true},
		{"tts", false},
		{"", false},
		{"other", false},
	}

	for _, tt := range tests {
		if got := isASRType(tt.input); got != tt.expected {
			t.Errorf("isASRType(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestLoadASRNonExistentModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sherpa-test-asr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	backend := &SherpaBackend{}
	err = backend.Load(&pb.ModelOptions{
		ModelFile: filepath.Join(tmpDir, "model.onnx"),
		Type:      "asr",
	})

	if err == nil {
		t.Fatal("Expected error when loading non-existent ASR model")
	}
}

func TestLoadDispatchesByType(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sherpa-test-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	modelFile := filepath.Join(tmpDir, "model.onnx")

	// TTS type (default) should fail but attempt TTS loading
	backend := &SherpaBackend{}
	err = backend.Load(&pb.ModelOptions{
		ModelFile: modelFile,
	})
	if err == nil {
		t.Fatal("Expected error for non-existent TTS model")
	}

	// ASR type should fail but attempt ASR loading
	backend = &SherpaBackend{}
	err = backend.Load(&pb.ModelOptions{
		ModelFile: modelFile,
		Type:      "asr",
	})
	if err == nil {
		t.Fatal("Expected error for non-existent ASR model")
	}
}
