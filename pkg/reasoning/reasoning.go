package reasoning

import (
	"strings"
)

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
