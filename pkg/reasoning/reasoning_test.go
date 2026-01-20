package reasoning_test

import (
	"strings"

	. "github.com/mudler/LocalAI/pkg/reasoning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DetectThinkingForcedOpen", func() {
	It("should detect <think> at end of prompt", func() {
		Expect(DetectThinkingForcedOpen("Some prompt<think>")).To(BeTrue())
		Expect(DetectThinkingForcedOpen("Some prompt<think>\n")).To(BeTrue())
	})

	It("should detect <thinking> at end of prompt", func() {
		Expect(DetectThinkingForcedOpen("Some prompt<thinking>")).To(BeTrue())
		Expect(DetectThinkingForcedOpen("Some prompt<thinking>\n")).To(BeTrue())
	})

	It("should detect model-specific tags", func() {
		Expect(DetectThinkingForcedOpen("Some prompt<|inner_prefix|>")).To(BeTrue())
		Expect(DetectThinkingForcedOpen("Some prompt<|START_THINKING|>")).To(BeTrue())
		Expect(DetectThinkingForcedOpen("Some prompt<seed:think>")).To(BeTrue())
		Expect(DetectThinkingForcedOpen("Some prompt[THINK]")).To(BeTrue())
		Expect(DetectThinkingForcedOpen("Some prompt[THINK]\n")).To(BeTrue())
	})

	It("should not detect if tag is in the middle", func() {
		Expect(DetectThinkingForcedOpen("Some <think> prompt")).To(BeFalse())
		Expect(DetectThinkingForcedOpen("<think>reasoning</think>")).To(BeFalse())
	})

	It("should not detect if no thinking tag", func() {
		Expect(DetectThinkingForcedOpen("Some regular prompt")).To(BeFalse())
		Expect(DetectThinkingForcedOpen("")).To(BeFalse())
	})
})

var _ = Describe("Extract", func() {
	Context("when content has no reasoning tags", func() {
		It("should return empty reasoning and original content", func() {
			content := "This is regular content without any tags."
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should handle empty string", func() {
			content := ""
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle content with only whitespace", func() {
			content := "   \n\t  "
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})
	})

	Context("when content has <thinking> tags", func() {
		It("should extract reasoning from single thinking block", func() {
			content := "Some text <thinking>This is my reasoning</thinking> More text"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("This is my reasoning"))
			Expect(cleaned).To(Equal("Some text  More text"))
		})

		It("should extract reasoning and preserve surrounding content", func() {
			content := "Before <thinking>Reasoning here</thinking> After"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Reasoning here"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle thinking block at the start", func() {
			content := "<thinking>Start reasoning</thinking> Regular content"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Start reasoning"))
			Expect(cleaned).To(Equal(" Regular content"))
		})

		It("should handle thinking block at the end", func() {
			content := "Regular content <thinking>End reasoning</thinking>"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("End reasoning"))
			Expect(cleaned).To(Equal("Regular content "))
		})

		It("should handle only thinking block", func() {
			content := "<thinking>Only reasoning</thinking>"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Only reasoning"))
			Expect(cleaned).To(BeEmpty())
		})

		It("should trim whitespace from reasoning content", func() {
			content := "Text <thinking>  \n  Reasoning with spaces  \n  </thinking> More"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Reasoning with spaces"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has <think> tags", func() {
		It("should extract reasoning from redacted_reasoning block", func() {
			content := "Text <think>Redacted reasoning</think> More"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Redacted reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle redacted_reasoning with multiline content", func() {
			content := "Before <think>Line 1\nLine 2\nLine 3</think> After"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Line 1\nLine 2\nLine 3"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle redacted_reasoning with complex content", func() {
			content := "Start <think>Complex reasoning\nwith\nmultiple\nlines</think> End"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Complex reasoning\nwith\nmultiple\nlines"))
			Expect(cleaned).To(Equal("Start  End"))
		})
	})

	Context("when content has multiple reasoning blocks", func() {
		It("should concatenate multiple thinking blocks with newlines", func() {
			content := "Text <thinking>First</thinking> Middle <thinking>Second</thinking> End"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("First\n\nSecond"))
			Expect(cleaned).To(Equal("Text  Middle  End"))
		})

		It("should handle multiple different tag types", func() {
			content := "A <thinking>One</thinking> B <think>Two</think> C <think>Three</think> D"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(ContainSubstring("One"))
			Expect(reasoning).To(ContainSubstring("Two"))
			Expect(reasoning).To(ContainSubstring("Three"))
			Expect(cleaned).To(Equal("A  B  C  D"))
		})

		It("should handle nested tags correctly (extracts first match)", func() {
			content := "Text <thinking>Outer <think>Inner</think></thinking> More"
			reasoning, cleaned := Extract(content)
			// Should extract the outer thinking block
			Expect(reasoning).To(ContainSubstring("Outer"))
			Expect(reasoning).To(ContainSubstring("Inner"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has unclosed reasoning tags", func() {
		It("should extract unclosed thinking block", func() {
			content := "Text <thinking>Unclosed reasoning"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Unclosed reasoning"))
			Expect(cleaned).To(Equal("Text "))
		})

		It("should extract unclosed think block", func() {
			content := "Before <think>Incomplete"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Incomplete"))
			Expect(cleaned).To(Equal("Before "))
		})

		It("should extract unclosed redacted_reasoning block", func() {
			content := "Start <think>Partial reasoning content"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Partial reasoning content"))
			Expect(cleaned).To(Equal("Start "))
		})

		It("should handle unclosed tag at the end", func() {
			content := "Regular content <thinking>Unclosed at end"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Unclosed at end"))
			Expect(cleaned).To(Equal("Regular content "))
		})
	})

	Context("when content has empty reasoning blocks", func() {
		It("should ignore empty thinking block", func() {
			content := "Text <thinking></thinking> More"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should ignore thinking block with only whitespace", func() {
			content := "Text <thinking>   \n\t  </thinking> More"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has reasoning tags with special characters", func() {
		It("should handle reasoning with newlines", func() {
			content := "Before <thinking>Line 1\nLine 2\nLine 3</thinking> After"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Line 1\nLine 2\nLine 3"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle reasoning with code blocks", func() {
			content := "Text <thinking>Reasoning with ```code``` blocks</thinking> More"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Reasoning with ```code``` blocks"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle reasoning with JSON", func() {
			content := "Before <think>{\"key\": \"value\"}</think> After"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("{\"key\": \"value\"}"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle reasoning with HTML-like content", func() {
			content := "Text <thinking>Reasoning with <tags> inside</thinking> More"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Reasoning with <tags> inside"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has reasoning mixed with regular content", func() {
		It("should preserve content order correctly", func() {
			content := "Start <thinking>Reasoning</thinking> Middle <think>More reasoning</think> End"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(ContainSubstring("Reasoning"))
			Expect(reasoning).To(ContainSubstring("More reasoning"))
			Expect(cleaned).To(Equal("Start  Middle  End"))
		})

		It("should handle reasoning in the middle of a sentence", func() {
			content := "This is a <thinking>reasoning</thinking> sentence."
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("reasoning"))
			Expect(cleaned).To(Equal("This is a  sentence."))
		})
	})

	Context("edge cases without WithThinkingForcedOpen", func() {
		It("should handle content with only opening tag", func() {
			content := "<thinking>"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(""))
		})

		It("should handle content with only closing tag (no content before)", func() {
			content := "</thinking>"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should extract reasoning when only closing tag is present", func() {
			// GLM-4 style: reasoning content followed by closing tag without opening tag
			content := "This is reasoning content</think>this is the actual response"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("This is reasoning content"))
			Expect(cleaned).To(Equal("this is the actual response"))
		})

		It("should handle closing-only tag with multiline reasoning", func() {
			content := "1. First point\n2. Second point\n3. Third point</think>Final answer"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("1. First point\n2. Second point\n3. Third point"))
			Expect(cleaned).To(Equal("Final answer"))
		})

		It("should handle closing-only tag with complex reasoning (GLM-4 example)", func() {
			content := "**Analyze the user's input:** The user says something.\n\n**Final Decision:** Output the text.</think>this is a test"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("**Analyze the user's input:** The user says something.\n\n**Final Decision:** Output the text."))
			Expect(cleaned).To(Equal("this is a test"))
		})

		It("should handle closing-only thinking tag", func() {
			content := "Some reasoning here</thinking>actual content"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Some reasoning here"))
			Expect(cleaned).To(Equal("actual content"))
		})

		It("should handle mismatched tags", func() {
			content := "<thinking>Content</think>"
			reasoning, cleaned := Extract(content)
			// Should extract unclosed thinking block
			Expect(reasoning).To(ContainSubstring("Content"))
			Expect(cleaned).To(Equal(""))
		})

		It("should handle very long reasoning content", func() {
			longReasoning := strings.Repeat("This is reasoning content. ", 100)
			content := "Text <thinking>" + longReasoning + "</thinking> More"
			reasoning, cleaned := Extract(content)
			// TrimSpace is applied, so we need to account for that
			Expect(reasoning).To(Equal(strings.TrimSpace(longReasoning)))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle reasoning with unicode characters", func() {
			content := "Text <thinking>Reasoning with 中文 and emoji 🧠</thinking> More"
			reasoning, cleaned := Extract(content)
			Expect(reasoning).To(Equal("Reasoning with 中文 and emoji 🧠"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("with WithThinkingForcedOpen option", func() {
		It("should treat all content as reasoning until closing tag", func() {
			content := "This is reasoning</think>this is content"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(Equal("This is reasoning"))
			Expect(cleaned).To(Equal("this is content"))
		})

		It("should treat all content as reasoning when no closing tag (streaming)", func() {
			content := "This is reasoning content still streaming"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(Equal("This is reasoning content still streaming"))
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle GLM-4 style output", func() {
			content := "**Analyze:** The user says something.\n\n**Final Decision:** Output the text.</think>this is a test"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(Equal("**Analyze:** The user says something.\n\n**Final Decision:** Output the text."))
			Expect(cleaned).To(Equal("this is a test"))
		})

		It("should handle multiline reasoning with closing tag", func() {
			content := "1. First point\n2. Second point\n3. Third point</think>Final answer"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(Equal("1. First point\n2. Second point\n3. Third point"))
			Expect(cleaned).To(Equal("Final answer"))
		})

		It("should handle </thinking> closing tag", func() {
			content := "Some reasoning here</thinking>actual content"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(Equal("Some reasoning here"))
			Expect(cleaned).To(Equal("actual content"))
		})

		It("should handle additional reasoning blocks after initial forced open", func() {
			content := "Initial reasoning</think>content<think>more reasoning</think>final content"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(Equal("Initial reasoning\n\nmore reasoning"))
			Expect(cleaned).To(Equal("contentfinal content"))
		})

		It("should handle empty content", func() {
			reasoning, cleaned := Extract("", WithThinkingForcedOpen())
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle only closing tag", func() {
			content := "</think>only content"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("only content"))
		})

		It("should find earliest closing tag", func() {
			// </think> comes before </thinking>
			content := "Reasoning</think>content</thinking>more"
			reasoning, cleaned := Extract(content, WithThinkingForcedOpen())
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("content</thinking>more"))
		})
	})
})
