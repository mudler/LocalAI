package websearch

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

type Searcher interface {
	Search(ctx context.Context, query string) ([]schema.UrlCitation, error)
}

type SimpleSearch struct{}

func New() *SimpleSearch {
	return &SimpleSearch{}
}

func (s *SimpleSearch) Search(ctx context.Context, query string) ([]schema.UrlCitation, error) {
	// TODO: Implement actual DuckDuckGo, Google Custom Search, or browsing logic here.
	// For now, returned a placeholder to validate the API schema flow.

	fmt.Printf("[WebSearch] Searching for :%s\n", query)

	results := []schema.UrlCitation{
		{
			Title:      "LocalAI Documentation",
			URL:        "https://localai.io",
			StartIndex: 0,
			EndIndex:   100, // Arbitrary indices for citation highlighting
		},
		{
			Title:      "Github - LocalAI",
			URL:        "https://github.com/mudler/LocalAI",
			StartIndex: 0,
			EndIndex:   0,
		},
	}
	return results, nil
}

// AugmmentedSystemPrompt adds search results to the context
func AugmmentedSystemPrompt(originalPrompt string, citations []schema.UrlCitation) string {

	var sb strings.Builder
	sb.WriteString("I found the following information from the web:\n\n")

	for i, c := range citations {
		sb.WriteString(fmt.Sprintf("[%d] %s (%s)\n", i+1, c.Title, c.URL))
	}

	sb.WriteString("\nPlease use this information to answer the user's question.\n\n")
	sb.WriteString("Original System Promt:\n")
	sb.WriteString(originalPrompt)

	return sb.String()
}
