package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseTranscript ([start][Sxx]text[end] grammar)", func() {
	It("parses the README two-speaker example into ordered segments", func() {
		raw := "[0.28][S01] And so, my fellow Americans, ask not what your country can do for you,[7.71][8.12][S02] ask what you can do for your country.[10.59]"
		segs := parseTranscript(raw)
		Expect(segs).To(HaveLen(2))

		Expect(segs[0].Speaker).To(Equal("S01"))
		Expect(segs[0].Start).To(BeNumerically("~", 0.28, 1e-9))
		Expect(segs[0].End).To(BeNumerically("~", 7.71, 1e-9))
		Expect(segs[0].Text).To(Equal("And so, my fellow Americans, ask not what your country can do for you,"))

		Expect(segs[1].Speaker).To(Equal("S02"))
		Expect(segs[1].Start).To(BeNumerically("~", 8.12, 1e-9))
		Expect(segs[1].End).To(BeNumerically("~", 10.59, 1e-9))
		Expect(segs[1].Text).To(Equal("ask what you can do for your country."))
	})

	It("parses a single segment", func() {
		segs := parseTranscript("[1.50][S01] hello world[3.25]")
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Start).To(BeNumerically("~", 1.50, 1e-9))
		Expect(segs[0].End).To(BeNumerically("~", 3.25, 1e-9))
		Expect(segs[0].Speaker).To(Equal("S01"))
		Expect(segs[0].Text).To(Equal("hello world"))
	})

	It("uppercases a lowercase speaker tag", func() {
		segs := parseTranscript("[0.00][s03] hi[0.90]")
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Speaker).To(Equal("S03"))
	})

	It("falls back to start==end when the closing timestamp is missing", func() {
		segs := parseTranscript("[2.00][S01] trailing words with no end")
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Start).To(BeNumerically("~", 2.00, 1e-9))
		Expect(segs[0].End).To(Equal(segs[0].Start))
		Expect(segs[0].Text).To(Equal("trailing words with no end"))
	})

	It("handles multi-digit speaker ids and higher speaker counts", func() {
		segs := parseTranscript("[0.0][S01] a[1.0][1.0][S12] b[2.0]")
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Speaker).To(Equal("S01"))
		Expect(segs[1].Speaker).To(Equal("S12"))
	})

	It("returns no segments for text without bracket structure", func() {
		Expect(parseTranscript("just some plain text")).To(BeEmpty())
		Expect(parseTranscript("")).To(BeEmpty())
	})

	It("skips a start timestamp not followed by a speaker tag", func() {
		// A stray timestamp pair without a speaker tag is not a segment head.
		segs := parseTranscript("[1.0][2.0][S01] real[3.0]")
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Speaker).To(Equal("S01"))
		Expect(segs[0].Text).To(Equal("real"))
	})
})

var _ = Describe("transcriptResultFromRaw", func() {
	It("shapes parsed segments into a TranscriptResult with nanosecond times and speakers", func() {
		raw := "[0.28][S01] first part,[7.71][8.12][S02] second part.[10.59]"
		res := transcriptResultFromRaw(raw)

		Expect(res.Segments).To(HaveLen(2))
		Expect(res.Segments[0].Id).To(Equal(int32(0)))
		Expect(res.Segments[0].Start).To(Equal(secondsToNanos(0.28)))
		Expect(res.Segments[0].End).To(Equal(secondsToNanos(7.71)))
		Expect(res.Segments[0].Speaker).To(Equal("S01"))
		Expect(res.Segments[0].Text).To(Equal("first part,"))

		Expect(res.Segments[1].Id).To(Equal(int32(1)))
		Expect(res.Segments[1].Speaker).To(Equal("S02"))
		Expect(res.Segments[1].Start).To(Equal(secondsToNanos(8.12)))

		// Full text is the segments joined with single spaces.
		Expect(res.Text).To(Equal("first part, second part."))
	})

	It("falls back to a single whole-clip text segment when nothing parses", func() {
		res := transcriptResultFromRaw("plain transcript with no markers")
		Expect(res.Segments).To(HaveLen(1))
		Expect(res.Segments[0].Text).To(Equal("plain transcript with no markers"))
		Expect(res.Segments[0].Start).To(Equal(int64(0)))
		Expect(res.Text).To(Equal("plain transcript with no markers"))
	})
})

var _ = Describe("secondsToNanos", func() {
	It("converts fractional seconds to int64 nanoseconds", func() {
		Expect(secondsToNanos(0)).To(Equal(int64(0)))
		Expect(secondsToNanos(1)).To(Equal(int64(1e9)))
		Expect(secondsToNanos(0.28)).To(Equal(int64(0.28 * 1e9)))
	})
})
