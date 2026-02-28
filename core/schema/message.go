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

	// Annotations for citations
	Annotations   []Annotation `json:"annotations,omitempty"`
	StringContent string       `json:"string_content,omitempty" yaml:"string_content,omitempty"`
	StringImages  []string     `json:"string_images,omitempty" yaml:"string_images,omitempty"`
	StringVideos  []string     `json:"string_videos,omitempty" yaml:"string_videos,omitempty"`
	StringAudios  []string     `json:"string_audios,omitempty" yaml:"string_audios,omitempty"`

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

// MessagesToProto converts schema.Message slice to proto.Message slice
// It handles content conversion, tool_calls serialization, and optional fields
func (messages Messages) ToProto() []*proto.Message {
	protoMessages := make([]*proto.Message, len(messages))
	for i, message := range messages {
		protoMessages[i] = &proto.Message{
			Role: message.Role,
			Name: message.Name, // needed by function calls
		}

		switch ct := message.Content.(type) {
		case string:
			protoMessages[i].Content = ct
		case []interface{}:
			// If using the tokenizer template, in case of multimodal we want to keep the multimodal content as and return only strings here
			data, _ := json.Marshal(ct)
			resultData := []struct {
				Text string `json:"text"`
			}{}
			json.Unmarshal(data, &resultData)
			for _, r := range resultData {
				protoMessages[i].Content += r.Text
			}
		}

		// Serialize tool_calls to JSON string if present
		if len(message.ToolCalls) > 0 {
			toolCallsJSON, err := json.Marshal(message.ToolCalls)
			if err != nil {
				xlog.Warn("failed to marshal tool_calls to JSON", "error", err)
			} else {
				protoMessages[i].ToolCalls = string(toolCallsJSON)
			}
		}

		// Note: tool_call_id is not in schema.Message yet
		// Reasoning field is now available in schema.Message but not yet in proto.Message
	}
	return protoMessages
}
