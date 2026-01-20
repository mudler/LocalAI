package reasoning

import (
	"strings"
)

// DetectThinkingStartToken checks if the prompt or template contains a thinking start token
// and returns the detected token. This indicates that the model's prompt template
// already includes the thinking token, so the model output will start with reasoning
// content without an explicit opening tag.
// Returns the detected token if found, empty string otherwise.
// Common tokens checked (in order of specificity - longer first):
// Based on llama.cpp's chat-parser.cpp implementations:
// - <|START_THINKING|>      (Command-R models)
// - <|inner_prefix|>        (Apertus models)
// - <seed:think>            (Seed models)
// - <think>    (DeepSeek, Granite, ExaOne models)
// - <|think|>               (Solar Open models)
// - <thinking>              (General thinking tag)
// - <think>                 (GLM models)
// - [THINK]                 (Magistral models)
func DetectThinkingStartToken(prompt string) string {
	// Common thinking start tokens (in order of specificity - longer first)
	// Based on llama.cpp's chat-parser.cpp implementations
	thinkingStartTokens := []string{
		"<|START_THINKING|>", // Command-R models
		"<|inner_prefix|>",   // Apertus models
		"<seed:think>",       // Seed models
		"<think>",            // DeepSeek, Granite, ExaOne models
		"<|think|>",          // Solar Open models
		"<thinking>",         // General thinking tag
		"[THINK]",            // Magistral models
	}

	// Check if prompt ends with any of these tokens (allowing for trailing whitespace/newlines)
	trimmedPrompt := strings.TrimRight(prompt, " \t\n\r")
	for _, token := range thinkingStartTokens {
		if strings.Contains(trimmedPrompt, token) {
			return token
		}
	}

	// Also check if any of these tokens appear near the end (within last 100 chars)
	// This handles cases where there might be stop tokens or other content after
	if len(trimmedPrompt) > 100 {
		lastPart := trimmedPrompt[len(trimmedPrompt)-100:]
		for _, token := range thinkingStartTokens {
			if idx := strings.LastIndex(lastPart, token); idx != -1 {
				// Check if this is the last meaningful content (only whitespace after)
				afterToken := lastPart[idx+len(token):]
				if strings.TrimSpace(afterToken) == "" {
					return token
				}
			}
		}
	}

	return ""
}

// ExtractReasoningWithConfig extracts reasoning from content with the given config.
// If reasoning is disabled, it returns the original content.
// If thinking start token prefill is enabled, it prepends the thinking start token to the content.
// It returns the extracted reasoning and the cleaned content.
func ExtractReasoningWithConfig(content, thinkingStartToken string, config Config) (reasoning string, cleanedContent string) {
	cleanedContent = content
	// If reasoning is not disabled, prepend the thinking start token if needed and extract reasoning
	if config.DisableReasoning == nil || !*config.DisableReasoning {
		// If thinking start token prefill is not disabled, prepend the thinking start token
		if config.DisableReasoningTagPrefill == nil || !*config.DisableReasoningTagPrefill {
			cleanedContent = PrependThinkingTokenIfNeeded(cleanedContent, thinkingStartToken)
		}
		// Extract reasoning from the cleaned content
		reasoning, cleanedContent = ExtractReasoning(cleanedContent)
	}

	return reasoning, cleanedContent
}

// PrependThinkingTokenIfNeeded prepends the thinking start token to content if it was
// detected in the prompt. This allows the standard extraction logic to work correctly
// for models where the thinking token is already in the prompt.
func PrependThinkingTokenIfNeeded(content string, startToken string) string {
	if startToken == "" {
		return content
	}

	// Check if content already starts with the token (allowing for leading whitespace)
	trimmed := strings.TrimLeftFunc(content, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})

	// If content already starts with the token, don't prepend
	if strings.Contains(trimmed, startToken) {
		return content
	}

	// Find where leading whitespace ends
	whitespaceEnd := 0
	for whitespaceEnd < len(content) {
		r := content[whitespaceEnd]
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			break
		}
		whitespaceEnd++
	}

	// Prepend the token after whitespace to make it look like normal tagged content
	if whitespaceEnd > 0 {
		return content[:whitespaceEnd] + startToken + content[whitespaceEnd:]
	}
	return startToken + content
}

// ExtractReasoning extracts reasoning content from thinking tags and returns
// both the extracted reasoning and the cleaned content (with tags removed).
// It handles <thinking>...</thinking> and <think>...</think> tags.
// Multiple reasoning blocks are concatenated with newlines.
func ExtractReasoning(content string) (reasoning string, cleanedContent string) {
	if content == "" {
		return "", content
	}

	var reasoningParts []string
	var cleanedParts []string
	remaining := content

	// Define tag pairs to look for (matching llama.cpp's chat-parser.cpp)
	tagPairs := []struct {
		start string
		end   string
	}{
		{"<|START_THINKING|>", "<|END_THINKING|>"},            // Command-R models
		{"<|inner_prefix|>", "<|inner_suffix|>"},              // Apertus models
		{"<seed:think>", "</seed:think>"},                     // Seed models
		{"<think>", "</think>"},                               // DeepSeek, Granite, ExaOne models
		{"<|think|>", "<|end|><|begin|>assistant<|content|>"}, // Solar Open models (complex end)
		{"<thinking>", "</thinking>"},                         // General thinking tag
		{"[THINK]", "[/THINK]"},                               // Magistral models
	}

	// Track the last position we've processed
	lastPos := 0

	for {
		// Find the earliest tag start
		earliestStart := -1
		earliestEnd := -1
		isUnclosed := false
		var matchedTag struct {
			start string
			end   string
		}

		for _, tagPair := range tagPairs {
			startIdx := strings.Index(remaining[lastPos:], tagPair.start)
			if startIdx == -1 {
				continue
			}
			startIdx += lastPos

			// Find the corresponding end tag
			endIdx := strings.Index(remaining[startIdx+len(tagPair.start):], tagPair.end)
			if endIdx == -1 {
				// Unclosed tag - extract what we have
				if earliestStart == -1 || startIdx < earliestStart {
					earliestStart = startIdx
					earliestEnd = len(remaining)
					isUnclosed = true
					matchedTag = tagPair
				}
				continue
			}
			endIdx += startIdx + len(tagPair.start)

			// Found a complete tag pair
			if earliestStart == -1 || startIdx < earliestStart {
				earliestStart = startIdx
				earliestEnd = endIdx + len(tagPair.end)
				isUnclosed = false
				matchedTag = tagPair
			}
		}

		if earliestStart == -1 {
			// No more tags found, add remaining content
			if lastPos < len(remaining) {
				cleanedParts = append(cleanedParts, remaining[lastPos:])
			}
			break
		}

		// Add content before the tag
		if earliestStart > lastPos {
			cleanedParts = append(cleanedParts, remaining[lastPos:earliestStart])
		}

		// Extract reasoning content
		reasoningStart := earliestStart + len(matchedTag.start)
		// For unclosed tags, earliestEnd is already at the end of the string
		// For closed tags, earliestEnd points to after the closing tag, so we subtract the end tag length
		var reasoningEnd int
		if isUnclosed {
			// Unclosed tag - extract everything to the end
			reasoningEnd = len(remaining)
		} else {
			// Closed tag - exclude the end tag
			reasoningEnd = earliestEnd - len(matchedTag.end)
		}
		if reasoningEnd > reasoningStart {
			reasoningContent := strings.TrimSpace(remaining[reasoningStart:reasoningEnd])
			if reasoningContent != "" {
				reasoningParts = append(reasoningParts, reasoningContent)
			}
		}

		// Move past this tag
		lastPos = earliestEnd
	}

	// Combine reasoning parts
	reasoning = strings.Join(reasoningParts, "\n\n")
	// Combine cleaned content parts
	cleanedContent = strings.Join(cleanedParts, "")

	return reasoning, cleanedContent
}
