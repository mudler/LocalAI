package main

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// imagePlaceholder is the URL given to image parts. The helper returns the
// placeholders in prompt order, which maps each rendered image to a request image.
const imagePlaceholder = "strata-image:"

func parseJSONArg(s string) (any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// parseToolChoice reads LocalAI's tool_choice: a JSON string, a JSON object,
// or a bare word.
func parseToolChoice(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

// flattenTools turns OpenAI tool objects into the flat shape the output parser
// reads ({"name", "description", "parameters"}).
func flattenTools(tools any) any {
	list, ok := tools.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, t := range list {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if fn, ok := m["function"].(map[string]any); ok {
			m = fn
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildPromptRequest turns the proto messages into an OpenAI chat request.
// LocalAI writes one marker into a message's text for each of its images; they
// become image parts.
func buildPromptRequest(opts *pb.PredictOptions, marker string, tools, choice any) (promptRequest, error) {
	req := promptRequest{Tools: tools, ToolChoice: choice}
	if v := opts.GetMetadata()["enable_thinking"]; v != "" {
		switch strings.ToLower(v) {
		case "true", "1":
			req.ChatTemplateKwargs = map[string]any{"enable_thinking": true}
		case "false", "0":
			req.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
		}
	}
	req.ReasoningEffort = opts.GetMetadata()["reasoning_effort"]
	next := 0
	for _, m := range opts.GetMessages() {
		msg := map[string]any{"role": m.GetRole()}
		content := m.GetContent()
		if n := strings.Count(content, marker); n > 0 && marker != "" {
			var parts []map[string]any
			for i, seg := range strings.Split(content, marker) {
				if i > 0 {
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": fmt.Sprintf("%s%d", imagePlaceholder, next)}})
					next++
				}
				if seg != "" {
					parts = append(parts, map[string]any{"type": "text", "text": seg})
				}
			}
			msg["content"] = parts
		} else {
			msg["content"] = content
		}
		if m.GetName() != "" {
			msg["name"] = m.GetName()
		}
		if m.GetToolCallId() != "" {
			msg["tool_call_id"] = m.GetToolCallId()
		}
		if m.GetReasoningContent() != "" {
			msg["reasoning_content"] = m.GetReasoningContent()
		}
		if tc := m.GetToolCalls(); tc != "" {
			var calls any
			if err := json.Unmarshal([]byte(tc), &calls); err != nil {
				return req, fmt.Errorf("tool_calls of a %s message is not valid JSON: %w", m.GetRole(), err)
			}
			msg["tool_calls"] = calls
		}
		req.Messages = append(req.Messages, msg)
	}
	if next != len(opts.GetImages()) {
		return req, fmt.Errorf("the messages have %d image markers (%s) for %d images", next, marker, len(opts.GetImages()))
	}
	return req, nil
}
