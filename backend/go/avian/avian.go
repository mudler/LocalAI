package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

const (
	defaultBaseURL = "https://api.avian.io/v1"
)

type Avian struct {
	base.SingleThread

	apiKey  string
	baseURL string
	model   string
}

// chatMessage represents an OpenAI-compatible chat message.
type chatMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// chatRequest represents an OpenAI-compatible chat completion request.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float32       `json:"temperature,omitempty"`
	TopP        float32       `json:"top_p,omitempty"`
	Stream      bool          `json:"stream"`
	Stop        []string      `json:"stop,omitempty"`
}

// chatChoice represents a single choice in a chat completion response.
type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// chatUsage represents token usage in a chat completion response.
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// chatResponse represents an OpenAI-compatible chat completion response.
type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

// streamDelta represents the delta in a streaming response chunk.
type streamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// streamChoice represents a choice in a streaming response chunk.
type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// streamChunk represents a single chunk in a streaming response.
type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *chatUsage     `json:"usage,omitempty"`
}

func (a *Avian) Load(opts *pb.ModelOptions) error {
	a.apiKey = os.Getenv("AVIAN_API_KEY")
	if a.apiKey == "" {
		return fmt.Errorf("AVIAN_API_KEY environment variable is required")
	}

	a.baseURL = os.Getenv("AVIAN_API_BASE")
	if a.baseURL == "" {
		a.baseURL = defaultBaseURL
	}

	a.model = opts.Model
	if a.model == "" {
		return fmt.Errorf("model name is required")
	}

	return nil
}

func (a *Avian) buildMessages(opts *pb.PredictOptions) []chatMessage {
	// If structured messages are provided (from chat completions), use them directly
	if len(opts.Messages) > 0 {
		messages := make([]chatMessage, len(opts.Messages))
		for i, msg := range opts.Messages {
			messages[i] = chatMessage{
				Role:       msg.Role,
				Content:    msg.Content,
				Name:       msg.Name,
				ToolCallID: msg.ToolCallId,
			}
		}
		return messages
	}

	// Fall back to using the prompt as a single user message
	return []chatMessage{
		{Role: "user", Content: opts.Prompt},
	}
}

func (a *Avian) Predict(opts *pb.PredictOptions) (string, error) {
	reqBody := chatRequest{
		Model:    a.model,
		Messages: a.buildMessages(opts),
		Stream:   false,
	}

	if opts.Tokens > 0 {
		reqBody.MaxTokens = int(opts.Tokens)
	}
	if opts.Temperature > 0 {
		reqBody.Temperature = opts.Temperature
	}
	if opts.TopP > 0 {
		reqBody.TopP = opts.TopP
	}
	if len(opts.StopPrompts) > 0 {
		reqBody.Stop = opts.StopPrompts
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", a.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

func (a *Avian) PredictStream(opts *pb.PredictOptions, results chan string) error {
	reqBody := chatRequest{
		Model:    a.model,
		Messages: a.buildMessages(opts),
		Stream:   true,
	}

	if opts.Tokens > 0 {
		reqBody.MaxTokens = int(opts.Tokens)
	}
	if opts.Temperature > 0 {
		reqBody.Temperature = opts.Temperature
	}
	if opts.TopP > 0 {
		reqBody.TopP = opts.TopP
	}
	if len(opts.StopPrompts) > 0 {
		reqBody.Stop = opts.StopPrompts
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		close(results)
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", a.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		close(results)
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	go func() {
		defer close(results)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "avian: stream request failed: %v\n", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "avian: API returned status %d: %s\n", resp.StatusCode, string(body))
			return
		}

		// Read SSE stream
		buf := make([]byte, 4096)
		var lineBuf strings.Builder

		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				lineBuf.Write(buf[:n])

				// Process complete lines
				for {
					text := lineBuf.String()
					idx := strings.Index(text, "\n")
					if idx < 0 {
						break
					}

					line := strings.TrimSpace(text[:idx])
					lineBuf.Reset()
					lineBuf.WriteString(text[idx+1:])

					if line == "" || line == "data: [DONE]" {
						continue
					}

					if !strings.HasPrefix(line, "data: ") {
						continue
					}

					data := strings.TrimPrefix(line, "data: ")

					var chunk streamChunk
					if jsonErr := json.Unmarshal([]byte(data), &chunk); jsonErr != nil {
						continue
					}

					if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
						results <- chunk.Choices[0].Delta.Content
					}
				}
			}

			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "avian: stream read error: %v\n", err)
				}
				break
			}
		}
	}()

	return nil
}
