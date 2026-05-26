package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// Anthropic Messages API wire-format types. Narrowed to what translate
// mode preserves through the Reply proto: text + tool_use blocks +
// usage tokens. Image blocks, prompt caching, metadata, and stop
// sequence metadata are not modelled — passthrough mode covers those.
//
// Notable differences from OpenAI:
//   - max_tokens is REQUIRED. Anthropic 400s without it.
//   - Roles are user/assistant only — system messages move to a
//     top-level `system` string field.
//   - Streaming SSE uses event: lines alongside data: lines. The
//     events we care about: content_block_start (carries tool_use
//     init: id + name), content_block_delta (text_delta with text;
//     input_json_delta with partial_json for tool arguments), and
//     message_stop (terminates the stream). Others are ignored.

type anthropicRequest struct {
	Model         string               `json:"model"`
	MaxTokens     int32                `json:"max_tokens"`
	System        string               `json:"system,omitempty"`
	Messages      []anthropicMessage   `json:"messages"`
	Stream        bool                 `json:"stream,omitempty"`
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Tools         []anthropicTool      `json:"tools,omitempty"`
	ToolChoice    *anthropicToolChoice `json:"tool_choice,omitempty"`
}

// Content is `any` because Anthropic accepts a bare string OR a
// list of content blocks. Use the string form for plain user/
// assistant turns; switch to []anthropicContentBlock when the
// turn needs tool_use (assistant) or tool_result (user) blocks.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicToolChoice mirrors the four shapes Anthropic accepts:
// {"type":"auto"} | {"type":"any"} | {"type":"tool","name":"X"} |
// {"type":"none"} (newer models). OpenAI's "auto"/"none"/
// "required"/{"function":{"name":"X"}} all map here.
type anthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// anthropicContentBlock is the union shape used both for response
// blocks (text/tool_use we read off the wire) and outbound request
// blocks (tool_use/tool_result we emit in the conversation history).
// Anthropic encodes tool calls inline rather than as a separate field,
// so we walk Content[] looking for type=="tool_use" on responses and
// produce equivalent blocks when serialising prior-turn tool calls.
type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// Tool-result block fields. tool_result uses `content` (not
	// `text`) and pairs with `tool_use_id`; modelling them as
	// distinct fields avoids ambiguity at marshal time.
	ToolUseID     string `json:"tool_use_id,omitempty"`
	ResultContent string `json:"content,omitempty"`
}

type anthropicResponse struct {
	ID      string                  `json:"id"`
	Type    string                  `json:"type"`
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
	Model   string                  `json:"model"`
	Usage   *anthropicUsage         `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicStreamEvent is the union shape used for every event type we
// process. Type discriminates; only the matching fields are populated.
// content_block_start carries ContentBlock (with id/name for tool_use);
// content_block_delta carries Delta (text or partial_json).
type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        *anthropicStreamDelta  `json:"delta,omitempty"`
	Message      *anthropicResponse     `json:"message,omitempty"`
	Usage        *anthropicUsage        `json:"usage,omitempty"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// Anthropic requires max_tokens. If the caller didn't set it, use a
// generous-but-bounded default so the request doesn't 400.
const anthropicDefaultMaxTokens int32 = 4096

const anthropicToolChoiceNone = "none"

// Reused JSON-Schema defaults for malformed inputs. Anthropic requires
// input_schema to be a JSON object and tool_use.input to be a JSON
// object; clients that omit them must not 400 the entire request.
var (
	emptyJSONObject   = json.RawMessage(`{}`)
	emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{}}`)
)

func buildAnthropicRequest(opts *pb.PredictOptions, cfg *proxyConfig, stream bool) ([]byte, error) {
	req := anthropicRequest{
		Model:         modelName(cfg, opts),
		MaxTokens:     opts.GetTokens(),
		Stream:        stream,
		StopSequences: opts.GetStopPrompts(),
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = anthropicDefaultMaxTokens
	}
	// Newer Anthropic models 400 when both temperature and top_p are
	// set ("`temperature` and `top_p` cannot both be specified for
	// this model. Please use only one.") even though their docs only
	// "recommend" picking one. The OpenAI-compatible chat UI almost
	// always sends both with default values, so prefer temperature
	// and drop top_p when both are present.
	if t := opts.GetTemperature(); t != 0 {
		v := float64(t)
		req.Temperature = &v
	} else if t := opts.GetTopP(); t != 0 {
		v := float64(t)
		req.TopP = &v
	}

	req.Tools = convertOpenAITools(opts.GetTools())
	req.ToolChoice = convertOpenAIToolChoice(opts.GetToolChoice())
	// Anthropic rejects tool_choice without tools and older models
	// don't accept {"type":"none"} — collapse to a no-tools request.
	if req.ToolChoice != nil && req.ToolChoice.Type == anthropicToolChoiceNone {
		req.Tools, req.ToolChoice = nil, nil
	}

	var systemParts []string
	for _, m := range opts.GetMessages() {
		role := m.GetRole()
		if role == "system" {
			if c := m.GetContent(); c != "" {
				systemParts = append(systemParts, c)
			}
			continue
		}
		switch role {
		case "user":
			req.Messages = append(req.Messages, anthropicMessage{
				Role:    "user",
				Content: m.GetContent(),
			})
		case "assistant":
			if blocks := assistantBlocks(m); blocks != nil {
				req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: blocks})
				continue
			}
			req.Messages = append(req.Messages, anthropicMessage{
				Role:    "assistant",
				Content: m.GetContent(),
			})
		case "tool", "function":
			req.Messages = appendToolResult(req.Messages, anthropicContentBlock{
				Type:          "tool_result",
				ToolUseID:     m.GetToolCallId(),
				ResultContent: m.GetContent(),
			})
		}
	}
	req.System = strings.Join(systemParts, "\n\n")

	if len(req.Messages) == 0 && opts.GetPrompt() != "" {
		req.Messages = []anthropicMessage{{Role: "user", Content: opts.GetPrompt()}}
	}

	return json.Marshal(req)
}

// appendToolResult appends a tool_result block as a user message,
// merging into a preceding user message that already carries blocks.
// Anthropic concatenates consecutive same-role messages on its end,
// but explicit merging keeps the body smaller and the conversation
// strictly alternating — which some upstream filters require.
func appendToolResult(msgs []anthropicMessage, block anthropicContentBlock) []anthropicMessage {
	if n := len(msgs); n > 0 && msgs[n-1].Role == "user" {
		if existing, ok := msgs[n-1].Content.([]anthropicContentBlock); ok {
			msgs[n-1].Content = append(existing, block)
			return msgs
		}
	}
	return append(msgs, anthropicMessage{
		Role:    "user",
		Content: []anthropicContentBlock{block},
	})
}

func convertOpenAITools(toolsJSON string) []anthropicTool {
	if toolsJSON == "" {
		return nil
	}
	var raw []openAITool
	if err := json.Unmarshal([]byte(toolsJSON), &raw); err != nil {
		xlog.Warn("cloud-proxy: anthropic translate: unparseable tools JSON, dropping", "error", err)
		return nil
	}
	tools := make([]anthropicTool, 0, len(raw))
	for _, t := range raw {
		if t.Function.Name == "" {
			continue
		}
		schema := t.Function.Parameters
		if len(schema) == 0 {
			schema = emptyObjectSchema
		}
		tools = append(tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	return tools
}

// convertOpenAIToolChoice accepts the spec form
// ({type:function, function:{name:X}}) and the flat legacy form
// ({type:function, name:X}) some clients send. Unknown object shapes
// are warned and dropped rather than silently treated as auto.
func convertOpenAIToolChoice(toolChoiceJSON string) *anthropicToolChoice {
	if toolChoiceJSON == "" {
		return nil
	}
	var asString string
	if err := json.Unmarshal([]byte(toolChoiceJSON), &asString); err == nil {
		switch asString {
		case "auto":
			return &anthropicToolChoice{Type: "auto"}
		case "none":
			return &anthropicToolChoice{Type: anthropicToolChoiceNone}
		case "required":
			return &anthropicToolChoice{Type: "any"}
		}
		return nil
	}
	var asObj struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(toolChoiceJSON), &asObj); err != nil {
		xlog.Warn("cloud-proxy: anthropic translate: unparseable tool_choice, dropping", "error", err)
		return nil
	}
	if name := asObj.Function.Name; name != "" {
		return &anthropicToolChoice{Type: "tool", Name: name}
	}
	if asObj.Name != "" {
		return &anthropicToolChoice{Type: "tool", Name: asObj.Name}
	}
	xlog.Warn("cloud-proxy: anthropic translate: unrecognised tool_choice shape, dropping", "shape", toolChoiceJSON)
	return nil
}

// openAITool mirrors pkg/functions.Tool but keeps Parameters as
// json.RawMessage so the input_schema passes through verbatim — no
// re-marshal cost, no fidelity loss on exotic schemas.
type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

func assistantBlocks(m *pb.Message) []anthropicContentBlock {
	toolCallsJSON := m.GetToolCalls()
	if toolCallsJSON == "" {
		return nil
	}
	var toolCalls []openAIToolCall
	if err := json.Unmarshal([]byte(toolCallsJSON), &toolCalls); err != nil || len(toolCalls) == 0 {
		return nil
	}
	blocks := make([]anthropicContentBlock, 0, len(toolCalls)+1)
	if text := m.GetContent(); text != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
	}
	for _, tc := range toolCalls {
		// OpenAI's arguments are a JSON-encoded string; pass through
		// as RawMessage so a non-JSON string from a poorly-formed
		// local model doesn't crash the marshaller downstream.
		args := json.RawMessage(tc.Function.Arguments)
		if len(args) == 0 {
			args = emptyJSONObject
		}
		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: args,
		})
	}
	return blocks
}

// doAnthropicRequest is the Anthropic counterpart of doOpenAIRequest.
// applyAuthHeader sets x-api-key and anthropic-version when provider
// is anthropic, so this method doesn't need to duplicate that.
func (c *CloudProxy) doAnthropicRequest(ctx context.Context, cfg *proxyConfig, body []byte) (*http.Response, error) {
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

// predictAnthropicRich returns the full Reply: joined text from all
// text blocks, tool_use blocks mapped to ToolCallDelta, and usage
// tokens.
func (c *CloudProxy) predictAnthropicRich(ctx context.Context, cfg *proxyConfig, opts *pb.PredictOptions) (*pb.Reply, error) {
	body, err := buildAnthropicRequest(opts, cfg, false)
	if err != nil {
		return nil, fmt.Errorf("cloud-proxy: marshal request: %w", err)
	}
	resp, err := c.doAnthropicRequest(ctx, cfg, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("cloud-proxy: upstream %d: %s", resp.StatusCode, string(errBody))
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("cloud-proxy: decode response: %w", err)
	}

	reply := &pb.Reply{}
	if parsed.Usage != nil {
		reply.PromptTokens = int32(parsed.Usage.InputTokens)
		reply.Tokens = int32(parsed.Usage.OutputTokens)
	}

	var content strings.Builder
	var toolCalls []*pb.ToolCallDelta
	toolIdx := 0
	for _, b := range parsed.Content {
		switch b.Type {
		case "text":
			content.WriteString(b.Text)
		case "tool_use":
			// Input is a structured JSON object; we serialise to a
			// string so it fits the OpenAI-shaped arguments field
			// downstream consumers expect.
			args := ""
			if len(b.Input) > 0 {
				args = string(b.Input)
			}
			toolCalls = append(toolCalls, newToolCallDelta(toolIdx, b.ID, b.Name, args))
			toolIdx++
		}
	}
	reply.Message = []byte(content.String())
	if len(toolCalls) > 0 {
		reply.ChatDeltas = []*pb.ChatDelta{{ToolCalls: toolCalls}}
	}
	return reply, nil
}

// predictAnthropicStreamRich streams Reply chunks from Anthropic's SSE.
// Three event types matter: content_block_start (initialises tool_use
// id+name), content_block_delta (carries text or input_json_delta),
// message_stop (terminates). The block index from the wire feeds
// straight into ToolCallDelta.Index so downstream consumers can
// reassemble multiple parallel tool calls.
func (c *CloudProxy) predictAnthropicStreamRich(ctx context.Context, cfg *proxyConfig, opts *pb.PredictOptions, results chan<- *pb.Reply) error {
	body, err := buildAnthropicRequest(opts, cfg, true)
	if err != nil {
		return fmt.Errorf("cloud-proxy: marshal request: %w", err)
	}
	resp, err := c.doAnthropicRequest(ctx, cfg, body)
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
		if payload == "" {
			continue
		}
		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			xlog.Debug("cloud-proxy: skip malformed SSE chunk", "error", err)
			continue
		}
		switch ev.Type {
		case "content_block_start":
			// tool_use blocks announce id + name here; arguments arrive
			// in subsequent input_json_delta events. Emit a Reply with
			// just the tool_call init fields so consumers can allocate
			// a slot at this index.
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				if !sendReply(ctx, results, &pb.Reply{
					ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{
						newToolCallDelta(ev.Index, ev.ContentBlock.ID, ev.ContentBlock.Name, ""),
					}}},
				}) {
					return ctx.Err()
				}
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text == "" {
					continue
				}
				if !sendReply(ctx, results, &pb.Reply{
					Message:    []byte(ev.Delta.Text),
					ChatDeltas: []*pb.ChatDelta{{Content: ev.Delta.Text}},
				}) {
					return ctx.Err()
				}
			case "input_json_delta":
				if ev.Delta.PartialJSON == "" {
					continue
				}
				if !sendReply(ctx, results, &pb.Reply{
					ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{
						newToolCallDelta(ev.Index, "", "", ev.Delta.PartialJSON),
					}}},
				}) {
					return ctx.Err()
				}
			}
		case "message_delta":
			// Anthropic sends final usage in message_delta.usage. Emit
			// a usage-only Reply so the consumer can record totals.
			if ev.Usage != nil {
				if !sendReply(ctx, results, &pb.Reply{
					Tokens: int32(ev.Usage.OutputTokens),
				}) {
					return ctx.Err()
				}
			}
		case "message_stop":
			return nil
		}
	}
	return scanner.Err()
}
