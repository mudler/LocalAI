package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// OpenAI Chat Completions wire-format types. Narrowed to the fields
// translate mode needs to preserve through the Reply proto: content,
// role, tool_calls (typed so we can map them to pb.ToolCallDelta),
// and sampling params copied verbatim from PredictOptions.
//
// Provider-specific extensions (logit_bias, function calling beyond
// tool_calls, etc.) are not modelled — passthrough mode covers callers
// that need full upstream fidelity.

type openAIRequest struct {
	Model            string          `json:"model"`
	Messages         []openAIMessage `json:"messages"`
	Stream           bool            `json:"stream,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	MaxTokens        *int32          `json:"max_tokens,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	Tools            json.RawMessage `json:"tools,omitempty"`
	ToolChoice       json.RawMessage `json:"tool_choice,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

// openAIToolCall covers both the non-streaming response shape (full
// id+function+arguments) and the streaming-delta shape (sparse fields,
// index assignment). The proto's ToolCallDelta absorbs both — name is
// set on first appearance, arguments arrive incrementally in streaming.
type openAIToolCall struct {
	Index    int                `json:"index,omitempty"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openAIFunctionCall `json:"function,omitempty"`
}

type openAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIResponse struct {
	ID      string         `json:"id"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIStreamChoice struct {
	Index int `json:"index"`
	Delta struct {
		Content   string           `json:"content,omitempty"`
		Role      string           `json:"role,omitempty"`
		ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type openAIStreamChunk struct {
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// buildOpenAIRequest converts pb.PredictOptions into the OpenAI Chat
// Completions request body. Prefers Messages when non-empty; falls
// back to wrapping Prompt as a single user message so plain
// /completions-style calls still work in translate mode.
func buildOpenAIRequest(opts *pb.PredictOptions, cfg *proxyConfig, stream bool) ([]byte, error) {
	req := openAIRequest{
		Model:      modelName(cfg, opts),
		Stream:     stream,
		Stop:       opts.GetStopPrompts(),
		Tools:      parseRawJSON(opts.GetTools()),
		ToolChoice: parseRawJSON(opts.GetToolChoice()),
	}
	if t := opts.GetTemperature(); t != 0 {
		v := float64(t)
		req.Temperature = &v
	}
	if t := opts.GetTopP(); t != 0 {
		v := float64(t)
		req.TopP = &v
	}
	if n := opts.GetTokens(); n > 0 {
		req.MaxTokens = &n
	}
	if p := opts.GetFrequencyPenalty(); p != 0 {
		v := float64(p)
		req.FrequencyPenalty = &v
	}
	if p := opts.GetPresencePenalty(); p != 0 {
		v := float64(p)
		req.PresencePenalty = &v
	}

	for _, m := range opts.GetMessages() {
		msg := openAIMessage{
			Role:       m.GetRole(),
			Content:    m.GetContent(),
			Name:       m.GetName(),
			ToolCallID: m.GetToolCallId(),
		}
		// Pre-existing tool_calls arrive as a JSON string from the
		// upstream caller's previous assistant turn; pass-through as-is.
		if tc := m.GetToolCalls(); tc != "" {
			_ = json.Unmarshal([]byte(tc), &msg.ToolCalls)
		}
		req.Messages = append(req.Messages, msg)
	}
	// Fallback for plain Prompt requests (no Messages array). LocalAI
	// templating may have produced a flat prompt; rewrap as a single
	// user message so the upstream chat endpoint accepts it.
	if len(req.Messages) == 0 && opts.GetPrompt() != "" {
		req.Messages = []openAIMessage{{Role: "user", Content: opts.GetPrompt()}}
	}

	return json.Marshal(req)
}

// modelName picks the upstream model: upstream_model from the proxy
// config wins (operator override), else the local model name captured
// at LoadModel time. Operator sets upstream_model to map LocalAI's
// alias (e.g. "claude-strict") to the upstream's canonical name
// (e.g. "claude-3-5-sonnet-20241022").
func modelName(cfg *proxyConfig, _ *pb.PredictOptions) string {
	if cfg.upstreamModel != "" {
		return cfg.upstreamModel
	}
	return cfg.localModel
}

// parseRawJSON parses a JSON string into a RawMessage so it round-trips
// into the upstream body. Returns nil for empty/invalid input so the
// field is omitted (omitempty).
func parseRawJSON(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	var probe json.RawMessage
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return nil
	}
	return probe
}

// doOpenAIRequest builds + sends the upstream request. Returns the
// raw response on success; caller handles status / body.
func (c *CloudProxy) doOpenAIRequest(ctx context.Context, cfg *proxyConfig, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cloud-proxy: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	if cfg.apiKey != "" {
		applyAuthHeader(req, cfg.provider, cfg.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloud-proxy: upstream request: %w", err)
	}
	return resp, nil
}

// predictOpenAIRich is the non-streaming translate path. Returns a
// fully-populated *pb.Reply with assistant content, tool calls, and
// token usage. The gRPC server forwards the Reply verbatim.
func (c *CloudProxy) predictOpenAIRich(ctx context.Context, cfg *proxyConfig, opts *pb.PredictOptions) (*pb.Reply, error) {
	body, err := buildOpenAIRequest(opts, cfg, false)
	if err != nil {
		return nil, fmt.Errorf("cloud-proxy: marshal request: %w", err)
	}
	resp, err := c.doOpenAIRequest(ctx, cfg, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("cloud-proxy: upstream %d: %s", resp.StatusCode, string(errBody))
	}

	var parsed openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("cloud-proxy: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("cloud-proxy: upstream returned no choices")
	}

	choice := parsed.Choices[0]
	reply := &pb.Reply{
		Message: []byte(choice.Message.Content),
	}
	if parsed.Usage != nil {
		reply.PromptTokens = int32(parsed.Usage.PromptTokens)
		reply.Tokens = int32(parsed.Usage.CompletionTokens)
	}
	if len(choice.Message.ToolCalls) > 0 {
		// Non-streaming: a single ChatDelta carries the full tool-call
		// set. Index/Name/Arguments are populated together; downstream
		// consumers don't need to assemble streaming deltas.
		delta := &pb.ChatDelta{}
		for _, tc := range choice.Message.ToolCalls {
			delta.ToolCalls = append(delta.ToolCalls,
				newToolCallDelta(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments))
		}
		reply.ChatDeltas = []*pb.ChatDelta{delta}
	}
	return reply, nil
}

// predictOpenAIStreamRich streams *pb.Reply chunks. Each chunk carries
// either a content delta (Message + ChatDeltas[].Content) or tool-call
// deltas (ChatDeltas[].ToolCalls). The final Reply carries usage tokens
// when the upstream sends them (stream_options.include_usage).
func (c *CloudProxy) predictOpenAIStreamRich(ctx context.Context, cfg *proxyConfig, opts *pb.PredictOptions, results chan<- *pb.Reply) error {
	body, err := buildOpenAIRequest(opts, cfg, true)
	if err != nil {
		return fmt.Errorf("cloud-proxy: marshal request: %w", err)
	}
	resp, err := c.doOpenAIRequest(ctx, cfg, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("cloud-proxy: upstream %d: %s", resp.StatusCode, string(errBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			xlog.Debug("cloud-proxy: skip malformed SSE chunk", "error", err)
			continue
		}
		// Usage frames may arrive separately from content frames when
		// stream_options.include_usage is set; emit a usage-only Reply
		// in that case so the consumer sees the totals.
		if chunk.Usage != nil && len(chunk.Choices) == 0 {
			if !sendReply(ctx, results, &pb.Reply{
				PromptTokens: int32(chunk.Usage.PromptTokens),
				Tokens:       int32(chunk.Usage.CompletionTokens),
			}) {
				return ctx.Err()
			}
			continue
		}
		for _, ch := range chunk.Choices {
			reply := &pb.Reply{}
			if ch.Delta.Content != "" {
				reply.Message = []byte(ch.Delta.Content)
				reply.ChatDeltas = []*pb.ChatDelta{{Content: ch.Delta.Content}}
			}
			if len(ch.Delta.ToolCalls) > 0 {
				if len(reply.ChatDeltas) == 0 {
					reply.ChatDeltas = []*pb.ChatDelta{{}}
				}
				for _, tc := range ch.Delta.ToolCalls {
					reply.ChatDeltas[0].ToolCalls = append(reply.ChatDeltas[0].ToolCalls,
						newToolCallDelta(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments))
				}
			}
			if reply.Message == nil && len(reply.ChatDeltas) == 0 {
				continue
			}
			if !sendReply(ctx, results, reply) {
				return ctx.Err()
			}
		}
	}
	return scanner.Err()
}
