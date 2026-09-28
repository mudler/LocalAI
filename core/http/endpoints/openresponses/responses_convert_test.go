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
		msgs, err := convertORInputToMessages("Hello", cfg)
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
		msgs, err := convertORInputToMessages(input, cfg)
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
		msgs, err := convertORInputToMessages(input, cfg)
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
			msgs, err := convertORInputToMessages(request.Input, cfg)
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
	})

	It("does not treat a non-message item (no content key) as a message", func() {
		// An item with neither a known type nor a {role, content} shape must
		// keep falling through unchanged — no behaviour change for such inputs.
		input := []any{
			map[string]any{"role": "user"},
		}
		msgs, err := convertORInputToMessages(input, cfg)
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
