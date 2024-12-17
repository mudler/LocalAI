package templates_test

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/mudler/LocalAI/pkg/templates"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const toolCallJinja = `{{ '<|begin_of_text|>' }}{% if messages[0]['role'] == 'system' %}{% set system_message = messages[0]['content'] %}{% endif %}{% if system_message is defined %}{{ '<|start_header_id|>system<|end_header_id|>

' + system_message + '<|eot_id|>' }}{% endif %}{% for message in messages %}{% set content = message['content'] %}{% if message['role'] == 'user' %}{{ '<|start_header_id|>user<|end_header_id|>

' + content + '<|eot_id|><|start_header_id|>assistant<|end_header_id|>

' }}{% elif message['role'] == 'assistant' %}{{ content + '<|eot_id|>' }}{% endif %}{% endfor %}`

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
		"expected": "<|start_header_id|>user<|end_header_id|>\n\nA long time ago in a galaxy far, far away...<|eot_id|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: llama3,
			},
		},
		"functions":   []functions.Function{},
		"shouldUseFn": false,
		"messages": []schema.Message{
			{
				Role:          "user",
				StringContent: "A long time ago in a galaxy far, far away...",
			},
		},
	},
	"assistant": {
		"expected": "<|start_header_id|>assistant<|end_header_id|>\n\nA long time ago in a galaxy far, far away...<|eot_id|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: llama3,
			},
		},
		"functions": []functions.Function{},
		"messages": []schema.Message{
			{
				Role:          "assistant",
				StringContent: "A long time ago in a galaxy far, far away...",
			},
		},
		"shouldUseFn": false,
	},
	"function_call": {

		"expected": "<|start_header_id|>assistant<|end_header_id|>\n\nFunction call:\n{\"function\":\"test\"}<|eot_id|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: llama3,
			},
		},
		"functions": []functions.Function{},
		"messages": []schema.Message{
			{
				Role:         "assistant",
				FunctionCall: map[string]string{"function": "test"},
			},
		},
		"shouldUseFn": false,
	},
	"function_response": {
		"expected": "<|start_header_id|>tool<|end_header_id|>\n\nFunction response:\nResponse from tool<|eot_id|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: llama3,
			},
		},
		"functions": []functions.Function{},
		"messages": []schema.Message{
			{
				Role:          "tool",
				StringContent: "Response from tool",
			},
		},
		"shouldUseFn": false,
	},
}

var chatMLTestMatch map[string]map[string]interface{} = map[string]map[string]interface{}{
	"user": {
		"expected": "<|im_start|>user\nA long time ago in a galaxy far, far away...<|im_end|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: chatML,
			},
		},
		"functions":   []functions.Function{},
		"shouldUseFn": false,
		"messages": []schema.Message{
			{
				Role:          "user",
				StringContent: "A long time ago in a galaxy far, far away...",
			},
		},
	},
	"assistant": {
		"expected": "<|im_start|>assistant\nA long time ago in a galaxy far, far away...<|im_end|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: chatML,
			},
		},
		"functions": []functions.Function{},
		"messages": []schema.Message{
			{
				Role:          "assistant",
				StringContent: "A long time ago in a galaxy far, far away...",
			},
		},
		"shouldUseFn": false,
	},
	"function_call": {
		"expected": "<|im_start|>assistant\n<tool_call>\n{\"function\":\"test\"}\n</tool_call><|im_end|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: chatML,
			},
		},
		"functions": []functions.Function{
			{
				Name:        "test",
				Description: "test",
				Parameters:  nil,
			},
		},
		"shouldUseFn": true,
		"messages": []schema.Message{
			{
				Role:         "assistant",
				FunctionCall: map[string]string{"function": "test"},
			},
		},
	},
	"function_response": {
		"expected": "<|im_start|>tool\n<tool_response>\nResponse from tool\n</tool_response><|im_end|>",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage: chatML,
			},
		},
		"functions":   []functions.Function{},
		"shouldUseFn": false,
		"messages": []schema.Message{
			{
				Role:          "tool",
				StringContent: "Response from tool",
			},
		},
	},
}

var jinjaTest map[string]map[string]interface{} = map[string]map[string]interface{}{
	"user": {
		"expected": "<|begin_of_text|><|start_header_id|>user<|end_header_id|>\n\nA long time ago in a galaxy far, far away...<|eot_id|><|start_header_id|>assistant<|end_header_id|>\n\n",
		"config": &config.BackendConfig{
			TemplateConfig: config.TemplateConfig{
				ChatMessage:   toolCallJinja,
				JinjaTemplate: true,
			},
		},
		"functions":   []functions.Function{},
		"shouldUseFn": false,
		"messages": []schema.Message{
			{
				Role:          "user",
				StringContent: "A long time ago in a galaxy far, far away...",
			},
		},
	},
}
var _ = Describe("Templates", func() {
	Context("chat message ChatML", func() {
		var evaluator *Evaluator
		BeforeEach(func() {
			evaluator = NewEvaluator("")
		})
		for key := range chatMLTestMatch {
			foo := chatMLTestMatch[key]
			It("renders correctly `"+key+"`", func() {
				templated := evaluator.TemplateMessages(foo["messages"].([]schema.Message), foo["config"].(*config.BackendConfig), foo["functions"].([]functions.Function), foo["shouldUseFn"].(bool))
				Expect(templated).To(Equal(foo["expected"]), templated)
			})
		}
	})
	Context("chat message llama3", func() {
		var evaluator *Evaluator
		BeforeEach(func() {
			evaluator = NewEvaluator("")
		})
		for key := range llama3TestMatch {
			foo := llama3TestMatch[key]
			It("renders correctly `"+key+"`", func() {
				templated := evaluator.TemplateMessages(foo["messages"].([]schema.Message), foo["config"].(*config.BackendConfig), foo["functions"].([]functions.Function), foo["shouldUseFn"].(bool))
				Expect(templated).To(Equal(foo["expected"]), templated)
			})
		}
	})
	Context("chat message jinja", func() {
		var evaluator *Evaluator
		BeforeEach(func() {
			evaluator = NewEvaluator("")
		})
		for key := range jinjaTest {
			foo := jinjaTest[key]
			It("renders correctly `"+key+"`", func() {
				templated := evaluator.TemplateMessages(foo["messages"].([]schema.Message), foo["config"].(*config.BackendConfig), foo["functions"].([]functions.Function), foo["shouldUseFn"].(bool))
				Expect(templated).To(Equal(foo["expected"]), templated)
			})
		}
	})
})
