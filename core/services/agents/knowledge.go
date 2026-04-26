package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
)

// KBSearchResult represents a search result from the knowledge base.
type KBSearchResult struct {
	Content    string            `json:"content"`
	Score      float64           `json:"score"`
	Similarity float64           `json:"similarity"`
	Metadata   map[string]string `json:"metadata"`
}

// kbSearchResponse is the wrapper returned by the collection search endpoint.
type kbSearchResponse struct {
	Results []KBSearchResult `json:"results"`
	Count   int              `json:"count"`
}

// KBAutoSearchPrompt queries the knowledge base with the user's message
// and returns a system prompt block with relevant results.
// Uses LocalAI's collection search endpoint via the API.
func KBAutoSearchPrompt(ctx context.Context, apiURL, apiKey, collection, query string, maxResults int, userID string) string {
	if collection == "" || query == "" {
		return ""
	}

	if maxResults <= 0 {
		maxResults = 5
	}

	// Call LocalAI's collection search API
	searchURL := strings.TrimRight(apiURL, "/") + "/api/agents/collections/" + collection + "/search"
	if userID != "" {
		searchURL += "?user_id=" + userID
	}
	reqBody, _ := json.Marshal(map[string]any{
		"query":       query,
		"max_results": maxResults,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, strings.NewReader(string(reqBody)))
	if err != nil {
		xlog.Warn("KB auto-search: failed to create request", "error", err)
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		xlog.Warn("KB auto-search: request failed", "error", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		xlog.Warn("KB auto-search: non-200 response", "status", resp.StatusCode, "body", string(body))
		return ""
	}

	var searchResp kbSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		xlog.Warn("KB auto-search: failed to decode response", "error", err)
		return ""
	}

	if len(searchResp.Results) == 0 {
		return ""
	}

	// Format results as a system prompt block (same format as LocalAGI)
	var sb strings.Builder
	sb.WriteString("Given the user input you have the following in memory:\n")
	for i, r := range searchResp.Results {
		sb.WriteString(fmt.Sprintf("- %s", r.Content))
		if len(r.Metadata) > 0 {
			meta, _ := json.Marshal(r.Metadata)
			sb.WriteString(fmt.Sprintf(" (%s)", string(meta)))
		}
		if i < len(searchResp.Results)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// KBSearchMemoryArgs defines the arguments for the search_memory tool.
type KBSearchMemoryArgs struct {
	Query string `json:"query" jsonschema:"description=The search query to find relevant information in memory"`
}

// KBSearchMemoryTool implements the search_memory MCP tool.
type KBSearchMemoryTool struct {
	APIURL     string
	APIKey     string
	Collection string
	MaxResults int
	UserID     string
}

func (t KBSearchMemoryTool) Run(args KBSearchMemoryArgs) (string, any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := KBAutoSearchPrompt(ctx, t.APIURL, t.APIKey, t.Collection, args.Query, t.MaxResults, t.UserID)
	if result == "" {
		return "No results found.", nil, nil
	}
	return result, nil, nil
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
	uploadURL := strings.TrimRight(apiURL, "/") + "/api/agents/collections/" + collection + "/upload"
	if userID != "" {
		uploadURL += "?user_id=" + userID
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

	resp, err := http.DefaultClient.Do(req)
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
