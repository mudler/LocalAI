package functions

import (
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Robust fix for the glm-4.5 XML auto-detect false positive (relates to #9722
// / supersedes the brittle leading-"{" filter in #9940). When the XML
// auto-detector mis-identifies a Hermes-style <tool_call>JSON</tool_call> block
// as glm-4.5, it extracts the block body as the function NAME. A real function
// name is [A-Za-z0-9_.-]+; anything with braces, brackets, whitespace, quotes
// or colons is a misparse and must not be returned (so JSON parsing can take
// over). This is stronger than checking only for a leading "{": it also rejects
// leading prose, JSON arrays, and brace-less garbage.
var _ = Describe("glm-4.5 auto-detect name validation (#9722/#9940)", func() {
	// plausibleName mirrors the contract: a returned auto-detected tool name
	// must look like a real function name.
	plausible := regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

	DescribeTable("auto-detect must not emit a misparsed tool name",
		func(input string) {
			results, err := ParseXMLIterative(input, nil, false)
			Expect(err).ToNot(HaveOccurred())
			for _, r := range results {
				Expect(plausible.MatchString(r.Name)).To(BeTrue(),
					"auto-detected XML tool name must look like a function name, got: %q", r.Name)
			}
		},
		Entry("canonical Hermes JSON", "<tool_call>\n{\"name\": \"bash\", \"arguments\": {\"script\": \"ls\"}}\n</tool_call>"),
		Entry("leading prose then JSON", "<tool_call>\nSure: {\"name\": \"bash\", \"arguments\": {\"script\": \"ls\"}}\n</tool_call>"),
		Entry("JSON array (parallel calls)", "<tool_call>\n[{\"name\": \"bash\", \"arguments\": {}}]\n</tool_call>"),
		Entry("brace-less garbage", "<tool_call>\nname: bash, arguments: {}\n</tool_call>"),
	)

	// No-regression: a genuine glm-4.5 tool call must still be auto-detected.
	It("still parses a legitimate glm-4.5 tool call", func() {
		legit := "<tool_call>get_weather\n<arg_key>city</arg_key>\n<arg_value>NYC</arg_value>\n</tool_call>"
		results, err := ParseXMLIterative(legit, nil, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("get_weather"))
	})

	// A user who explicitly forces the glm-4.5 format keeps the raw behaviour
	// (no name filtering) — only auto-detection is guarded.
	It("does not filter when the glm-4.5 format is explicitly forced", func() {
		input := "<tool_call>\n{\"name\": \"bash\", \"arguments\": {}}\n</tool_call>"
		forced, err := ParseXMLIterative(input, GetXMLFormatPreset("glm-4.5"), false)
		Expect(err).ToNot(HaveOccurred())
		Expect(forced).ToNot(BeEmpty(),
			"explicit format must be trusted verbatim, even if it yields a JSON-blob name")
	})
})
