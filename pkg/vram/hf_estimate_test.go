package vram_test

import (
	. "github.com/mudler/LocalAI/pkg/vram"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseSizeString", func() {
	DescribeTable("valid sizes",
		func(input string, expected uint64) {
			got, err := ParseSizeString(input)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeNumerically("~", expected, float64(expected)*0.0001+1))
		},
		Entry("500MB", "500MB", uint64(500_000_000)),
		Entry("14.5GB", "14.5GB", uint64(14_500_000_000)),
		Entry("2TB", "2TB", uint64(2_000_000_000_000)),
		Entry("1024KB", "1024KB", uint64(1_024_000)),
		Entry("100B", "100B", uint64(100)),
		Entry("75 MB with space", "75 MB", uint64(75_000_000)),
		Entry("1.5 gb lowercase", "1.5 gb", uint64(1_500_000_000)),
		Entry("0.5GB", "0.5GB", uint64(500_000_000)),
		Entry("3PB", "3PB", uint64(3_000_000_000_000_000)),
		Entry("short suffix 100M", "100M", uint64(100_000_000)),
		Entry("short suffix 2G", "2G", uint64(2_000_000_000)),
		Entry("short suffix 1K", "1K", uint64(1_000)),
	)

	DescribeTable("invalid sizes",
		func(input string) {
			_, err := ParseSizeString(input)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("suffix only", "MB"),
		Entry("letters only", "abc"),
		Entry("negative", "-5GB"),
		Entry("unknown suffix", "5XB"),
	)
})

var _ = Describe("ExtractHFRepoID", func() {
	DescribeTable("valid repo IDs",
		func(input, expectedID string) {
			gotID, gotOK := ExtractHFRepoID(input)
			Expect(gotOK).To(BeTrue())
			Expect(gotID).To(Equal(expectedID))
		},
		Entry("short form", "Wan-AI/Wan2.2-I2V-A14B-Diffusers", "Wan-AI/Wan2.2-I2V-A14B-Diffusers"),
		Entry("short form 2", "meta-llama/Llama-3-8B", "meta-llama/Llama-3-8B"),
		Entry("https URL", "https://huggingface.co/Wan-AI/Wan2.2-I2V-A14B-Diffusers", "Wan-AI/Wan2.2-I2V-A14B-Diffusers"),
		Entry("http URL", "http://huggingface.co/Wan-AI/Wan2.2-I2V-A14B-Diffusers", "Wan-AI/Wan2.2-I2V-A14B-Diffusers"),
		Entry("no scheme", "huggingface.co/Wan-AI/Wan2.2-I2V-A14B-Diffusers", "Wan-AI/Wan2.2-I2V-A14B-Diffusers"),
		Entry("trailing slash", "https://huggingface.co/Wan-AI/Wan2.2-I2V-A14B-Diffusers/", "Wan-AI/Wan2.2-I2V-A14B-Diffusers"),
		Entry("extra path", "https://huggingface.co/Wan-AI/Wan2.2-I2V-A14B-Diffusers/tree/main", "Wan-AI/Wan2.2-I2V-A14B-Diffusers"),
		Entry("uppercase URL", "HTTPS://HUGGINGFACE.CO/org/model", "org/model"),
	)

	DescribeTable("invalid inputs",
		func(input string) {
			_, gotOK := ExtractHFRepoID(input)
			Expect(gotOK).To(BeFalse())
		},
		Entry("empty", ""),
		Entry("single word", "single-word"),
		Entry("three parts", "llama-cpp/models/file.gguf"),
		Entry("non-HF URL", "https://example.com/org/model"),
		Entry("wrong scheme", "ftp://huggingface.co/org/model"),
		Entry("has space", "has spaces/model"),
		Entry("incomplete URL", "huggingface.co/"),
		Entry("org only", "huggingface.co/org"),
		Entry("empty org", "huggingface.co//model"),
	)
})
