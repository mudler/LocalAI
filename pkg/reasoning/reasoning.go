package reasoning

import (
	"strings"
)

// Common thinking/reasoning opening tags used by various models
var thinkingOpenTags = []string{
	"<think>\n",
	"<think>",
	"<thinking>\n",
	"<thinking>",
	"<|inner_prefix|>",   // Apertus
	"<|START_THINKING|>", // Command R7B
	"<seed:think>",       // Seed
	"[THINK]\n",          // Magistral
	"[THINK]",
}

// DetectThinkingForcedOpen checks if a prompt ends with a thinking opening tag.
// This is used to automatically detect when the model template has already added
// the opening thinking tag, meaning the model will output reasoning content directly.
// Returns true if the prompt ends with a known thinking opening tag.
func DetectThinkingForcedOpen(prompt string) bool {
	for _, tag := range thinkingOpenTags {
		if strings.HasSuffix(prompt, tag) {
			return true
		}
	}
	return false
}

// Extract extracts reasoning content from thinking tags and returns
// both the extracted reasoning and the cleaned content (with tags removed).
// It handles <thinking>...</thinking> and <think>...</think> tags.
// Multiple reasoning blocks are concatenated with newlines.
// It also handles the case where only a closing tag is present (no opening tag),
// in which case everything before the closing tag is treated as reasoning.
//
// Use WithThinkingForcedOpen() option when all content from the start should be
// treated as reasoning until a closing tag is found.
func Extract(content string, opts ...Option) (reasoning string, cleanedContent string) {
	if content == "" {
		return "", content
	}

	cfg := &options{}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.thinkingForcedOpen {
		return extractForcedOpen(content)
	}

	return extractFromTags(content)
}

// extractForcedOpen handles the case where reasoning starts without an opening tag.
// All content from the start is treated as reasoning until a closing tag is found.
func extractForcedOpen(content string) (reasoning string, cleanedContent string) {
	// Look for the earliest closing tag
	closingTags := []string{"</thinking>", "</think>"}

	earliestCloseIdx := -1
	var matchedCloseTag string

	for _, closeTag := range closingTags {
		idx := strings.Index(content, closeTag)
		if idx != -1 && (earliestCloseIdx == -1 || idx < earliestCloseIdx) {
			earliestCloseIdx = idx
			matchedCloseTag = closeTag
		}
	}

	if earliestCloseIdx == -1 {
		// No closing tag found - all content is reasoning (still streaming)
		return strings.TrimSpace(content), ""
	}

	// Found closing tag - everything before is reasoning, everything after is content
	reasoning = strings.TrimSpace(content[:earliestCloseIdx])
	cleanedContent = content[earliestCloseIdx+len(matchedCloseTag):]

	// Continue processing the rest for any additional reasoning blocks
	if cleanedContent != "" {
		additionalReasoning, finalContent := extractFromTags(cleanedContent)
		if additionalReasoning != "" {
			if reasoning != "" {
				reasoning = reasoning + "\n\n" + additionalReasoning
			} else {
				reasoning = additionalReasoning
			}
		}
		cleanedContent = finalContent
	}

	return reasoning, cleanedContent
}

// extractFromTags extracts reasoning content from thinking tags.
// This is the core implementation that handles standard tag-based extraction.
func extractFromTags(content string) (reasoning string, cleanedContent string) {
	if content == "" {
		return "", content
	}

	var reasoningParts []string
	var cleanedParts []string
	remaining := content

	// Define tag pairs to look for
	tagPairs := []struct {
		start string
		end   string
	}{
		{"<thinking>", "</thinking>"},
		{"<think>", "</think>"},
	}

	// Track the last position we've processed
	lastPos := 0

	for {
		// Find the earliest tag start
		earliestStart := -1
		earliestEnd := -1
		isUnclosed := false
		isClosingOnly := false
		var matchedTag struct {
			start string
			end   string
		}

		for _, tagPair := range tagPairs {
			startIdx := strings.Index(remaining[lastPos:], tagPair.start)
			endIdx := strings.Index(remaining[lastPos:], tagPair.end)

			// Check for closing-only tag (closing tag appears before or without opening tag)
			if endIdx != -1 && (startIdx == -1 || endIdx < startIdx) {
				// Found a closing tag without a preceding opening tag
				closingTagPos := endIdx + lastPos
				if earliestStart == -1 || closingTagPos < earliestStart || (isClosingOnly && closingTagPos < earliestEnd) {
					earliestStart = lastPos
					earliestEnd = closingTagPos + len(tagPair.end)
					isClosingOnly = true
					isUnclosed = false
					matchedTag = tagPair
				}
				continue
			}

			if startIdx == -1 {
				continue
			}
			startIdx += lastPos

			// Find the corresponding end tag after the start tag
			endIdxAfterStart := strings.Index(remaining[startIdx+len(tagPair.start):], tagPair.end)
			if endIdxAfterStart == -1 {
				// Unclosed tag - extract what we have
				if earliestStart == -1 || startIdx < earliestStart {
					earliestStart = startIdx
					earliestEnd = len(remaining)
					isUnclosed = true
					isClosingOnly = false
					matchedTag = tagPair
				}
				continue
			}
			endIdxAfterStart += startIdx + len(tagPair.start)

			// Found a complete tag pair
			if earliestStart == -1 || startIdx < earliestStart {
				earliestStart = startIdx
				earliestEnd = endIdxAfterStart + len(tagPair.end)
				isUnclosed = false
				isClosingOnly = false
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

		if isClosingOnly {
			// Closing tag without opening tag - content before closing tag is reasoning
			reasoningContent := strings.TrimSpace(remaining[lastPos : earliestEnd-len(matchedTag.end)])
			if reasoningContent != "" {
				reasoningParts = append(reasoningParts, reasoningContent)
			}
			// Move past the closing tag
			lastPos = earliestEnd
			continue
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
