package openresponses

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression for mudler/LocalAI#10039. convertORInputToMessages must populate
// both Content and StringContent: the templating fallback path reads
// StringContent, while the UseTokenizerTemplate path serialises Content via
// Messages.ToProto(). Leaving Content nil produced an empty prompt on any model
// without a Go-side template.chat_message block (the default for imported GGUFs).
var _ = Describe("convertORInputToMessages", func() {
	cfg := &config.ModelConfig{}

	It("populates both Content and StringContent for plain string input", func() {
		msgs, err := convertORInputToMessages("Hello", cfg, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Role).To(Equal("user"))
		Expect(msgs[0].StringContent).To(Equal("Hello"))
		Expect(msgs[0].Content).To(Equal("Hello"))
	})

	It("accepts a bare {role, content} item without a type discriminator", func() {
		// The OpenAI Python SDK helper client.responses.create(input=[{...}])
		// sends message items with no "type" field. They must not be dropped.
		input := []any{
			map[string]any{"role": "user", "content": "Hi there"},
		}
		msgs, err := convertORInputToMessages(input, cfg, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Role).To(Equal("user"))
		Expect(msgs[0].StringContent).To(Equal("Hi there"))
		Expect(msgs[0].Content).To(Equal("Hi there"))
	})

	It("still populates both fields for an explicit type:message item", func() {
		input := []any{
			map[string]any{"type": "message", "role": "user", "content": "Typed"},
		}
		msgs, err := convertORInputToMessages(input, cfg, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].StringContent).To(Equal("Typed"))
		Expect(msgs[0].Content).To(Equal("Typed"))
	})

	Context("replaying serialized response output", func() {
		outputItems := func(items ...schema.ORItemField) []any {
			data, err := json.Marshal(schema.ORResponseResource{Output: items})
			Expect(err).NotTo(HaveOccurred())
			var response struct {
				Output []any `json:"output"`
			}
			Expect(json.Unmarshal(data, &response)).To(Succeed())
			return response.Output
		}
		convertRequest := func(input []any) []schema.Message {
			data, err := json.Marshal(schema.OpenResponsesRequest{Input: input})
			Expect(err).NotTo(HaveOccurred())
			var request schema.OpenResponsesRequest
			Expect(json.Unmarshal(data, &request)).To(Succeed())
			msgs, err := convertORInputToMessages(request.Input, cfg, "")
			Expect(err).NotTo(HaveOccurred())
			return msgs
		}
		assistant := func(parts ...string) schema.ORItemField {
			var content []schema.ORContentPart
			for _, text := range parts {
				content = append(content, schema.ORContentPart{Type: "output_text", Text: text})
			}
			return schema.ORItemField{Type: "message", Role: "assistant", Content: content}
		}
		user := func(text string) any {
			return map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": text},
			}}
		}

		It("preserves assistant output_text in both content fields", func() {
			msgs := convertRequest(outputItems(assistant("Hello again")))
			Expect(msgs).To(HaveLen(1))
			Expect(msgs[0].Role).To(Equal("assistant"))
			Expect(msgs[0].Content).To(Equal("Hello again"))
			Expect(msgs[0].StringContent).To(Equal("Hello again"))
		})

		It("joins text parts and contiguous assistant messages across user input_text boundaries", func() {
			input := []any{user("First question")}
			input = append(input, outputItems(assistant("First ", "answer"), assistant("More detail"))...)
			input = append(input, user("Next question"))
			input = append(input, outputItems(assistant("Second answer"))...)
			msgs := convertRequest(input)
			Expect(msgs).To(HaveLen(4))
			for i, expected := range []struct{ role, text string }{
				{"user", "First question"},
				{"assistant", "First answer\nMore detail"},
				{"user", "Next question"},
				{"assistant", "Second answer"},
			} {
				Expect(msgs[i].Role).To(Equal(expected.role))
				Expect(msgs[i].Content).To(Equal(expected.text))
				Expect(msgs[i].StringContent).To(Equal(expected.text))
			}
		})

		It("replays a namespaced function_call under the name the model was offered", func() {
			msgs := convertRequest(outputItems(schema.ORItemField{
				Type: "function_call", CallID: "call_1", Namespace: "mcp__docs", Name: "search", Arguments: `{"q":"x"}`,
			}))
			Expect(msgs).To(HaveLen(1))
			Expect(msgs[0].ToolCalls).To(HaveLen(1))
			Expect(msgs[0].ToolCalls[0].FunctionCall.Name).To(Equal("ns_mcp__docs___search"))
			Expect(msgs[0].ToolCalls[0].FunctionCall.Arguments).To(Equal(`{"q":"x"}`))
		})
	})

	It("does not treat a non-message item (no content key) as a message", func() {
		// An item with neither a known type nor a {role, content} shape must
		// keep falling through unchanged — no behaviour change for such inputs.
		input := []any{
			map[string]any{"role": "user"},
		}
		msgs, err := convertORInputToMessages(input, cfg, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(BeEmpty())
	})
})

var _ = Describe("convertORToolsToOpenAIFormat", func() {
	It("only converts Responses function tools", func() {
		converted := convertORToolsToOpenAIFormat([]schema.ORFunctionTool{
			{Type: "function", Name: "example_function", Parameters: map[string]any{"type": "object"}},
			{Type: "web_search"},
			{Type: "namespace", Name: "multi_agent_v1"},
		})

		Expect(converted).To(HaveLen(1))
		Expect(converted[0].Type).To(Equal("function"))
		Expect(converted[0].Function.Name).To(Equal("example_function"))
		Expect(converted[0].Function.Parameters).To(Equal(map[string]any{"type": "object"}))
	})

	It("flattens namespace members to ns_{namespace}___{name}", func() {
		var tools []schema.ORFunctionTool
		Expect(json.Unmarshal([]byte(`[
			{"type": "function", "name": "exec_command"},
			{"type": "namespace", "name": "mcp__docs", "tools": [
				{"type": "function", "name": "search", "description": "Search the docs", "parameters": {"type": "object"}},
				{"type": "function", "name": "fetch"}
			]}
		]`), &tools)).To(Succeed())

		converted := convertORToolsToOpenAIFormat(tools)

		Expect(converted).To(HaveLen(3))
		Expect(converted[0].Function.Name).To(Equal("exec_command"))
		Expect(converted[1].Function.Name).To(Equal("ns_mcp__docs___search"))
		Expect(converted[1].Function.Description).To(Equal("Search the docs"))
		Expect(converted[1].Function.Parameters).To(Equal(map[string]any{"type": "object"}))
		Expect(converted[2].Function.Name).To(Equal("ns_mcp__docs___fetch"))
	})
})

var _ = Describe("namespace tool names", func() {
	namespaceTool := func(namespace string, members ...string) schema.ORFunctionTool {
		tool := schema.ORFunctionTool{Type: "namespace", Name: namespace}
		for _, m := range members {
			tool.Tools = append(tool.Tools, schema.ORFunctionTool{Type: "function", Name: m})
		}
		return tool
	}
	call := func(name string) schema.ORItemField {
		return schema.ORItemField{Type: "function_call", CallID: "call_1", Name: name, Arguments: "{}"}
	}
	output := func(tools []schema.ORFunctionTool, name string) schema.ORItemField {
		input := &schema.OpenResponsesRequest{Tools: tools}
		response := buildORResponse("resp_1", 1, nil, schema.ORStatusCompleted, input, []schema.ORItemField{call(name)}, nil, false)
		Expect(response.Output).To(HaveLen(1))
		return response.Output[0]
	}

	DescribeTable("returns the namespace and name the client declared",
		func(namespace, name string) {
			tools := []schema.ORFunctionTool{namespaceTool(namespace, name)}
			item := output(tools, responsesToolName(namespace, name))
			Expect(item.Namespace).To(Equal(namespace))
			Expect(item.Name).To(Equal(name))
		},
		Entry("plain namespace", "mcp__docs", "search"),
		// Codex sanitizes the server "docs-" to the namespace "mcp__docs_".
		Entry("namespace ending in _", "mcp__docs_", "search"),
		Entry("namespace containing ___", "mcp___docs", "search"),
		Entry("member name starting with _", "mcp__docs", "_search"),
	)

	It("does not split a plain function whose name looks flattened", func() {
		tools := []schema.ORFunctionTool{{Type: "function", Name: "ns_x___y"}}
		item := output(tools, "ns_x___y")
		Expect(item.Namespace).To(BeEmpty())
		Expect(item.Name).To(Equal("ns_x___y"))
	})

	It("does not split a call to an undeclared name", func() {
		item := output(nil, "ns_mcp__docs___search")
		Expect(item.Namespace).To(BeEmpty())
		Expect(item.Name).To(Equal("ns_mcp__docs___search"))
	})

	It("keeps a plain function when a namespace member flattens to its name", func() {
		tools := []schema.ORFunctionTool{
			namespaceTool("x", "y"),
			{Type: "function", Name: "ns_x___y"},
		}
		converted := convertORToolsToOpenAIFormat(tools)
		Expect(converted).To(HaveLen(1))
		Expect(converted[0].Function.Name).To(Equal("ns_x___y"))

		item := output(tools, "ns_x___y")
		Expect(item.Namespace).To(BeEmpty())
		Expect(item.Name).To(Equal("ns_x___y"))
	})

	It("skips a later namespace member that flattens to the same name", func() {
		// ("a", "b___c") and ("a___b", "c") both flatten to "ns_a___b___c".
		tools := []schema.ORFunctionTool{
			namespaceTool("a", "b___c"),
			namespaceTool("a___b", "c"),
		}
		converted := convertORToolsToOpenAIFormat(tools)
		Expect(converted).To(HaveLen(1))
		Expect(converted[0].Function.Name).To(Equal("ns_a___b___c"))

		item := output(tools, "ns_a___b___c")
		Expect(item.Namespace).To(Equal("a"))
		Expect(item.Name).To(Equal("b___c"))
	})

	It("drops namespace members that are not functions", func() {
		tools := []schema.ORFunctionTool{{Type: "namespace", Name: "mcp__docs", Tools: []schema.ORFunctionTool{
			{Type: "custom", Name: "apply_patch"},
			{Type: "function", Name: "search"},
		}}}
		converted := convertORToolsToOpenAIFormat(tools)
		Expect(converted).To(HaveLen(1))
		Expect(converted[0].Function.Name).To(Equal("ns_mcp__docs___search"))
	})

	It("forces the flattened name when tool_choice names a namespaced function", func() {
		cfg := &config.ModelConfig{}
		input := &schema.OpenResponsesRequest{
			Tools:      []schema.ORFunctionTool{namespaceTool("mcp__docs", "search")},
			ToolChoice: map[string]any{"type": "function", "namespace": "mcp__docs", "name": "search"},
		}
		funcs, _ := convertORToolsToFunctions(input, cfg)
		Expect(funcs).To(HaveLen(1))
		Expect(cfg.FunctionToCall()).To(Equal("ns_mcp__docs___search"))

		Expect(toolChoiceForModel(input.ToolChoice)).To(Equal(map[string]any{"type": "function", "name": "ns_mcp__docs___search"}))
		Expect(input.ToolChoice).To(HaveKeyWithValue("namespace", "mcp__docs"), "the request echoed to the client keeps its tool_choice")
		Expect(toolChoiceForModel("auto")).To(Equal("auto"))
	})
})

var _ = Describe("resolvePreviousResponseMessages", func() {
	It("replays multi-hop response history from oldest to newest", func() {
		store := NewResponseStore(0)
		cfg := &config.ModelConfig{}
		message := func(role, text string) schema.ORItemField {
			return schema.ORItemField{
				Type:    "message",
				Role:    role,
				Content: []schema.ORContentPart{{Type: "output_text", Text: text}},
			}
		}

		store.Store("resp_0", &schema.OpenResponsesRequest{Input: "base"}, &schema.ORResponseResource{
			ID: "resp_0", Output: []schema.ORItemField{message("assistant", "answer-0")},
		})
		store.Store("resp_1", &schema.OpenResponsesRequest{PreviousResponseID: "resp_0", Input: "question-1"}, &schema.ORResponseResource{
			ID: "resp_1", Output: []schema.ORItemField{message("assistant", "answer-1")},
		})
		store.Store("resp_2", &schema.OpenResponsesRequest{PreviousResponseID: "resp_1", Input: "question-2"}, &schema.ORResponseResource{
			ID: "resp_2", Output: []schema.ORItemField{message("assistant", "answer-2")},
		})

		msgs, err := resolvePreviousResponseMessages(store, "resp_2", cfg, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(6))
		Expect([]string{
			msgs[0].StringContent, msgs[1].StringContent,
			msgs[2].StringContent, msgs[3].StringContent,
			msgs[4].StringContent, msgs[5].StringContent,
		}).To(Equal([]string{"base", "answer-0", "question-1", "answer-1", "question-2", "answer-2"}))
	})

	It("resolves a chain across connection-local and global stores", func() {
		connectionStore := NewResponseStore(0)
		globalStore := NewResponseStore(0)
		cfg := &config.ModelConfig{}
		message := func(text string) schema.ORItemField {
			return schema.ORItemField{
				Type:    "message",
				Role:    "assistant",
				Content: []schema.ORContentPart{{Type: "output_text", Text: text}},
			}
		}

		globalStore.Store("resp_global", &schema.OpenResponsesRequest{Input: "base"}, &schema.ORResponseResource{
			ID: "resp_global", Output: []schema.ORItemField{message("answer-0")},
		})
		connectionStore.Store("resp_local", &schema.OpenResponsesRequest{PreviousResponseID: "resp_global", Input: "question-1"}, &schema.ORResponseResource{
			ID: "resp_local", Output: []schema.ORItemField{message("answer-1")},
		})

		msgs, _, err := resolvePreviousResponseMessagesFromSources(
			[]previousResponseStoreSource{
				{store: connectionStore, connectionLocal: true},
				{store: globalStore},
			},
			"resp_local",
			cfg,
			"",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(4))
		Expect([]string{msgs[0].StringContent, msgs[1].StringContent, msgs[2].StringContent, msgs[3].StringContent}).To(
			Equal([]string{"base", "answer-0", "question-1", "answer-1"}),
		)
	})
})
