package openai

import (
	"strings"

	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("renderRTTM", func() {
	It("formats segments as NIST RTTM rows", func() {
		r := &schema.DiarizationResult{
			Segments: []schema.DiarizationSegment{
				{Id: 0, Speaker: "SPEAKER_00", Start: 0, End: 2.34},
				{Id: 1, Speaker: "SPEAKER_01", Start: 2.34, End: 4.10},
			},
		}
		out := renderRTTM(r, "/tmp/uploads/meeting.wav")

		lines := strings.Split(strings.TrimSpace(out), "\n")
		Expect(lines).To(HaveLen(2))

		// File ID should be the basename without extension; durations are
		// (end - start) with millisecond precision.
		Expect(lines[0]).To(HavePrefix("SPEAKER meeting 1 "))
		Expect(lines[0]).To(ContainSubstring(" 0.000 2.340 <NA> <NA> SPEAKER_00 <NA> <NA>"))
		Expect(lines[1]).To(ContainSubstring(" 2.340 1.760 <NA> <NA> SPEAKER_01 <NA> <NA>"))
	})

	It("clamps negative duration to zero", func() {
		// Backends shouldn't emit end<start, but if they do (clock skew during a
		// long pipeline), the RTTM duration must stay non-negative.
		r := &schema.DiarizationResult{
			Segments: []schema.DiarizationSegment{
				{Id: 0, Speaker: "SPEAKER_00", Start: 5, End: 4},
			},
		}
		out := renderRTTM(r, "x.wav")
		Expect(out).To(ContainSubstring(" 5.000 0.000 "))
	})

	It("falls back to 'audio' when the source file name is empty", func() {
		r := &schema.DiarizationResult{
			Segments: []schema.DiarizationSegment{{Id: 0, Speaker: "SPEAKER_00", Start: 0, End: 1}},
		}
		out := renderRTTM(r, "")
		Expect(out).To(HavePrefix("SPEAKER audio 1 "))
	})
})
