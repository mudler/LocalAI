package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Wire types of the OpenAI-compatible API of coli serve. Only the fields
// the backend reads or writes are modelled.

type chatMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	Name             string          `json:"name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        json.RawMessage `json:"tool_calls,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type request struct {
	Model string `json:"model"`

	Messages []chatMessage `json:"messages,omitempty"`
	Prompt   *string       `json:"prompt,omitempty"`

	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`

	MaxTokens   *int32   `json:"max_tokens,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
	TopP        *float32 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`

	Tools      json.RawMessage `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	EnableThinking  *bool  `json:"enable_thinking,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Chat takes a boolean and top_logprobs, completions an integer count.
	Logprobs    any    `json:"logprobs,omitempty"`
	TopLogprobs *int32 `json:"top_logprobs,omitempty"`

	LogitBias json.RawMessage `json:"logit_bias,omitempty"`
}

type toolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type usage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
}

type choice struct {
	Message struct {
		Content          *string    `json:"content"`
		ReasoningContent string     `json:"reasoning_content"`
		ToolCalls        []toolCall `json:"tool_calls"`
	} `json:"message"`
	Delta struct {
		Content          string     `json:"content"`
		ReasoningContent string     `json:"reasoning_content"`
		ToolCalls        []toolCall `json:"tool_calls"`
	} `json:"delta"`
	Text         string          `json:"text"`
	Logprobs     json.RawMessage `json:"logprobs"`
	FinishReason *string         `json:"finish_reason"`
}

type response struct {
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage"`
	Error   *apiErr  `json:"error"`
}

type apiErr struct {
	Message string `json:"message"`
	Code    any    `json:"code"`
	Param   any    `json:"param"`
}

// isChat reports whether a request goes to /v1/chat/completions: LocalAI
// hands over the structured messages and colibri renders them with the
// model's own chat template and parses its tool calls.
func isChat(opts *pb.PredictOptions) bool {
	return opts.GetUseTokenizerTemplate() && len(opts.GetMessages()) > 0
}

// checkRequest refuses what colibri cannot do and LocalAI only sends when a
// request asked for it, instead of answering without it.
//
// Sampling settings that LocalAI fills with defaults on every request
// (top_k, min_p, repeat, presence and frequency penalties, typical_p, tfz,
// mirostat, seed) cannot be told apart from a user's choice here; colibri's
// gateway refuses most of them, so they are left out of the request. The
// docs list them.
func checkRequest(opts *pb.PredictOptions) error {
	if opts.GetGrammar() != "" {
		return errors.New("colibri: grammars are not supported. For tool calls set `function.grammar.disable: true` in the model config (colibri parses the model's own tool-call format); `response_format` JSON modes are not supported")
	}
	if len(opts.GetImages()) > 0 || len(opts.GetVideos()) > 0 || len(opts.GetAudios()) > 0 {
		return errors.New("colibri: image, video and audio input are not supported by this backend yet")
	}
	if n := opts.GetLogprobs(); !isChat(opts) && n > 32 {
		return fmt.Errorf("colibri: logprobs is at most 32, got %d", n)
	}
	if (opts.GetTools() != "" || opts.GetToolChoice() != "") && !isChat(opts) {
		return errors.New("colibri: tools need structured messages; set `template.use_tokenizer_template: true` in the model config")
	}
	return nil
}

// buildRequest translates PredictOptions into a chat or completions body.
func buildRequest(opts *pb.PredictOptions, model string, stream bool) ([]byte, error) {
	r := request{Model: model, Stream: stream}
	if stream {
		r.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if n := opts.GetTokens(); n > 0 {
		r.MaxTokens = &n
	}
	t, p := opts.GetTemperature(), opts.GetTopP()
	r.Temperature = &t
	if p > 0 && p <= 1 {
		r.TopP = &p
	}
	r.Stop = opts.GetStopPrompts()
	if b := strings.TrimSpace(opts.GetLogitBias()); b != "" && b != "{}" && b != "null" {
		r.LogitBias = json.RawMessage(b)
	}

	if isChat(opts) {
		for _, m := range opts.GetMessages() {
			cm := chatMessage{
				Role:             m.GetRole(),
				Content:          m.GetContent(),
				Name:             m.GetName(),
				ToolCallID:       m.GetToolCallId(),
				ReasoningContent: m.GetReasoningContent(),
			}
			if tc := strings.TrimSpace(m.GetToolCalls()); tc != "" && tc != "null" && tc != "[]" {
				if !json.Valid([]byte(tc)) {
					return nil, fmt.Errorf("colibri: tool_calls of a %s message are not valid JSON", m.GetRole())
				}
				cm.ToolCalls = json.RawMessage(tc)
			}
			r.Messages = append(r.Messages, cm)
		}
		var err error
		if r.Tools, err = rawJSON("tools", opts.GetTools()); err != nil {
			return nil, err
		}
		if r.ToolChoice, err = toolChoice(opts.GetToolChoice()); err != nil {
			return nil, err
		}
		md := opts.GetMetadata()
		if v, ok := md["enable_thinking"]; ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("colibri: enable_thinking must be true or false, got %q", v)
			}
			r.EnableThinking = &b
		}
		r.ReasoningEffort = md["reasoning_effort"]
		if opts.GetLogprobs() > 0 || opts.GetTopLogprobs() > 0 {
			r.Logprobs = true
			if n := opts.GetTopLogprobs(); n > 0 {
				r.TopLogprobs = &n
			}
		}
	} else {
		prompt := opts.GetPrompt()
		r.Prompt = &prompt
		if n := opts.GetLogprobs(); n > 0 {
			r.Logprobs = n
		}
	}
	return json.Marshal(r)
}

func rawJSON(name, s string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil, nil
	}
	if !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("colibri: %s is not valid JSON", name)
	}
	return json.RawMessage(s), nil
}

// toolChoice accepts the JSON LocalAI sends ("auto", an object) and also a
// bare word, which some callers put in the field without quotes.
func toolChoice(s string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "", "null":
		return nil, nil
	case "auto", "none", "required":
		return json.RawMessage(strconv.Quote(s)), nil
	}
	return rawJSON("tool_choice", s)
}

// upstreamError turns an error response of coli serve into an error. The
// gateway names what it refuses (an unsupported field, a full queue), so
// its message is passed on as it is.
func upstreamError(status int, body io.Reader) error {
	b, _ := io.ReadAll(io.LimitReader(body, 1<<20))
	var r response
	if json.Unmarshal(b, &r) == nil && r.Error != nil && r.Error.Message != "" {
		msg := r.Error.Message
		if p, ok := r.Error.Param.(string); ok && p != "" {
			msg += " (param: " + p + ")"
		}
		return fmt.Errorf("colibri: coli serve answered %d: %s", status, msg)
	}
	return fmt.Errorf("colibri: coli serve answered %d: %s", status, strings.TrimSpace(string(b)))
}

func toolDeltas(calls []toolCall) []*pb.ToolCallDelta {
	out := make([]*pb.ToolCallDelta, 0, len(calls))
	for i, c := range calls {
		idx := c.Index
		if idx == 0 && i > 0 {
			idx = i // a non-streamed list may leave index out
		}
		out = append(out, &pb.ToolCallDelta{
			Index:     int32(idx), // #nosec G115 -- tool call indexes are tiny
			Id:        c.ID,
			Name:      c.Function.Name,
			Arguments: c.Function.Arguments,
		})
	}
	return out
}

// replyFromResponse maps a non-streamed answer to a Reply. A chat answer
// carries one ChatDelta with the parsed content, reasoning and tool calls,
// as the llama.cpp backend returns its autoparser output.
func replyFromResponse(r *response, chat bool) (*pb.Reply, error) {
	if len(r.Choices) == 0 {
		return nil, errors.New("colibri: coli serve returned no choices")
	}
	c := r.Choices[0]
	reply := &pb.Reply{}
	if r.Usage != nil {
		reply.PromptTokens, reply.Tokens = r.Usage.PromptTokens, r.Usage.CompletionTokens
	}
	if len(c.Logprobs) > 0 && string(c.Logprobs) != "null" {
		reply.Logprobs = c.Logprobs
	}
	if !chat {
		reply.Message = []byte(c.Text)
		return reply, nil
	}
	content := ""
	if c.Message.Content != nil {
		content = *c.Message.Content
	}
	reply.Message = []byte(content)
	d := &pb.ChatDelta{Content: content, ReasoningContent: c.Message.ReasoningContent, ToolCalls: toolDeltas(c.Message.ToolCalls)}
	if d.Content != "" || d.ReasoningContent != "" || len(d.ToolCalls) > 0 {
		reply.ChatDeltas = []*pb.ChatDelta{d}
	}
	return reply, nil
}

// readStream reads the SSE stream of coli serve and calls emit for each
// chunk that carries something. The last call carries the usage.
func readStream(body io.Reader, chat bool, emit func(*pb.Reply) error) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	var final *pb.Reply
	complete := false
	for sc.Scan() {
		line := sc.Text()
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // comments (keepalives), event names, blank lines
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			complete = true
			break
		}
		if payload == "" {
			continue
		}
		var chunk response
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("colibri: malformed stream chunk: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("colibri: coli serve failed while streaming: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			final = &pb.Reply{PromptTokens: chunk.Usage.PromptTokens, Tokens: chunk.Usage.CompletionTokens}
		}
		for _, c := range chunk.Choices {
			var reply *pb.Reply
			if chat {
				d := &pb.ChatDelta{Content: c.Delta.Content, ReasoningContent: c.Delta.ReasoningContent, ToolCalls: toolDeltas(c.Delta.ToolCalls)}
				if d.Content == "" && d.ReasoningContent == "" && len(d.ToolCalls) == 0 {
					continue
				}
				// Message carries the parsed content only, as the llama.cpp
				// backend does, so tool-call markup never streams as text.
				reply = &pb.Reply{Message: []byte(d.Content), ChatDeltas: []*pb.ChatDelta{d}}
			} else {
				if c.Text == "" {
					continue
				}
				reply = &pb.Reply{Message: []byte(c.Text)}
			}
			if err := emit(reply); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !complete {
		return errors.New("colibri: the stream of coli serve ended before it was complete")
	}
	if final != nil {
		return emit(final)
	}
	return nil
}
