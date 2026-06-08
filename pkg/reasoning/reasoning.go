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
// - <|channel>thought        (Gemma 4 models)
// - <|think|>               (Solar Open models)
// - <thinking>              (General thinking tag)
// - [THINK]                 (Magistral models)
// Custom tokens from config are checked first, then default tokens.
func DetectThinkingStartToken(prompt string, config *Config) string {
	// Common thinking start tokens (in order of specificity - longer first)
	// Based on llama.cpp's chat-parser.cpp implementations
	defaultTokens := []string{
		"<|START_THINKING|>", // Command-R models
		"<|channel>thought",  // Gemma 4 models (before <|think|> — Gemma 4 templates contain both)
		"<|inner_prefix|>",   // Apertus models
		"<seed:think>",       // Seed models
		"<think>",            // DeepSeek, Granite, ExaOne models
		"<|think|>",          // Solar Open models
		"<thinking>",         // General thinking tag
		"[THINK]",            // Magistral models
	}

	// Merge custom tokens with default tokens (custom tokens first for priority)
	var thinkingStartTokens []string
	if config != nil && len(config.ThinkingStartTokens) > 0 {
		thinkingStartTokens = append(thinkingStartTokens, config.ThinkingStartTokens...)
	}
	thinkingStartTokens = append(thinkingStartTokens, defaultTokens...)

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
		reasoning, cleanedContent = ExtractReasoning(cleanedContent, &config)
		if config.StripReasoningOnly != nil && *config.StripReasoningOnly {
			reasoning = ""
		}
	}

	return reasoning, cleanedContent
}

// ExtractReasoningComplete extracts reasoning from a COMPLETE (non-streaming)
// model response. It behaves like ExtractReasoningWithConfig except that it only
// honors a prefilled thinking start token when the response actually contains
// the matching closing tag.
//
// Rationale: when a chat template injects the start token into the prompt (so
// DetectThinkingStartToken returns e.g. "<think>"), the model's output begins
// inside a reasoning block and carries only the closing tag. The defensive
// fallback prepends the start token so the extractor can pair it with that
// close tag. But on a COMPLETE response with no closing tag, the model answered
// directly with no reasoning at all — prepending the start token would
// manufacture an unclosed block that swallows the entire answer into reasoning,
// leaving content empty (breaking short/direct answers such as session names or
// JSON summaries). Genuine reasoning tags already present in the content still
// extract, because dropping the synthetic prefill does not affect them.
//
// Streaming callers must keep using ExtractReasoningWithConfig: mid-stream an
// as-yet-unclosed block is legitimate and its tokens should surface as
// reasoning deltas as they arrive.
func ExtractReasoningComplete(content, thinkingStartToken string, config Config) (reasoning string, cleanedContent string) {
	startToken := thinkingStartToken
	if startToken != "" {
		if end := ClosingTokenForStart(startToken, &config); end == "" || !strings.Contains(content, end) {
			startToken = ""
		}
	}
	return ExtractReasoningWithConfig(content, startToken, config)
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

	// If content already contains the token, don't prepend
	if strings.Contains(trimmed, startToken) {
		return content
	}

	// If content is a non-empty prefix of the start token (e.g. "<|channel>"
	// accumulating toward "<|channel>thought"), don't prepend — we're still
	// receiving the tag token-by-token during streaming.
	if trimmed != "" && strings.HasPrefix(startToken, trimmed) {
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

// defaultReasoningTagPairs are the built-in start/end reasoning tag pairs,
// matching llama.cpp's chat-parser.cpp. Kept at package scope so that
// ExtractReasoning and ClosingTokenForStart share a single source of truth.
var defaultReasoningTagPairs = []TagPair{
	{Start: "<|START_THINKING|>", End: "<|END_THINKING|>"},            // Command-R models
	{Start: "<|inner_prefix|>", End: "<|inner_suffix|>"},              // Apertus models
	{Start: "<seed:think>", End: "</seed:think>"},                     // Seed models
	{Start: "<think>", End: "</think>"},                               // DeepSeek, Granite, ExaOne models
	{Start: "<|think|>", End: "<|end|><|begin|>assistant<|content|>"}, // Solar Open models (complex end)
	{Start: "<|channel>thought", End: "<channel|>"},                   // Gemma 4 models
	{Start: "<thinking>", End: "</thinking>"},                         // General thinking tag
	{Start: "[THINK]", End: "[/THINK]"},                               // Magistral models
}

// ClosingTokenForStart returns the closing reasoning tag that pairs with the
// given start token, searching custom config TagPairs first then the built-in
// defaults. Returns "" when startToken is empty or unrecognized.
//
// Used by the non-streaming autoparser fallback to decide whether a complete
// response that began with a prefilled thinking token actually closed its
// reasoning block: only then is synthesizing the start token (so the standard
// extractor can pair it with the model's close tag) safe. A complete response
// with no closing tag is a direct answer, not unclosed reasoning.
func ClosingTokenForStart(startToken string, config *Config) string {
	if startToken == "" {
		return ""
	}
	if config != nil {
		for _, pair := range config.TagPairs {
			if pair.Start == startToken {
				return pair.End
			}
		}
	}
	for _, pair := range defaultReasoningTagPairs {
		if pair.Start == startToken {
			return pair.End
		}
	}
	return ""
}

// ExtractReasoning extracts reasoning content from thinking tags and returns
// both the extracted reasoning and the cleaned content (with tags removed).
// It handles <thinking>...</thinking> and <think>...</think> tags.
// Multiple reasoning blocks are concatenated with newlines.
// Custom tag pairs from config are checked first, then default tag pairs.
func ExtractReasoning(content string, config *Config) (reasoning string, cleanedContent string) {
	if content == "" {
		return "", content
	}

	var reasoningParts []string
	var cleanedParts []string
	remaining := content

	// Merge custom tag pairs (highest priority) with the built-in defaults.
	var tagPairs []struct {
		start string
		end   string
	}
	if config != nil && len(config.TagPairs) > 0 {
		for _, pair := range config.TagPairs {
			if pair.Start != "" && pair.End != "" {
				tagPairs = append(tagPairs, struct {
					start string
					end   string
				}{pair.Start, pair.End})
			}
		}
	}
	for _, pair := range defaultReasoningTagPairs {
		tagPairs = append(tagPairs, struct {
			start string
			end   string
		}{pair.Start, pair.End})
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
