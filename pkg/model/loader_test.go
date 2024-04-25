package model_test

import (
	"github.com/go-skynet/LocalAI/pkg/model"
	. "github.com/go-skynet/LocalAI/pkg/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const chatML = `<|im_start|>{{if eq .RoleName "assistant"}}assistant{{else if eq .RoleName "system"}}system{{else if eq .RoleName "tool"}}tool{{else if eq .RoleName "user"}}user{{end}}
{{- if .FunctionCall }}
<tool_call>
{{- else if eq .RoleName "tool" }}
<tool_response>
{{- end }}
{{- if .Content}}
{{.Content }}
{{- end }}
{{- if .FunctionCall}}
{{toJson .FunctionCall}}
{{- end }}
{{- if .FunctionCall }}
</tool_call>
{{- else if eq .RoleName "tool" }}
</tool_response>
{{- end }}<|im_end|>`

const llama3 = `<|start_header_id|>{{if eq .RoleName "assistant"}}assistant{{else if eq .RoleName "system"}}system{{else if eq .RoleName "tool"}}tool{{else if eq .RoleName "user"}}user{{end}}<|end_header_id|>

{{ if .FunctionCall -}}
Function call:
{{ else if eq .RoleName "tool" -}}
Function response:
{{ end -}}
{{ if .Content -}}
{{.Content -}}
{{ else if .FunctionCall -}}
{{ toJson .FunctionCall -}}
{{ end -}}
<|eot_id|>`

var llama3TestMatch map[string]map[string]interface{} = map[string]map[string]interface{}{
	"user": {
		"template": llama3,
		"expected": "<|start_header_id|>user<|end_header_id|>\n\nA long time ago in a galaxy far, far away...<|eot_id|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "user",
			RoleName:     "user",
			Content:      "A long time ago in a galaxy far, far away...",
			FunctionCall: nil,
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
	"assistant": {
		"template": llama3,
		"expected": "<|start_header_id|>assistant<|end_header_id|>\n\nA long time ago in a galaxy far, far away...<|eot_id|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "assistant",
			RoleName:     "assistant",
			Content:      "A long time ago in a galaxy far, far away...",
			FunctionCall: nil,
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
	"function_call": {
		"template": llama3,
		"expected": "<|start_header_id|>assistant<|end_header_id|>\n\nFunction call:\n{\"function\":\"test\"}<|eot_id|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "assistant",
			RoleName:     "assistant",
			Content:      "",
			FunctionCall: map[string]string{"function": "test"},
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
	"function_response": {
		"template": llama3,
		"expected": "<|start_header_id|>tool<|end_header_id|>\n\nFunction response:\nResponse from tool<|eot_id|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "tool",
			RoleName:     "tool",
			Content:      "Response from tool",
			FunctionCall: nil,
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
}

var chatMLTestMatch map[string]map[string]interface{} = map[string]map[string]interface{}{
	"user": {
		"template": chatML,
		"expected": "<|im_start|>user\nA long time ago in a galaxy far, far away...<|im_end|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "user",
			RoleName:     "user",
			Content:      "A long time ago in a galaxy far, far away...",
			FunctionCall: nil,
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
	"assistant": {
		"template": chatML,
		"expected": "<|im_start|>assistant\nA long time ago in a galaxy far, far away...<|im_end|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "assistant",
			RoleName:     "assistant",
			Content:      "A long time ago in a galaxy far, far away...",
			FunctionCall: nil,
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
	"function_call": {
		"template": chatML,
		"expected": "<|im_start|>assistant\n<tool_call>\n{\"function\":\"test\"}\n</tool_call><|im_end|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "assistant",
			RoleName:     "assistant",
			Content:      "",
			FunctionCall: map[string]string{"function": "test"},
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
	"function_response": {
		"template": chatML,
		"expected": "<|im_start|>tool\n<tool_response>\nResponse from tool\n</tool_response><|im_end|>",
		"data": model.ChatMessageTemplateData{
			SystemPrompt: "",
			Role:         "tool",
			RoleName:     "tool",
			Content:      "Response from tool",
			FunctionCall: nil,
			FunctionName: "",
			LastMessage:  false,
			Function:     false,
			MessageIndex: 0,
		},
	},
}

var _ = Describe("Templates", func() {
	Context("chat message ChatML", func() {
		var modelLoader *ModelLoader
		BeforeEach(func() {
			modelLoader = NewModelLoader("")
		})
		for key := range chatMLTestMatch {
			foo := chatMLTestMatch[key]
			It("renders correctly `"+key+"`", func() {
				templated, err := modelLoader.EvaluateTemplateForChatMessage(foo["template"].(string), foo["data"].(model.ChatMessageTemplateData))
				Expect(err).ToNot(HaveOccurred())
				Expect(templated).To(Equal(foo["expected"]), templated)
			})
		}
	})
	Context("chat message llama3", func() {
		var modelLoader *ModelLoader
		BeforeEach(func() {
			modelLoader = NewModelLoader("")
		})
		for key := range llama3TestMatch {
			foo := llama3TestMatch[key]
			It("renders correctly `"+key+"`", func() {
				templated, err := modelLoader.EvaluateTemplateForChatMessage(foo["template"].(string), foo["data"].(model.ChatMessageTemplateData))
				Expect(err).ToNot(HaveOccurred())
				Expect(templated).To(Equal(foo["expected"]), templated)
			})
		}
	})
})
