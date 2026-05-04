package backend

import (
	"testing"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

func TestDiarizationResultFromProto_NormalisesSpeakerLabels(t *testing.T) {
	in := &proto.DiarizeResponse{
		Duration: 10.5,
		Language: "en",
		Segments: []*proto.DiarizeSegment{
			{Start: 0.0, End: 1.0, Speaker: "5", Text: "hi"},
			{Start: 1.0, End: 2.0, Speaker: "2"},
			{Start: 2.0, End: 3.5, Speaker: "5"},
			{Start: 3.5, End: 4.0, Speaker: ""}, // empty → coerced to "0"
		},
	}

	got := diarizationResultFromProto(in)

	if got.Task != "diarize" {
		t.Errorf("task = %q, want diarize", got.Task)
	}
	if got.NumSpeakers != 3 {
		t.Errorf("num_speakers = %d, want 3 (5, 2, 0)", got.NumSpeakers)
	}
	if got.Duration != 10.5 || got.Language != "en" {
		t.Errorf("metadata not preserved: duration=%v language=%q", got.Duration, got.Language)
	}
	if len(got.Segments) != 4 {
		t.Fatalf("segments=%d, want 4", len(got.Segments))
	}
	// First-seen-order normalization: "5"→SPEAKER_00, "2"→SPEAKER_01, ""→SPEAKER_02
	want := []struct {
		speaker string
		label   string
	}{
		{"SPEAKER_00", "5"},
		{"SPEAKER_01", "2"},
		{"SPEAKER_00", "5"},
		{"SPEAKER_02", "0"},
	}
	for i, w := range want {
		if got.Segments[i].Speaker != w.speaker {
			t.Errorf("seg[%d].speaker = %q, want %q", i, got.Segments[i].Speaker, w.speaker)
		}
		if got.Segments[i].Label != w.label {
			t.Errorf("seg[%d].label = %q, want %q", i, got.Segments[i].Label, w.label)
		}
	}

	// Per-speaker totals reflect cumulative speech duration and segment count.
	if len(got.Speakers) != 3 {
		t.Fatalf("speakers=%d, want 3", len(got.Speakers))
	}
	byID := map[string]float64{}
	countByID := map[string]int{}
	for _, sp := range got.Speakers {
		byID[sp.Id] = sp.TotalSpeechDuration
		countByID[sp.Id] = sp.SegmentCount
	}
	if byID["SPEAKER_00"] != 2.5 { // 1.0 + 1.5
		t.Errorf("SPEAKER_00 total = %v, want 2.5", byID["SPEAKER_00"])
	}
	if byID["SPEAKER_01"] != 1.0 {
		t.Errorf("SPEAKER_01 total = %v, want 1.0", byID["SPEAKER_01"])
	}
	if countByID["SPEAKER_00"] != 2 || countByID["SPEAKER_01"] != 1 || countByID["SPEAKER_02"] != 1 {
		t.Errorf("segment counts wrong: %v", countByID)
	}
}

func TestDiarizationResultFromProto_NilSafe(t *testing.T) {
	got := diarizationResultFromProto(nil)
	if got == nil || got.Segments == nil {
		t.Fatalf("nil input must return a non-nil result with a non-nil segments slice")
	}
	if len(got.Segments) != 0 {
		t.Errorf("nil input segments = %d, want 0", len(got.Segments))
	}
}

func TestDiarizationResultFromProto_EmptySegmentsKeepsBackendSpeakerCount(t *testing.T) {
	// When the backend reports a non-zero NumSpeakers but no segments
	// (early stop, silence-only audio after VAD trim, etc.), the result
	// should still surface the backend's count rather than zero.
	in := &proto.DiarizeResponse{NumSpeakers: 2, Duration: 5}
	got := diarizationResultFromProto(in)
	if got.NumSpeakers != 2 {
		t.Errorf("num_speakers = %d, want 2", got.NumSpeakers)
	}
	if len(got.Segments) != 0 {
		t.Errorf("expected no segments, got %d", len(got.Segments))
	}
}
