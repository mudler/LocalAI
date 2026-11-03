package studio_test

import (
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the plain answer to a proposal", func() {
	It("reads a short word of agreement or refusal", func() {
		for _, w := range []string{"yes", "Yes!", " ok ", "OK.", "okay", "sure", "yep", "go ahead", "do it", "👍", "sì", "ja", "confirm"} {
			v, code, ok := studio.ParseReply(w)
			Expect(ok).To(BeTrue(), w)
			Expect(v).To(Equal("confirm"), w)
			Expect(code).To(BeEmpty(), w)
		}
		for _, w := range []string{"no", "No.", "cancel", "stop", "nope", "no thanks", "never mind", "👎", "nein"} {
			v, code, ok := studio.ParseReply(w)
			Expect(ok).To(BeTrue(), w)
			Expect(v).To(Equal("cancel"), w)
			Expect(code).To(BeEmpty(), w)
		}
	})

	It("reads a word with the code, for when several proposals wait", func() {
		v, code, ok := studio.ParseReply("yes 7k2q")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("confirm"))
		Expect(code).To(Equal("7K2Q"))
		v, code, ok = studio.ParseReply("Cancel 7K2Q")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("cancel"))
		Expect(code).To(Equal("7K2Q"))
	})

	It("leaves any longer or mixed message to the agent", func() {
		for _, w := range []string{"", "yes but at 19:00", "ok, and also remind me about the milk", "yes yes yes", "can you cancel my order",
			"maybe", "okay then why", "yes 7K2", "yes 7K2QX"} {
			_, _, ok := studio.ParseReply(w)
			Expect(ok).To(BeFalse(), w)
		}
	})
})

var _ = Describe("a time that a chat gives", func() {
	It("reads a delay in words and numbers", func() {
		for in, want := range map[string]time.Duration{
			"10m": 10 * time.Minute, "10 minutes": 10 * time.Minute, "1 minute": time.Minute, "an hour": time.Hour, "1h": time.Hour,
			"2 hours": 2 * time.Hour, "1h30m": 90 * time.Minute, "1 hour 30 minutes": 90 * time.Minute, "90 min": 90 * time.Minute,
			"2d": 48 * time.Hour, "1 day": 24 * time.Hour, "half an hour": 30 * time.Minute, " 5 MIN ": 5 * time.Minute,
		} {
			got, err := studio.ParseDelay(in)
			Expect(err).NotTo(HaveOccurred(), in)
			Expect(got).To(Equal(want), in)
		}
	})

	It("refuses what it cannot read or what is not a delay", func() {
		for _, in := range []string{"", "soon", "ten minutes", "-5m", "0m", "5", "3 weeks", "10m later maybe"} {
			_, err := studio.ParseDelay(in)
			Expect(err).To(HaveOccurred(), in)
		}
	})
})

var _ = Describe("a time of day that a chat gives", func() {
	It("reads 24-hour and am/pm forms", func() {
		for in, want := range map[string][2]int{"18:00": {18, 0}, "6pm": {18, 0}, "6:30 PM": {18, 30}, "12am": {0, 0}, "12pm": {12, 0}, "7": {7, 0}, "07:05": {7, 5}} {
			h, m, err := studio.ParseClock(in)
			Expect(err).NotTo(HaveOccurred(), in)
			Expect([2]int{h, m}).To(Equal(want), in)
		}
	})
	It("refuses what is not a time", func() {
		for _, in := range []string{"", "25:00", "18:60", "13pm", "0am", "noon", "6 o'clock"} {
			_, _, err := studio.ParseClock(in)
			Expect(err).To(HaveOccurred(), in)
		}
	})
})
