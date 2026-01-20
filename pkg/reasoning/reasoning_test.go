package reasoning_test

import (
	"strings"

	. "github.com/mudler/LocalAI/pkg/reasoning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExtractReasoning", func() {
	Context("when content has no reasoning tags", func() {
		It("should return empty reasoning and original content", func() {
			content := "This is regular content without any tags."
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should handle empty string", func() {
			content := ""
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle content with only whitespace", func() {
			content := "   \n\t  "
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})
	})

	Context("when content has <thinking> tags", func() {
		It("should extract reasoning from single thinking block", func() {
			content := "Some text <thinking>This is my reasoning</thinking> More text"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("This is my reasoning"))
			Expect(cleaned).To(Equal("Some text  More text"))
		})

		It("should extract reasoning and preserve surrounding content", func() {
			content := "Before <thinking>Reasoning here</thinking> After"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Reasoning here"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle thinking block at the start", func() {
			content := "<thinking>Start reasoning</thinking> Regular content"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Start reasoning"))
			Expect(cleaned).To(Equal(" Regular content"))
		})

		It("should handle thinking block at the end", func() {
			content := "Regular content <thinking>End reasoning</thinking>"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("End reasoning"))
			Expect(cleaned).To(Equal("Regular content "))
		})

		It("should handle only thinking block", func() {
			content := "<thinking>Only reasoning</thinking>"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Only reasoning"))
			Expect(cleaned).To(BeEmpty())
		})

		It("should trim whitespace from reasoning content", func() {
			content := "Text <thinking>  \n  Reasoning with spaces  \n  </thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Reasoning with spaces"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has <think> tags", func() {
		It("should extract reasoning from redacted_reasoning block", func() {
			content := "Text <think>Redacted reasoning</think> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Redacted reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle redacted_reasoning with multiline content", func() {
			content := "Before <think>Line 1\nLine 2\nLine 3</think> After"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Line 1\nLine 2\nLine 3"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle redacted_reasoning with complex content", func() {
			content := "Start <think>Complex reasoning\nwith\nmultiple\nlines</think> End"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Complex reasoning\nwith\nmultiple\nlines"))
			Expect(cleaned).To(Equal("Start  End"))
		})
	})

	Context("when content has multiple reasoning blocks", func() {
		It("should concatenate multiple thinking blocks with newlines", func() {
			content := "Text <thinking>First</thinking> Middle <thinking>Second</thinking> End"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("First\n\nSecond"))
			Expect(cleaned).To(Equal("Text  Middle  End"))
		})

		It("should handle multiple different tag types", func() {
			content := "A <thinking>One</thinking> B <think>Two</think> C <think>Three</think> D"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(ContainSubstring("One"))
			Expect(reasoning).To(ContainSubstring("Two"))
			Expect(reasoning).To(ContainSubstring("Three"))
			Expect(cleaned).To(Equal("A  B  C  D"))
		})

		It("should handle nested tags correctly (extracts first match)", func() {
			content := "Text <thinking>Outer <think>Inner</think></thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			// Should extract the outer thinking block
			Expect(reasoning).To(ContainSubstring("Outer"))
			Expect(reasoning).To(ContainSubstring("Inner"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has unclosed reasoning tags", func() {
		It("should extract unclosed thinking block", func() {
			content := "Text <thinking>Unclosed reasoning"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Unclosed reasoning"))
			Expect(cleaned).To(Equal("Text "))
		})

		It("should extract unclosed think block", func() {
			content := "Before <think>Incomplete"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Incomplete"))
			Expect(cleaned).To(Equal("Before "))
		})

		It("should extract unclosed redacted_reasoning block", func() {
			content := "Start <think>Partial reasoning content"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Partial reasoning content"))
			Expect(cleaned).To(Equal("Start "))
		})

		It("should handle unclosed tag at the end", func() {
			content := "Regular content <thinking>Unclosed at end"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Unclosed at end"))
			Expect(cleaned).To(Equal("Regular content "))
		})
	})

	Context("when content has empty reasoning blocks", func() {
		It("should ignore empty thinking block", func() {
			content := "Text <thinking></thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should ignore thinking block with only whitespace", func() {
			content := "Text <thinking>   \n\t  </thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has reasoning tags with special characters", func() {
		It("should handle reasoning with newlines", func() {
			content := "Before <thinking>Line 1\nLine 2\nLine 3</thinking> After"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Line 1\nLine 2\nLine 3"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle reasoning with code blocks", func() {
			content := "Text <thinking>Reasoning with ```code``` blocks</thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Reasoning with ```code``` blocks"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle reasoning with JSON", func() {
			content := "Before <think>{\"key\": \"value\"}</think> After"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("{\"key\": \"value\"}"))
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle reasoning with HTML-like content", func() {
			content := "Text <thinking>Reasoning with <tags> inside</thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Reasoning with <tags> inside"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has reasoning mixed with regular content", func() {
		It("should preserve content order correctly", func() {
			content := "Start <thinking>Reasoning</thinking> Middle <think>More reasoning</think> End"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(ContainSubstring("Reasoning"))
			Expect(reasoning).To(ContainSubstring("More reasoning"))
			Expect(cleaned).To(Equal("Start  Middle  End"))
		})

		It("should handle reasoning in the middle of a sentence", func() {
			content := "This is a <thinking>reasoning</thinking> sentence."
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("reasoning"))
			Expect(cleaned).To(Equal("This is a  sentence."))
		})
	})

	Context("edge cases", func() {
		It("should handle content with only opening tag", func() {
			content := "<thinking>"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(""))
		})

		It("should handle content with only closing tag", func() {
			content := "</thinking>"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("</thinking>"))
		})

		It("should handle mismatched tags", func() {
			content := "<thinking>Content</think>"
			reasoning, cleaned := ExtractReasoning(content, nil)
			// Should extract unclosed thinking block
			Expect(reasoning).To(ContainSubstring("Content"))
			Expect(cleaned).To(Equal(""))
		})

		It("should handle very long reasoning content", func() {
			longReasoning := strings.Repeat("This is reasoning content. ", 100)
			content := "Text <thinking>" + longReasoning + "</thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			// TrimSpace is applied, so we need to account for that
			Expect(reasoning).To(Equal(strings.TrimSpace(longReasoning)))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle reasoning with unicode characters", func() {
			content := "Text <thinking>Reasoning with 中文 and emoji 🧠</thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Reasoning with 中文 and emoji 🧠"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has <|START_THINKING|> tags (Command-R)", func() {
		It("should extract reasoning from START_THINKING block", func() {
			content := "Text <|START_THINKING|>Command-R reasoning<|END_THINKING|> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Command-R reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle unclosed START_THINKING block", func() {
			content := "Before <|START_THINKING|>Incomplete reasoning"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Incomplete reasoning"))
			Expect(cleaned).To(Equal("Before "))
		})
	})

	Context("when content has <|inner_prefix|> tags (Apertus)", func() {
		It("should extract reasoning from inner_prefix block", func() {
			content := "Text <|inner_prefix|>Apertus reasoning<|inner_suffix|> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Apertus reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has <seed:think> tags (Seed)", func() {
		It("should extract reasoning from seed:think block", func() {
			content := "Text <seed:think>Seed reasoning</seed:think> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Seed reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has <|think|> tags (Solar Open)", func() {
		It("should extract reasoning from Solar Open think block", func() {
			content := "Text <|think|>Solar reasoning<|end|><|begin|>assistant<|content|> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Solar reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})

	Context("when content has [THINK] tags (Magistral)", func() {
		It("should extract reasoning from THINK block", func() {
			content := "Text [THINK]Magistral reasoning[/THINK] More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Magistral reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle unclosed THINK block", func() {
			content := "Before [THINK]Incomplete reasoning"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Incomplete reasoning"))
			Expect(cleaned).To(Equal("Before "))
		})
	})
})

var _ = Describe("DetectThinkingStartToken", func() {
	Context("when prompt contains thinking start tokens", func() {
		It("should detect <|START_THINKING|> at the end", func() {
			prompt := "Some prompt text <|START_THINKING|>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<|START_THINKING|>"))
		})

		It("should detect <think> at the end", func() {
			prompt := "Prompt with <think>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<think>"))
		})

		It("should detect <thinking> at the end", func() {
			prompt := "Some text <thinking>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<thinking>"))
		})

		It("should detect <|inner_prefix|> at the end", func() {
			prompt := "Prompt <|inner_prefix|>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<|inner_prefix|>"))
		})

		It("should detect <seed:think> at the end", func() {
			prompt := "Text <seed:think>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<seed:think>"))
		})

		It("should detect <|think|> at the end", func() {
			prompt := "Prompt <|think|>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<|think|>"))
		})

		It("should detect [THINK] at the end", func() {
			prompt := "Text [THINK]"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("[THINK]"))
		})

		It("should handle trailing whitespace", func() {
			prompt := "Prompt <|START_THINKING|>   \n\t  "
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<|START_THINKING|>"))
		})

		It("should detect token near the end (within last 100 chars)", func() {
			prefix := strings.Repeat("x", 50)
			prompt := prefix + "<|START_THINKING|>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<|START_THINKING|>"))
		})

		It("should detect token when followed by only whitespace", func() {
			prompt := "Text <think>   \n  "
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<think>"))
		})
	})

	Context("when prompt does not contain thinking tokens", func() {
		It("should return empty string for regular prompt", func() {
			prompt := "This is a regular prompt without thinking tokens"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(BeEmpty())
		})

		It("should return empty string for empty prompt", func() {
			prompt := ""
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(BeEmpty())
		})

		It("should detect token even when far from end (Contains check)", func() {
			prefix := strings.Repeat("x", 150)
			prompt := prefix + "<|START_THINKING|>"
			token := DetectThinkingStartToken(prompt, nil)
			// Current implementation uses Contains, so it finds tokens anywhere
			Expect(token).To(Equal("<|START_THINKING|>"))
		})

		It("should detect token even when followed by non-whitespace (Contains check)", func() {
			prompt := "Text <|START_THINKING|>more text"
			token := DetectThinkingStartToken(prompt, nil)
			// Current implementation uses Contains, so it finds tokens anywhere
			Expect(token).To(Equal("<|START_THINKING|>"))
		})
	})

	Context("when multiple tokens are present", func() {
		It("should return the first matching token (most specific)", func() {
			prompt := "Text <|START_THINKING|> <thinking>"
			token := DetectThinkingStartToken(prompt, nil)
			// Should return the first one found (order matters)
			Expect(token).To(Equal("<|START_THINKING|>"))
		})
	})
})

var _ = Describe("PrependThinkingTokenIfNeeded", func() {
	Context("when startToken is empty", func() {
		It("should return content unchanged", func() {
			content := "Some content"
			result := PrependThinkingTokenIfNeeded(content, "")
			Expect(result).To(Equal(content))
		})
	})

	Context("when content already starts with token", func() {
		It("should not prepend if content starts with token", func() {
			content := "<|START_THINKING|>Reasoning content"
			result := PrependThinkingTokenIfNeeded(content, "<|START_THINKING|>")
			Expect(result).To(Equal(content))
		})

		It("should not prepend if content starts with token after whitespace", func() {
			content := "   <think>Reasoning"
			result := PrependThinkingTokenIfNeeded(content, "<think>")
			Expect(result).To(Equal(content))
		})

		It("should not prepend if token appears anywhere in content", func() {
			content := "Some text <thinking>Reasoning</thinking>"
			result := PrependThinkingTokenIfNeeded(content, "<thinking>")
			// With Contains check, it should not prepend
			Expect(result).To(Equal(content))
		})
	})

	Context("when content does not contain token", func() {
		It("should prepend token to content", func() {
			content := "Reasoning content"
			result := PrependThinkingTokenIfNeeded(content, "<|START_THINKING|>")
			Expect(result).To(Equal("<|START_THINKING|>Reasoning content"))
		})

		It("should prepend token after leading whitespace", func() {
			content := "   \n  Reasoning content"
			result := PrependThinkingTokenIfNeeded(content, "<think>")
			Expect(result).To(Equal("   \n  <think>Reasoning content"))
		})

		It("should handle empty content", func() {
			content := ""
			result := PrependThinkingTokenIfNeeded(content, "<thinking>")
			Expect(result).To(Equal("<thinking>"))
		})

		It("should handle content with only whitespace", func() {
			content := "   \n\t  "
			result := PrependThinkingTokenIfNeeded(content, "<|START_THINKING|>")
			Expect(result).To(Equal("   \n\t  <|START_THINKING|>"))
		})
	})

	Context("with different token types", func() {
		It("should prepend <|START_THINKING|>", func() {
			content := "Reasoning"
			result := PrependThinkingTokenIfNeeded(content, "<|START_THINKING|>")
			Expect(result).To(Equal("<|START_THINKING|>Reasoning"))
		})

		It("should prepend <think>", func() {
			content := "Reasoning"
			result := PrependThinkingTokenIfNeeded(content, "<think>")
			Expect(result).To(Equal("<think>Reasoning"))
		})

		It("should prepend <thinking>", func() {
			content := "Reasoning"
			result := PrependThinkingTokenIfNeeded(content, "<thinking>")
			Expect(result).To(Equal("<thinking>Reasoning"))
		})

		It("should prepend [THINK]", func() {
			content := "Reasoning"
			result := PrependThinkingTokenIfNeeded(content, "[THINK]")
			Expect(result).To(Equal("[THINK]Reasoning"))
		})
	})
})

var _ = Describe("ExtractReasoningWithConfig", func() {
	Context("when reasoning is disabled", func() {
		It("should return original content when DisableReasoning is true", func() {
			content := "Some text <thinking>Reasoning</thinking> More text"
			config := Config{DisableReasoning: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should return original content even with thinking start token when DisableReasoning is true", func() {
			content := "Reasoning content"
			config := Config{DisableReasoning: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<|START_THINKING|>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should return original content even with tag prefill disabled when DisableReasoning is true", func() {
			content := "Some content"
			config := Config{
				DisableReasoning:           boolPtr(true),
				DisableReasoningTagPrefill: boolPtr(false),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})
	})

	Context("when reasoning is enabled (DisableReasoning is nil or false)", func() {
		Context("when tag prefill is enabled (DisableReasoningTagPrefill is nil or false)", func() {
			It("should prepend token and extract reasoning when both configs are nil", func() {
				content := "Reasoning content"
				config := Config{}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
				// Token is prepended, then extracted
				Expect(reasoning).To(Equal("Reasoning content"))
				Expect(cleaned).To(BeEmpty())
			})

			It("should prepend token and extract reasoning when DisableReasoning is false", func() {
				content := "Some reasoning"
				config := Config{DisableReasoning: boolPtr(false)}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<think>", config)
				Expect(reasoning).To(Equal("Some reasoning"))
				Expect(cleaned).To(BeEmpty())
			})

			It("should prepend token and extract reasoning when DisableReasoningTagPrefill is false", func() {
				content := "My reasoning"
				config := Config{DisableReasoningTagPrefill: boolPtr(false)}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<|START_THINKING|>", config)
				Expect(reasoning).To(Equal("My reasoning"))
				Expect(cleaned).To(BeEmpty())
			})

			It("should prepend token to content with existing tags and extract", func() {
				content := "Before <thinking>Existing reasoning</thinking> After"
				config := Config{}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
				// Should extract existing reasoning, token prepend doesn't affect already tagged content
				Expect(reasoning).To(Equal("Existing reasoning"))
				Expect(cleaned).To(Equal("Before  After"))
			})

			It("should prepend token and extract from content that becomes tagged", func() {
				content := "Pure reasoning without tags"
				config := Config{}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
				// Token is prepended, making it <thinking>Pure reasoning without tags</thinking>
				// But since there's no closing tag, it extracts as unclosed
				Expect(reasoning).To(Equal("Pure reasoning without tags"))
				Expect(cleaned).To(BeEmpty())
			})

			It("should handle empty token when tag prefill is enabled", func() {
				content := "Some content <thinking>Reasoning</thinking> More"
				config := Config{}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "", config)
				// No token to prepend, just extract existing reasoning
				Expect(reasoning).To(Equal("Reasoning"))
				Expect(cleaned).To(Equal("Some content  More"))
			})

			It("should prepend token after leading whitespace", func() {
				content := "   \n  Reasoning content"
				config := Config{}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
				Expect(reasoning).To(Equal("Reasoning content"))
				Expect(cleaned).To(Equal("   \n  "))
			})
		})

		Context("when tag prefill is disabled (DisableReasoningTagPrefill is true)", func() {
			It("should extract reasoning without prepending token when DisableReasoningTagPrefill is true", func() {
				content := "Some text <thinking>Reasoning</thinking> More text"
				config := Config{DisableReasoningTagPrefill: boolPtr(true)}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
				Expect(reasoning).To(Equal("Reasoning"))
				Expect(cleaned).To(Equal("Some text  More text"))
			})

			It("should not prepend token to content without tags when DisableReasoningTagPrefill is true", func() {
				content := "Pure content without tags"
				config := Config{DisableReasoningTagPrefill: boolPtr(true)}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
				// No token prepended, no tags to extract
				Expect(reasoning).To(BeEmpty())
				Expect(cleaned).To(Equal(content))
			})

			It("should extract multiple reasoning blocks without prepending when DisableReasoningTagPrefill is true", func() {
				content := "A <thinking>First</thinking> B <think>Second</think> C"
				config := Config{DisableReasoningTagPrefill: boolPtr(true)}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
				Expect(reasoning).To(ContainSubstring("First"))
				Expect(reasoning).To(ContainSubstring("Second"))
				Expect(cleaned).To(Equal("A  B  C"))
			})

			It("should handle empty token when tag prefill is disabled", func() {
				content := "Text <thinking>Reasoning</thinking> More"
				config := Config{DisableReasoningTagPrefill: boolPtr(true)}
				reasoning, cleaned := ExtractReasoningWithConfig(content, "", config)
				Expect(reasoning).To(Equal("Reasoning"))
				Expect(cleaned).To(Equal("Text  More"))
			})
		})
	})

	Context("edge cases", func() {
		It("should handle empty content with default config", func() {
			content := ""
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle empty content when reasoning is disabled", func() {
			content := ""
			config := Config{DisableReasoning: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle empty token with content containing tags", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "", config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle content with only whitespace when reasoning is enabled", func() {
			content := "   \n\t  "
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			// Token is prepended after whitespace, then extracted as unclosed
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("   \n\t  "))
		})

		It("should handle content with only whitespace when reasoning is disabled", func() {
			content := "   \n\t  "
			config := Config{DisableReasoning: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should handle unclosed reasoning tags with tag prefill enabled", func() {
			content := "Some text <thinking>Unclosed"
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(Equal("Unclosed"))
			Expect(cleaned).To(Equal("Some text "))
		})

		It("should handle different token types with config", func() {
			content := "Reasoning content"
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<|START_THINKING|>", config)
			Expect(reasoning).To(Equal("Reasoning content"))
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle content that already contains the token", func() {
			content := "<thinking>Already tagged</thinking>"
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			// Token already present, should not prepend, just extract
			Expect(reasoning).To(Equal("Already tagged"))
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle complex reasoning with multiline content and tag prefill", func() {
			content := "Before\n<thinking>Line 1\nLine 2\nLine 3</thinking>\nAfter"
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(Equal("Line 1\nLine 2\nLine 3"))
			Expect(cleaned).To(Equal("Before\n\nAfter"))
		})
	})

	Context("config combinations", func() {
		It("should handle nil DisableReasoning and nil DisableReasoningTagPrefill", func() {
			content := "Reasoning"
			config := Config{}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle false DisableReasoning and true DisableReasoningTagPrefill", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			config := Config{
				DisableReasoning:           boolPtr(false),
				DisableReasoningTagPrefill: boolPtr(true),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle true DisableReasoning regardless of DisableReasoningTagPrefill", func() {
			content := "Some content <thinking>Reasoning</thinking>"
			config := Config{
				DisableReasoning:           boolPtr(true),
				DisableReasoningTagPrefill: boolPtr(false),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})
	})

	Context("when StripReasoningOnly is enabled", func() {
		It("should strip reasoning but keep cleaned content when StripReasoningOnly is true", func() {
			content := "Some text <thinking>Reasoning content</thinking> More text"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Some text  More text"))
		})

		It("should strip reasoning from multiple blocks when StripReasoningOnly is true", func() {
			content := "A <thinking>First</thinking> B <thinking>Second</thinking> C"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("A  B  C"))
		})

		It("should strip reasoning from different tag types when StripReasoningOnly is true", func() {
			content := "Before <thinking>One</thinking> Middle <think>Two</think> After"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Before  Middle  After"))
		})

		It("should strip reasoning but preserve content when StripReasoningOnly is true", func() {
			content := "Regular content <thinking>Reasoning</thinking>"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Regular content "))
		})

		It("should strip reasoning from unclosed tags when StripReasoningOnly is true", func() {
			content := "Text <thinking>Unclosed reasoning"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text "))
		})

		It("should strip reasoning from Command-R tags when StripReasoningOnly is true", func() {
			content := "Before <|START_THINKING|>Command-R reasoning<|END_THINKING|> After"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<|START_THINKING|>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should strip reasoning from Apertus tags when StripReasoningOnly is true", func() {
			content := "Text <|inner_prefix|>Apertus reasoning<|inner_suffix|> More"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<|inner_prefix|>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should strip reasoning from Seed tags when StripReasoningOnly is true", func() {
			content := "Before <seed:think>Seed reasoning</seed:think> After"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<seed:think>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should strip reasoning from Magistral tags when StripReasoningOnly is true", func() {
			content := "Text [THINK]Magistral reasoning[/THINK] More"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "[THINK]", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should strip reasoning with multiline content when StripReasoningOnly is true", func() {
			content := "Start <thinking>Line 1\nLine 2\nLine 3</thinking> End"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Start  End"))
		})

		It("should handle content with only reasoning tags when StripReasoningOnly is true", func() {
			content := "<thinking>Only reasoning</thinking>"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should handle empty reasoning blocks when StripReasoningOnly is true", func() {
			content := "Text <thinking></thinking> More"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle content without reasoning tags when StripReasoningOnly is true", func() {
			content := "Regular content without tags"
			config := Config{
				StripReasoningOnly:         boolPtr(true),
				DisableReasoningTagPrefill: boolPtr(true),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should handle content without reasoning tags when StripReasoningOnly is true", func() {
			content := "Regular content without tags"
			config := Config{
				StripReasoningOnly: boolPtr(true),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should handle content without reasoning tags when StripReasoningOnly is true", func() {
			content := "Regular content without tags"
			config := Config{
				StripReasoningOnly: boolPtr(true),
			}
			thinkingStartToken := DetectThinkingStartToken("template_without_thinking_tag", &config)
			reasoning, cleaned := ExtractReasoningWithConfig(content, thinkingStartToken, config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should handle content without reasoning tags when StripReasoningOnly is true", func() {
			content := "foo</think>Regular content without tags"
			config := Config{}
			thinkingStartToken := DetectThinkingStartToken("<think>", &config)
			reasoning, cleaned := ExtractReasoningWithConfig(content, thinkingStartToken, config)
			Expect(reasoning).To(Equal("foo"))
			Expect(cleaned).To(Equal("Regular content without tags"))
		})

		It("should strip reasoning when StripReasoningOnly is true and tag prefill is enabled", func() {
			content := "Reasoning content"
			config := Config{
				StripReasoningOnly:         boolPtr(true),
				DisableReasoningTagPrefill: boolPtr(false),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(BeEmpty())
		})

		It("should strip reasoning when StripReasoningOnly is true and tag prefill is disabled", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			config := Config{
				StripReasoningOnly:         boolPtr(true),
				DisableReasoningTagPrefill: boolPtr(true),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should not strip reasoning when StripReasoningOnly is false", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			config := Config{StripReasoningOnly: boolPtr(false)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should not strip reasoning when StripReasoningOnly is nil", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			config := Config{StripReasoningOnly: nil}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should strip reasoning but not affect DisableReasoning behavior", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			config := Config{
				DisableReasoning:   boolPtr(true),
				StripReasoningOnly: boolPtr(true),
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			// When DisableReasoning is true, reasoning extraction doesn't happen at all
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal(content))
		})

		It("should handle complex content with reasoning and regular text when StripReasoningOnly is true", func() {
			content := "Start <thinking>Reasoning</thinking> Middle <think>More reasoning</think> End"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Start  Middle  End"))
		})

		It("should handle reasoning with special characters when StripReasoningOnly is true", func() {
			content := "Before <thinking>Reasoning with ```code``` and {\"json\": true}</thinking> After"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Before  After"))
		})

		It("should handle reasoning with unicode when StripReasoningOnly is true", func() {
			content := "Text <thinking>Reasoning with 中文 and emoji 🧠</thinking> More"
			config := Config{StripReasoningOnly: boolPtr(true)}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "<thinking>", config)
			Expect(reasoning).To(BeEmpty())
			Expect(cleaned).To(Equal("Text  More"))
		})
	})
})

var _ = Describe("Custom Thinking Start Tokens", func() {
	Context("when custom thinking start tokens are provided", func() {
		It("should detect custom thinking start token", func() {
			prompt := "Some prompt <custom:think>"
			config := &Config{ThinkingStartTokens: []string{"<custom:think>"}}
			token := DetectThinkingStartToken(prompt, config)
			Expect(token).To(Equal("<custom:think>"))
		})

		It("should prioritize custom tokens over default tokens", func() {
			prompt := "Text <thinking> <custom:think>"
			config := &Config{ThinkingStartTokens: []string{"<custom:think>"}}
			token := DetectThinkingStartToken(prompt, config)
			// Custom token should be found first even if default token appears later
			Expect(token).To(Equal("<custom:think>"))
		})

		It("should detect multiple custom tokens (first match)", func() {
			prompt := "Prompt <token1> <token2>"
			config := &Config{ThinkingStartTokens: []string{"<token1>", "<token2>"}}
			token := DetectThinkingStartToken(prompt, config)
			Expect(token).To(Equal("<token1>"))
		})

		It("should fall back to default tokens if custom tokens not found", func() {
			prompt := "Text <thinking>"
			config := &Config{ThinkingStartTokens: []string{"<custom:think>"}}
			token := DetectThinkingStartToken(prompt, config)
			Expect(token).To(Equal("<thinking>"))
		})

		It("should handle empty custom tokens list", func() {
			prompt := "Text <thinking>"
			config := &Config{ThinkingStartTokens: []string{}}
			token := DetectThinkingStartToken(prompt, config)
			Expect(token).To(Equal("<thinking>"))
		})

		It("should handle nil config (use defaults only)", func() {
			prompt := "Text <thinking>"
			token := DetectThinkingStartToken(prompt, nil)
			Expect(token).To(Equal("<thinking>"))
		})
	})
})

var _ = Describe("Custom Tag Pairs", func() {
	Context("when custom tag pairs are provided", func() {
		It("should extract reasoning from custom tag pair", func() {
			content := "Text <custom:think>Custom reasoning</custom:think> More"
			config := &Config{TagPairs: []TagPair{{Start: "<custom:think>", End: "</custom:think>"}}}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Custom reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should prioritize custom tag pairs over default pairs", func() {
			content := "Text <custom:think>Custom</custom:think> <thinking>Default</thinking> More"
			config := &Config{TagPairs: []TagPair{{Start: "<custom:think>", End: "</custom:think>"}}}
			reasoning, cleaned := ExtractReasoning(content, config)
			// Should extract both, but custom comes first
			Expect(reasoning).To(ContainSubstring("Custom"))
			Expect(reasoning).To(ContainSubstring("Default"))
			Expect(cleaned).To(Equal("Text   More"))
		})

		It("should handle multiple custom tag pairs", func() {
			content := "A <tag1>First</tag1> B <tag2>Second</tag2> C"
			config := &Config{
				TagPairs: []TagPair{
					{Start: "<tag1>", End: "</tag1>"},
					{Start: "<tag2>", End: "</tag2>"},
				},
			}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(ContainSubstring("First"))
			Expect(reasoning).To(ContainSubstring("Second"))
			Expect(cleaned).To(Equal("A  B  C"))
		})

		It("should handle custom tag pairs with complex end tags", func() {
			content := "Text <start>Reasoning<end><begin>assistant</begin> More"
			config := &Config{TagPairs: []TagPair{{Start: "<start>", End: "<end><begin>assistant</begin>"}}}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle unclosed custom tag pairs", func() {
			content := "Text <custom:think>Unclosed reasoning"
			config := &Config{TagPairs: []TagPair{{Start: "<custom:think>", End: "</custom:think>"}}}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Unclosed reasoning"))
			Expect(cleaned).To(Equal("Text "))
		})

		It("should ignore invalid tag pairs (empty start or end)", func() {
			content := "Text <valid>Content</valid> More"
			config := &Config{
				TagPairs: []TagPair{
					{Start: "", End: "</invalid>"},      // Invalid: empty start
					{Start: "<invalid>", End: ""},       // Invalid: empty end
					{Start: "<valid>", End: "</valid>"}, // Valid
				},
			}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Content"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should fall back to default tag pairs if custom pairs not found", func() {
			content := "Text <thinking>Default reasoning</thinking> More"
			config := &Config{TagPairs: []TagPair{{Start: "<custom:think>", End: "</custom:think>"}}}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Default reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle empty custom tag pairs list", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			config := &Config{TagPairs: []TagPair{}}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle nil config (use defaults only)", func() {
			content := "Text <thinking>Reasoning</thinking> More"
			reasoning, cleaned := ExtractReasoning(content, nil)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle custom tag pairs with special characters", func() {
			content := "Text <[think]>Reasoning</[think]> More"
			config := &Config{TagPairs: []TagPair{{Start: "<[think]>", End: "</[think]>"}}}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})

		It("should handle custom tag pairs with multiline content", func() {
			content := "Before <custom>Line 1\nLine 2\nLine 3</custom> After"
			config := &Config{TagPairs: []TagPair{{Start: "<custom>", End: "</custom>"}}}
			reasoning, cleaned := ExtractReasoning(content, config)
			Expect(reasoning).To(Equal("Line 1\nLine 2\nLine 3"))
			Expect(cleaned).To(Equal("Before  After"))
		})
	})
})

var _ = Describe("Custom Tokens and Tag Pairs Integration", func() {
	Context("when both custom tokens and tag pairs are provided", func() {
		It("should use custom thinking start token and custom tag pair together", func() {
			content := "Reasoning content"
			config := Config{
				ThinkingStartTokens: []string{"<custom:think>"},
				TagPairs:            []TagPair{{Start: "<custom:think>", End: "</custom:think>"}},
			}
			// First detect the token
			token := DetectThinkingStartToken("Prompt <custom:think>", &config)
			Expect(token).To(Equal("<custom:think>"))
			// Then extract with the custom tag pair
			reasoning, cleaned := ExtractReasoningWithConfig(content, token, config)
			Expect(reasoning).To(Equal("Reasoning content"))
			Expect(cleaned).To(BeEmpty())
		})

		It("should work with ExtractReasoningWithConfig and custom config", func() {
			content := "Text <custom:think>Reasoning</custom:think> More"
			config := Config{
				TagPairs: []TagPair{{Start: "<custom:think>", End: "</custom:think>"}},
			}
			reasoning, cleaned := ExtractReasoningWithConfig(content, "", config)
			Expect(reasoning).To(Equal("Reasoning"))
			Expect(cleaned).To(Equal("Text  More"))
		})
	})
})

// Helper function to create bool pointers for test configs
func boolPtr(b bool) *bool {
	return &b
}
