package schema

import (
	"encoding/json"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

type Message struct {
	// The message role
	Role string `json:"role,omitempty" yaml:"role"`

	// The message name (used for tools calls)
	Name string `json:"name,omitempty" yaml:"name"`

	// The message content
	Content interface{} `json:"content" yaml:"content"`

	StringContent string   `json:"string_content,omitempty" yaml:"string_content,omitempty"`
	StringImages  []string `json:"string_images,omitempty" yaml:"string_images,omitempty"`
	StringVideos  []string `json:"string_videos,omitempty" yaml:"string_videos,omitempty"`
	StringAudios  []string `json:"string_audios,omitempty" yaml:"string_audios,omitempty"`

	// A result of a function call
	FunctionCall interface{} `json:"function_call,omitempty" yaml:"function_call,omitempty"`

	ToolCalls []ToolCall `json:"tool_calls,omitempty" yaml:"tool_call,omitempty"`

	// Reasoning content extracted from <thinking>...</thinking> tags
	Reasoning *string `json:"reasoning,omitempty" yaml:"reasoning,omitempty"`
}

type ToolCall struct {
	Index        int          `json:"index"`
	ID           string       `json:"id"`
	Type         string       `json:"type"`
	FunctionCall FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type Messages []Message

// MessagesToProto converts schema.Message slice to proto.Message slice.
// It handles content conversion, tool_calls serialization, and optional fields.
// When mergeThinking is true, messages with role "thinking" are merged into
// the next assistant message's ReasoningContent field instead of being passed
// through as standalone messages.
func (messages Messages) ToProto(mergeThinking ...bool) []*proto.Message {
	merge := len(mergeThinking) > 0 && mergeThinking[0]

	input := []Message(messages)
	if merge {
		input = mergeThinkingMessages(input)
	}

	protoMessages := make([]*proto.Message, 0, len(input))
	for _, message := range input {
		pm := &proto.Message{
			Role: message.Role,
			Name: message.Name, // needed by function calls
		}

		switch ct := message.Content.(type) {
		case string:
			pm.Content = ct
		case []interface{}:
			// If using the tokenizer template, in case of multimodal we want to keep the multimodal content as and return only strings here
			data, _ := json.Marshal(ct)
			resultData := []struct {
				Text string `json:"text"`
			}{}
			json.Unmarshal(data, &resultData)
			for _, r := range resultData {
				pm.Content += r.Text
			}
		}

		// Serialize tool_calls to JSON string if present
		if len(message.ToolCalls) > 0 {
			toolCallsJSON, err := json.Marshal(message.ToolCalls)
			if err != nil {
				xlog.Warn("failed to marshal tool_calls to JSON", "error", err)
			} else {
				pm.ToolCalls = string(toolCallsJSON)
			}
		}

		// Map Reasoning field to proto ReasoningContent
		if message.Reasoning != nil {
			pm.ReasoningContent = *message.Reasoning
		}

		protoMessages = append(protoMessages, pm)
	}
	return protoMessages
}

// mergeThinkingMessages pre-processes messages to merge "thinking" role messages
// into the following assistant message's Reasoning field, removing them from the list.
func mergeThinkingMessages(messages []Message) []Message {
	result := make([]Message, 0, len(messages))
	var pendingThinking string

	for _, msg := range messages {
		if msg.Role == "thinking" {
			content := messageContentString(msg)
			if pendingThinking != "" {
				pendingThinking += "\n"
			}
			pendingThinking += content
			continue
		}

		if pendingThinking != "" && msg.Role == "assistant" {
			merged := pendingThinking
			if msg.Reasoning != nil {
				merged += "\n" + *msg.Reasoning
			}
			msg.Reasoning = &merged
			pendingThinking = ""
		}

		result = append(result, msg)
	}

	// If there's leftover thinking content with no following assistant message,
	// preserve it as a thinking role message so it's not silently lost.
	if pendingThinking != "" {
		result = append(result, Message{
			Role:    "thinking",
			Content: pendingThinking,
		})
	}

	return result
}

// messageContentString extracts the string content from a Message's Content field.
func messageContentString(msg Message) string {
	switch ct := msg.Content.(type) {
	case string:
		return ct
	default:
		return ""
	}
}
