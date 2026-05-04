package openai

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/schema"
)

func TestRenderRTTM_FormatsSegmentsAsNISTRows(t *testing.T) {
	r := &schema.DiarizationResult{
		Segments: []schema.DiarizationSegment{
			{Id: 0, Speaker: "SPEAKER_00", Start: 0, End: 2.34},
			{Id: 1, Speaker: "SPEAKER_01", Start: 2.34, End: 4.10},
		},
	}
	out := renderRTTM(r, "/tmp/uploads/meeting.wav")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 RTTM rows, got %d:\n%s", len(lines), out)
	}

	// File ID should be the basename without extension; durations are
	// (end - start) with millisecond precision.
	wantPrefix := "SPEAKER meeting 1 "
	if !strings.HasPrefix(lines[0], wantPrefix) {
		t.Errorf("row 0 prefix = %q, want %q…", lines[0][:min(len(lines[0]), len(wantPrefix))], wantPrefix)
	}

	if !strings.Contains(lines[0], " 0.000 2.340 <NA> <NA> SPEAKER_00 <NA> <NA>") {
		t.Errorf("row 0 = %q, missing expected payload", lines[0])
	}
	if !strings.Contains(lines[1], " 2.340 1.760 <NA> <NA> SPEAKER_01 <NA> <NA>") {
		t.Errorf("row 1 = %q, missing expected payload", lines[1])
	}
}

func TestRenderRTTM_NegativeDurationClampedToZero(t *testing.T) {
	// Backends shouldn't emit end<start, but if they do (clock skew during a
	// long pipeline), the RTTM duration must stay non-negative.
	r := &schema.DiarizationResult{
		Segments: []schema.DiarizationSegment{
			{Id: 0, Speaker: "SPEAKER_00", Start: 5, End: 4},
		},
	}
	out := renderRTTM(r, "x.wav")
	if !strings.Contains(out, " 5.000 0.000 ") {
		t.Errorf("expected duration clamped to 0, got: %q", out)
	}
}

func TestRenderRTTM_FallbackFileID(t *testing.T) {
	r := &schema.DiarizationResult{
		Segments: []schema.DiarizationSegment{{Id: 0, Speaker: "SPEAKER_00", Start: 0, End: 1}},
	}
	out := renderRTTM(r, "")
	if !strings.HasPrefix(out, "SPEAKER audio 1 ") {
		t.Errorf("expected fallback file ID 'audio', got: %q", out)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
