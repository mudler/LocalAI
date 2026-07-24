package main

// The rich chat path (AIModelRich): rides the ENGINE's serving pipeline via
// the ABI v3 chat entry points, exactly like the llama-cpp autoparser flow.
// The engine applies the model's chat template, decides when a tool call
// engages (tool_choice auto lowers to a LAZY structural-tag decode
// constraint), parses tool calls with its streaming-stateful Hermes-style
// parser, and hands back chat.completion.chunk JSON that this file maps 1:1
// onto pb.Reply ChatDelta / ToolCallDelta.

import (
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// useChatPath reports whether the request should go through the engine-side
// chat pipeline: the model config asked for backend-side templating and the
// host handed us structured messages.
func useChatPath(opts *pb.PredictOptions) bool {
	return opts.UseTokenizerTemplate && len(opts.Messages) > 0
}

// chatRequestJSON lowers PredictOptions into one OpenAI chat-completions
// request object for the ABI (the engine ignores `model`/`stream`).
func chatRequestJSON(opts *pb.PredictOptions, stream bool) (string, error) {
	messages := make([]map[string]any, 0, len(opts.Messages))
	for _, m := range opts.Messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if m.ToolCalls != "" {
			var toolCalls any
			if err := json.Unmarshal([]byte(m.ToolCalls), &toolCalls); err == nil {
				msg["tool_calls"] = toolCalls
			}
		}
		messages = append(messages, msg)
	}
	req := map[string]any{"messages": messages}

	if opts.Tools != "" {
		var tools any
		if err := json.Unmarshal([]byte(opts.Tools), &tools); err != nil {
			return "", fmt.Errorf("vllm-cpp: tools is not valid JSON: %w", err)
		}
		req["tools"] = tools
	}
	if opts.ToolChoice != "" {
		var choice any
		// ToolChoice arrives either as a bare string ("auto"/"required"/"none")
		// or as the OpenAI named-function JSON object.
		if err := json.Unmarshal([]byte(opts.ToolChoice), &choice); err == nil {
			req["tool_choice"] = choice
		} else {
			req["tool_choice"] = opts.ToolChoice
		}
	}

	req["temperature"] = opts.Temperature
	if opts.TopP > 0 {
		req["top_p"] = opts.TopP
	}
	if opts.TopK > 0 {
		req["top_k"] = opts.TopK
	}
	if opts.Tokens > 0 {
		req["max_tokens"] = opts.Tokens
	}
	if opts.Seed > 0 {
		req["seed"] = opts.Seed
	}
	if len(opts.StopPrompts) > 0 {
		req["stop"] = opts.StopPrompts
	}
	if opts.PresencePenalty != 0 {
		req["presence_penalty"] = opts.PresencePenalty
	}
	if opts.FrequencyPenalty != 0 {
		req["frequency_penalty"] = opts.FrequencyPenalty
	}
	if stream {
		// The engine's request parser validates stream_options against the
		// stream flag at parse time (before the ABI entry point forces it),
		// so state the intent explicitly.
		req["stream"] = true
		req["stream_options"] = map[string]any{"include_usage": true}
	}

	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// chatChunk is the subset of an OpenAI chat.completion(.chunk) object the
// backend consumes.
type chatChunk struct {
	Object  string `json:"object"`
	Choices []struct {
		Delta        *chatDelta `json:"delta"`   // streaming chunks
		Message      *chatDelta `json:"message"` // non-stream response
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int32 `json:"prompt_tokens"`
		CompletionTokens int32 `json:"completion_tokens"`
	} `json:"usage"`
}

type chatDelta struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning"`
	ToolCalls        []struct {
		Index    int32  `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// toReply maps one parsed chunk onto a pb.Reply carrying the content bytes
// plus the structured ChatDelta (the host prefers ChatDeltas when present).
func (c *chatChunk) toReply() *pb.Reply {
	reply := &pb.Reply{}
	if c.Usage != nil {
		reply.PromptTokens = c.Usage.PromptTokens
		reply.Tokens = c.Usage.CompletionTokens
	}
	if len(c.Choices) == 0 {
		return reply
	}
	d := c.Choices[0].Delta
	if d == nil {
		d = c.Choices[0].Message
	}
	if d == nil {
		return reply
	}
	delta := &pb.ChatDelta{
		Content:          d.Content,
		ReasoningContent: d.ReasoningContent,
	}
	for _, tc := range d.ToolCalls {
		delta.ToolCalls = append(delta.ToolCalls, &pb.ToolCallDelta{
			Index:     tc.Index,
			Id:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	reply.Message = []byte(d.Content)
	if delta.Content != "" || delta.ReasoningContent != "" ||
		len(delta.ToolCalls) > 0 {
		reply.ChatDeltas = []*pb.ChatDelta{delta}
	}
	return reply
}

// Chat-stream registry: chunk JSON arrives on the engine's delivery thread
// through one shared C callback; the integer handle in user_data selects the
// destination channel (never a Go pointer across the ABI).
var (
	chatStreamsMu  sync.Mutex
	chatStreams    = map[uintptr]chan<- *pb.Reply{}
	chatStreamNext uintptr
	chatCbOnce     sync.Once
	chatCbPtr      uintptr
)

func chatCallback(delta uintptr, finished uintptr, userData uintptr) uintptr {
	chatStreamsMu.Lock()
	results := chatStreams[userData]
	chatStreamsMu.Unlock()
	if results == nil {
		return 0
	}
	_ = finished // the terminal call carries an empty delta; nothing to emit.
	payload := goString(delta)
	if payload == "" {
		return 1
	}
	var chunk chatChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		xlog.Error("[vllm-cpp] unparseable chat chunk", "error", err)
		return 1
	}
	results <- chunk.toReply()
	return 1
}

func registerChatStream(results chan<- *pb.Reply) uintptr {
	chatStreamsMu.Lock()
	defer chatStreamsMu.Unlock()
	chatStreamNext++
	chatStreams[chatStreamNext] = results
	return chatStreamNext
}

func unregisterChatStream(h uintptr) {
	chatStreamsMu.Lock()
	defer chatStreamsMu.Unlock()
	delete(chatStreams, h)
}

// PredictRich implements the non-streaming rich path. Without structured
// messages it falls back to the plain Predict flow (LocalAI-side templating,
// optional grammar constraint).
func (v *VllmCpp) PredictRich(opts *pb.PredictOptions) (*pb.Reply, error) {
	if !useChatPath(opts) {
		text, err := v.Predict(opts)
		if err != nil {
			return nil, err
		}
		return &pb.Reply{Message: []byte(text)}, nil
	}
	if v.engine == 0 {
		return nil, fmt.Errorf("vllm-cpp: model not loaded")
	}
	request, err := chatRequestJSON(opts, false)
	if err != nil {
		return nil, err
	}
	var out uintptr
	rc := vllmChat(v.engine, request, unsafe.Pointer(&out)) // #nosec G103 -- char** out-param
	if rc != vllmOK {
		return nil, fmt.Errorf("vllm-cpp: chat failed: %s", vllmLastError())
	}
	payload := goString(out)
	vllmStringFree(out)
	var response chatChunk
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		return nil, fmt.Errorf("vllm-cpp: unparseable chat response: %w", err)
	}
	return response.toReply(), nil
}

// PredictStreamRich implements the streaming rich path. Contract: send into
// the channel and return when finished; the host closes the channel.
func (v *VllmCpp) PredictStreamRich(opts *pb.PredictOptions, results chan<- *pb.Reply) error {
	if !useChatPath(opts) {
		// Legacy bridge: run the plain stream and wrap deltas.
		plain := make(chan string)
		if err := v.PredictStream(opts, plain); err != nil {
			return err
		}
		for delta := range plain {
			results <- &pb.Reply{Message: []byte(delta)}
		}
		return nil
	}
	if v.engine == 0 {
		return fmt.Errorf("vllm-cpp: model not loaded")
	}
	request, err := chatRequestJSON(opts, true)
	if err != nil {
		return err
	}
	chatCbOnce.Do(func() {
		chatCbPtr = purego.NewCallback(chatCallback)
	})
	handle := registerChatStream(results)
	defer unregisterChatStream(handle)
	rc := vllmChatStream(v.engine, request, chatCbPtr, handle)
	if rc != vllmOK {
		return fmt.Errorf("vllm-cpp: chat stream failed: %s", vllmLastError())
	}
	return nil
}
