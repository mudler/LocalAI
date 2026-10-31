package loops_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/loops"
)

func TestLoops(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Loops Suite")
}

var _ = Describe("Loop values", func() {
	DescribeTable("LooksLikeInterval", func(in string, want bool) {
		Expect(loops.LooksLikeInterval(in)).To(Equal(want))
	},
		Entry("seconds", "30s", true),
		Entry("minutes", "5m", true),
		Entry("hours and minutes", "1h30m", true),
		Entry("a word", "check", false),
		Entry("a bare number", "5", false),
		Entry("milliseconds", "5ms", false),
		Entry("days", "2d", false),
		Entry("empty", "", false),
		Entry("unit first", "m5", false),
	)

	It("parses an interval within the bounds", func() {
		d, err := loops.ParseInterval("5m", time.Minute)
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(Equal(5 * time.Minute))
		_, err = loops.ParseInterval("30s", time.Minute)
		Expect(err).To(MatchError(loops.ErrInvalid))
		Expect(err.Error()).To(ContainSubstring("at least 1m"))
		_, err = loops.ParseInterval("25h", time.Minute)
		Expect(err).To(MatchError(loops.ErrInvalid))
		_, err = loops.ParseInterval("soon", time.Minute)
		Expect(err.Error()).To(ContainSubstring("not an interval"))
	})

	It("clamps a wait to the minimum and to a day", func() {
		Expect(loops.Clamp(time.Second, time.Minute)).To(Equal(time.Minute))
		Expect(loops.Clamp(48*time.Hour, time.Minute)).To(Equal(24 * time.Hour))
		Expect(loops.Clamp(10*time.Minute, time.Minute)).To(Equal(10 * time.Minute))
	})

	It("skips missed slots instead of running them", func() {
		from := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
		Expect(loops.NextSlot(from, 5*time.Minute, from.Add(time.Minute))).To(Equal(from.Add(5 * time.Minute)))
		Expect(loops.NextSlot(from, 5*time.Minute, from.Add(17*time.Minute))).To(Equal(from.Add(20 * time.Minute)))
		Expect(loops.NextSlot(from, 5*time.Minute, from.Add(20*time.Minute))).To(Equal(from.Add(25 * time.Minute)))
	})

	It("writes durations short and cuts long reasons", func() {
		Expect(loops.ShortDuration(5 * time.Minute)).To(Equal("5m"))
		Expect(loops.ShortDuration(2 * time.Hour)).To(Equal("2h"))
		Expect(loops.ShortDuration(90 * time.Minute)).To(Equal("1h30m"))
		long := ""
		for range 400 {
			long += "é"
		}
		Expect(len(loops.Cut(long))).To(BeNumerically("<=", loops.MaxReasonBytes))
	})

	It("names budget days in UTC", func() {
		t := time.Date(2026, 10, 31, 23, 30, 0, 0, time.FixedZone("x", -2*3600))
		Expect(loops.Day(t)).To(Equal("2026-11-01"))
		Expect(loops.NextDay(t)).To(Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)))
	})
})
