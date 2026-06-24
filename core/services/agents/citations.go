package agents

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
)

type kbCitationList struct {
	mu        sync.Mutex
	citations []KBCitation
}

func (l *kbCitationList) AddKBCitations(citations []KBCitation) {
	if len(citations) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.citations = append(l.citations, citations...)
}

func (l *kbCitationList) Citations() []KBCitation {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]KBCitation, len(l.citations))
	copy(out, l.citations)
	return out
}

// AppendKBCitations appends a markdown Sources block for KB citations.
func AppendKBCitations(response, collection, userID string, citations []KBCitation) string {
	if strings.TrimSpace(response) == "" || len(citations) == 0 {
		return response
	}

	var lines []string
	seen := make(map[string]struct{})
	for _, citation := range citations {
		key := strings.TrimSpace(citation.EntryKey)
		if key == "" {
			key = strings.TrimSpace(citation.FileName)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		displayName := kbCitationDisplayName(citation)
		if displayName == "" {
			continue
		}

		sourceURL := kbCitationRawFileURL(collection, citation.EntryKey, userID)
		number := len(lines) + 1
		if sourceURL == "" {
			lines = append(lines, fmt.Sprintf("[%d] %s", number, displayName))
			continue
		}
		lines = append(lines, fmt.Sprintf("[%d] [%s](%s)", number, escapeMarkdownLinkText(displayName), sourceURL))
	}

	if len(lines) == 0 {
		return response
	}

	var sb strings.Builder
	sb.WriteString(strings.TrimRight(response, "\n"))
	sb.WriteString("\n\nSources:\n")
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func kbCitationDisplayName(citation KBCitation) string {
	if fileName := strings.TrimSpace(citation.FileName); fileName != "" {
		return fileName
	}

	segments := strings.Split(strings.Trim(strings.TrimSpace(citation.EntryKey), "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if segment := strings.TrimSpace(segments[i]); segment != "" {
			return segment
		}
	}
	return ""
}

func kbCitationRawFileURL(collection, entryKey, userID string) string {
	collection = strings.TrimSpace(collection)
	entryKey = strings.Trim(strings.TrimSpace(entryKey), "/")
	if collection == "" || entryKey == "" {
		return ""
	}

	var escapedEntrySegments []string
	for segment := range strings.SplitSeq(entryKey, "/") {
		if segment == "" {
			continue
		}
		escapedEntrySegments = append(escapedEntrySegments, url.PathEscape(segment))
	}
	if len(escapedEntrySegments) == 0 {
		return ""
	}

	sourceURL := "/api/agents/collections/" + url.PathEscape(collection) + "/entries-raw/" + strings.Join(escapedEntrySegments, "/")
	if userID != "" {
		query := url.Values{}
		query.Set("user_id", userID)
		sourceURL += "?" + query.Encode()
	}
	return sourceURL
}

func escapeMarkdownLinkText(text string) string {
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, "[", `\[`)
	text = strings.ReplaceAll(text, "]", `\]`)
	return text
}
