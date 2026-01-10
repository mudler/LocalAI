package e2e_test

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Anthropic API E2E test", func() {
	var client anthropic.Client

	Context("API with Anthropic SDK", func() {
		BeforeEach(func() {
			// Create Anthropic client pointing to LocalAI
			client = anthropic.NewClient(
				option.WithBaseURL(localAIURL),
				option.WithAPIKey("test-api-key"), // LocalAI doesn't require a real API key
			)

			// Wait for API to be ready by attempting a simple request
			Eventually(func() error {
				_, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 10,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Hi")),
					},
				})
				return err
			}, "2m").ShouldNot(HaveOccurred())
		})

		Context("Non-streaming responses", func() {
			It("generates a response for a simple message", func() {
				message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("How much is 2+2? Reply with just the number.")),
					},
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(message.Content).ToNot(BeEmpty())
				// Role is a constant type that defaults to "assistant"
				Expect(string(message.Role)).To(Equal("assistant"))
				Expect(message.StopReason).To(Equal(anthropic.MessageStopReasonEndTurn))
				Expect(string(message.Type)).To(Equal("message"))

				// Check that content contains text block with expected answer
				Expect(len(message.Content)).To(BeNumerically(">=", 1))
				textBlock := message.Content[0]
				Expect(string(textBlock.Type)).To(Equal("text"))
				Expect(textBlock.Text).To(Or(ContainSubstring("4"), ContainSubstring("four")))
			})

			It("handles system prompts", func() {
				message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					System: []anthropic.TextBlockParam{
						{Text: "You are a helpful assistant. Always respond in uppercase letters."},
					},
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Say hello")),
					},
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(message.Content).ToNot(BeEmpty())
				Expect(len(message.Content)).To(BeNumerically(">=", 1))
			})

			It("returns usage information", func() {
				message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 100,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Hello")),
					},
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(message.Usage.InputTokens).To(BeNumerically(">", 0))
				Expect(message.Usage.OutputTokens).To(BeNumerically(">", 0))
			})
		})

		Context("Streaming responses", func() {
			It("streams tokens for a simple message", func() {
				stream := client.Messages.NewStreaming(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Count from 1 to 5")),
					},
				})

				message := anthropic.Message{}
				eventCount := 0
				hasContentDelta := false

				for stream.Next() {
					event := stream.Current()
					err := message.Accumulate(event)
					Expect(err).ToNot(HaveOccurred())
					eventCount++

					// Check for content block delta events
					switch event.AsAny().(type) {
					case anthropic.ContentBlockDeltaEvent:
						hasContentDelta = true
					}
				}

				Expect(stream.Err()).ToNot(HaveOccurred())
				Expect(eventCount).To(BeNumerically(">", 0))
				Expect(hasContentDelta).To(BeTrue())

				// Check accumulated message
				Expect(message.Content).ToNot(BeEmpty())
				// Role is a constant type that defaults to "assistant"
				Expect(string(message.Role)).To(Equal("assistant"))
			})

			It("streams with system prompt", func() {
				stream := client.Messages.NewStreaming(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					System: []anthropic.TextBlockParam{
						{Text: "You are a helpful assistant."},
					},
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Say hello")),
					},
				})

				message := anthropic.Message{}
				for stream.Next() {
					event := stream.Current()
					err := message.Accumulate(event)
					Expect(err).ToNot(HaveOccurred())
				}

				Expect(stream.Err()).ToNot(HaveOccurred())
				Expect(message.Content).ToNot(BeEmpty())
			})
		})

		Context("Tool calling", func() {
			It("handles tool calls in non-streaming mode", func() {
				message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather like in San Francisco?")),
					},
					Tools: []anthropic.ToolParam{
						{
							Name:        "get_weather",
							Description: anthropic.F("Get the current weather in a given location"),
							InputSchema: anthropic.F(map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type":        "string",
										"description": "The city and state, e.g. San Francisco, CA",
									},
								},
								"required": []string{"location"},
							}),
						},
					},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(message.Content).ToNot(BeEmpty())

				// Check if there's a tool use in the response
				hasToolUse := false
				for _, block := range message.Content {
					if block.Type == anthropic.ContentBlockTypeToolUse {
						hasToolUse = true
						Expect(block.Name).To(Equal("get_weather"))
						Expect(block.ID).ToNot(BeEmpty())
						// Verify that input contains location
						inputMap, ok := block.Input.(map[string]interface{})
						Expect(ok).To(BeTrue())
						_, hasLocation := inputMap["location"]
						Expect(hasLocation).To(BeTrue())
					}
				}

				// If tool_use is returned, verify stop reason
				if hasToolUse {
					Expect(message.StopReason).To(Equal(anthropic.MessageStopReasonToolUse))
				}
			})

			It("handles tool_choice parameter", func() {
				message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Tell me about the weather")),
					},
					Tools: []anthropic.ToolParam{
						{
							Name:        "get_weather",
							Description: anthropic.F("Get the current weather"),
							InputSchema: anthropic.F(map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type": "string",
									},
								},
							}),
						},
					},
					ToolChoice: anthropic.F[anthropic.ToolChoiceUnionParam](
						anthropic.ToolChoiceAutoParam{
							Type: anthropic.F(anthropic.ToolChoiceAutoTypeAuto),
						},
					),
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(message.Content).ToNot(BeEmpty())
			})

			It("handles tool results in messages", func() {
				// First, make a request that should trigger a tool call
				firstMessage, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather in SF?")),
					},
					Tools: []anthropic.ToolParam{
						{
							Name:        "get_weather",
							Description: anthropic.F("Get weather"),
							InputSchema: anthropic.F(map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{"type": "string"},
								},
							}),
						},
					},
				})

				Expect(err).ToNot(HaveOccurred())

				// Find the tool use block
				var toolUseID string
				var toolUseName string
				for _, block := range firstMessage.Content {
					if block.Type == anthropic.ContentBlockTypeToolUse {
						toolUseID = block.ID
						toolUseName = block.Name
						break
					}
				}

				// If we got a tool use, send back a tool result
				if toolUseID != "" {
					secondMessage, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
						Model:     "gpt-4",
						MaxTokens: 1024,
						Messages: []anthropic.MessageParam{
							anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather in SF?")),
							anthropic.NewAssistantMessage(firstMessage.Content...),
							anthropic.NewUserMessage(
								anthropic.NewToolResultBlock(toolUseID, "Sunny, 72°F", false),
							),
						},
						Tools: []anthropic.ToolParam{
							{
								Name:        toolUseName,
								Description: anthropic.F("Get weather"),
								InputSchema: anthropic.F(map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"location": map[string]interface{}{"type": "string"},
									},
								}),
							},
						},
					})

					Expect(err).ToNot(HaveOccurred())
					Expect(secondMessage.Content).ToNot(BeEmpty())
				}
			})
		})
	})
})
