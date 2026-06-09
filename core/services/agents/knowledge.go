package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Metadata keys populated by localrecall for every stored chunk. The original
// upload file name lives under file_name (used for display); source holds the
// collection entry key ("<uuid>/<filename>") used to build the raw-file URL.
const (
	kbMetadataFileName = "file_name"
	kbMetadataSource   = "source"
)

// KBSearchResult represents a search result from the knowledge base.
// Field names mirror the collection search endpoint's JSON response.
type KBSearchResult struct {
	Content    string            `json:"content"`
	ID         string            `json:"id"`
	Similarity float64           `json:"similarity"`
	Metadata   map[string]string `json:"metadata"`
}

// kbSearchResponse is the wrapper returned by the collection search endpoint.
type kbSearchResponse struct {
	Results []KBSearchResult `json:"results"`
	Count   int              `json:"count"`
}

// KBCitation is a single source document that a KB search drew from. Citations
// travel alongside the prompt as structured data so the consumer (and UI) can
// render clickable source links, independent of what the model writes inline.
type KBCitation struct {
	// FileName is the original uploaded file name, for display (e.g. "report.pdf").
	FileName string `json:"file_name"`
	// EntryKey is the collection entry identifier ("<uuid>/<filename>"), used to
	// build the raw-file URL and as the de-duplication key.
	EntryKey string `json:"entry_key"`
}

// KBSearchContext is the result of an auto-search against the knowledge base:
// the system-prompt block to feed the model, plus the de-duplicated list of
// source documents the results were drawn from.
type KBSearchContext struct {
	Prompt    string       `json:"prompt"`
	Citations []KBCitation `json:"citations"`
}

// KBCitationCollector receives source citations found during KB searches.
type KBCitationCollector interface {
	AddKBCitations([]KBCitation)
}

// KBAutoSearchPrompt queries the knowledge base with the user's message and
// returns a KBSearchContext: a system prompt block with the relevant results
// plus the de-duplicated source citations those results came from.
// Uses LocalAI's collection search endpoint via the API.
func KBAutoSearchPrompt(ctx context.Context, apiURL, apiKey, collection, query string, maxResults int, userID string) KBSearchContext {
	if collection == "" || query == "" {
		return KBSearchContext{}
	}

	if maxResults <= 0 {
		maxResults = 5
	}

	searchURL := strings.TrimRight(apiURL, "/") + "/api/agents/collections/" + url.PathEscape(collection) + "/search"
	if userID != "" {
		query := url.Values{}
		query.Set("user_id", userID)
		searchURL += "?" + query.Encode()
	}
	reqBody, _ := json.Marshal(map[string]any{
		"query":       query,
		"max_results": maxResults,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, strings.NewReader(string(reqBody)))
	if err != nil {
		xlog.Warn("KB auto-search: failed to create request", "error", err)
		return KBSearchContext{}
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpclient.New().Do(req)
	if err != nil {
		xlog.Warn("KB auto-search: request failed", "error", err)
		return KBSearchContext{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		xlog.Warn("KB auto-search: non-200 response", "status", resp.StatusCode, "body", string(body))
		return KBSearchContext{}
	}

	var searchResp kbSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		xlog.Warn("KB auto-search: failed to decode response", "error", err)
		return KBSearchContext{}
	}

	if len(searchResp.Results) == 0 {
		return KBSearchContext{}
	}

	// Build the system prompt block, labelling each chunk with its source file
	// so the model can attribute inline, and collect the structured citations.
	var sb strings.Builder
	sb.WriteString("Given the user input you have the following in memory:\n")

	var citations []KBCitation
	seen := make(map[string]struct{})

	for _, r := range searchResp.Results {
		fileName := r.Metadata[kbMetadataFileName]
		source := r.Metadata[kbMetadataSource]

		label := fileName
		if label == "" {
			label = "unknown"
		}
		sb.WriteString(fmt.Sprintf("[Source: %s]\n%s\n", label, r.Content))

		// Citations are de-duplicated per source document: many chunks from the
		// same file share one source key, so a file is listed only once. Skip
		// results with no source key — they cannot be linked back to a document.
		dedupKey := source
		if dedupKey == "" {
			dedupKey = fileName
		}
		if dedupKey == "" {
			continue
		}
		if _, ok := seen[dedupKey]; ok {
			continue
		}
		seen[dedupKey] = struct{}{}
		citations = append(citations, KBCitation{
			FileName: fileName,
			EntryKey: source,
		})
	}

	sb.WriteString("When answering, cite sources using [Source: filename].")

	return KBSearchContext{
		Prompt:    sb.String(),
		Citations: citations,
	}
}

// KBSearchMemoryArgs defines the arguments for the search_memory tool.
type KBSearchMemoryArgs struct {
	Query string `json:"query" jsonschema:"description=The search query to find relevant information in memory"`
}

// KBSearchMemoryTool implements the search_memory MCP tool.
type KBSearchMemoryTool struct {
	APIURL            string
	APIKey            string
	Collection        string
	MaxResults        int
	UserID            string
	CitationCollector KBCitationCollector
}

func (t KBSearchMemoryTool) Run(args KBSearchMemoryArgs) (string, any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := KBAutoSearchPrompt(ctx, t.APIURL, t.APIKey, t.Collection, args.Query, t.MaxResults, t.UserID)
	if result.Prompt == "" {
		return "No results found.", nil, nil
	}
	if t.CitationCollector != nil {
		t.CitationCollector.AddKBCitations(result.Citations)
	}
	return result.Prompt, nil, nil
}

// KBAddMemoryArgs defines the arguments for the add_memory tool.
type KBAddMemoryArgs struct {
	Content string `json:"content" jsonschema:"description=The content to store in memory for later retrieval"`
}

// KBAddMemoryTool implements the add_memory MCP tool.
type KBAddMemoryTool struct {
	APIURL     string
	APIKey     string
	Collection string
	UserID     string
}

func (t KBAddMemoryTool) Run(args KBAddMemoryArgs) (string, any, error) {
	if args.Content == "" {
		return "No content provided.", nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := KBStoreContent(ctx, t.APIURL, t.APIKey, t.Collection, args.Content, t.UserID)
	if err != nil {
		xlog.Warn("add_memory: failed to store content", "error", err, "collection", t.Collection)
		return fmt.Sprintf("Failed to store content: %v", err), nil, nil
	}
	return "Content stored in memory.", nil, nil
}

// KBStoreContent uploads text content to a collection via the multipart upload API.
func KBStoreContent(ctx context.Context, apiURL, apiKey, collection, content, userID string) error {
	uploadURL := strings.TrimRight(apiURL, "/") + "/api/agents/collections/" + url.PathEscape(collection) + "/upload"
	if userID != "" {
		query := url.Values{}
		query.Set("user_id", userID)
		uploadURL += "?" + query.Encode()
	}

	// Build multipart form with the text content as a file
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	filename := fmt.Sprintf("memory_%d.txt", time.Now().UnixNano())
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, strings.NewReader(content)); err != nil {
		return fmt.Errorf("failed to write content: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpclient.New().Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// saveConversationToKB stores conversation content in the agent's KB collection
// based on the configured storage mode and summary settings.
func saveConversationToKB(ctx context.Context, llm cogito.LLM, apiURL, apiKey string, cfg *AgentConfig, userMessage, assistantResponse, userID string) {
	if apiURL == "" || cfg.Name == "" {
		return
	}

	mode := cfg.ConversationStorageMode
	if mode == "" {
		mode = ConvStorageUserOnly
	}

	// If summary mode is enabled, summarize the conversation first
	if cfg.SummaryLongTermMemory {
		summary := summarizeConversation(ctx, llm, userMessage, assistantResponse)
		if summary != "" {
			if err := KBStoreContent(ctx, apiURL, apiKey, cfg.Name, summary, userID); err != nil {
				xlog.Warn("Failed to store conversation summary in KB", "agent", cfg.Name, "error", err)
			}
		}
		return
	}

	switch mode {
	case ConvStorageUserOnly:
		if err := KBStoreContent(ctx, apiURL, apiKey, cfg.Name, userMessage, userID); err != nil {
			xlog.Warn("Failed to store user message in KB", "agent", cfg.Name, "error", err)
		}
	case ConvStorageUserAndAssistant:
		if err := KBStoreContent(ctx, apiURL, apiKey, cfg.Name, "User: "+userMessage, userID); err != nil {
			xlog.Warn("Failed to store user message in KB", "agent", cfg.Name, "error", err)
		}
		if err := KBStoreContent(ctx, apiURL, apiKey, cfg.Name, "Assistant: "+assistantResponse, userID); err != nil {
			xlog.Warn("Failed to store assistant response in KB", "agent", cfg.Name, "error", err)
		}
	case ConvStorageWholeConversation:
		block := "User: " + userMessage + "\nAssistant: " + assistantResponse
		if err := KBStoreContent(ctx, apiURL, apiKey, cfg.Name, block, userID); err != nil {
			xlog.Warn("Failed to store conversation in KB", "agent", cfg.Name, "error", err)
		}
	}
}

// summarizeConversation uses the LLM to summarize a conversation exchange.
func summarizeConversation(ctx context.Context, llm cogito.LLM, userMessage, assistantResponse string) string {
	prompt := fmt.Sprintf(
		"Summarize the conversation below, keep the highlights as a bullet list:\n\nUser: %s\nAssistant: %s",
		userMessage, assistantResponse,
	)

	fragment := cogito.NewEmptyFragment().
		AddMessage(cogito.SystemMessageRole, "You are a helpful summarizer. Produce a concise bullet-point summary.").
		AddMessage(cogito.UserMessageRole, prompt)

	result, err := cogito.ExecuteTools(llm, fragment, cogito.WithContext(ctx))
	if err != nil {
		xlog.Warn("Failed to summarize conversation", "error", err)
		return ""
	}

	if len(result.Messages) > 0 {
		return result.Messages[len(result.Messages)-1].Content
	}
	return ""
}
