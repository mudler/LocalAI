package e2e_test

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
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
					Tools: []anthropic.ToolUnionParam{
						anthropic.ToolUnionParam{
							OfTool: &anthropic.ToolParam{
								Name:        "get_weather",
								Description: anthropic.Opt("Get the current weather in a given location"),
								InputSchema: anthropic.ToolInputSchemaParam{
									Type: constant.ValueOf[constant.Object](),
									Properties: map[string]interface{}{
										"location": map[string]interface{}{
											"type":        "string",
											"description": "The city and state, e.g. San Francisco, CA",
										},
									},
									Required: []string{"location"},
								},
							},
						},
					},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(message.Content).ToNot(BeEmpty())

				// The model must use tools - find the tool use in the response
				hasToolUse := false
				for _, block := range message.Content {
					if block.Type == "tool_use" {
						hasToolUse = true
						Expect(block.Name).To(Equal("get_weather"))
						Expect(block.ID).ToNot(BeEmpty())
						// Verify that input contains location
						var inputMap map[string]interface{}
						err := json.Unmarshal(block.Input, &inputMap)
						Expect(err).ToNot(HaveOccurred())
						_, hasLocation := inputMap["location"]
						Expect(hasLocation).To(BeTrue())
					}
				}

				// Model must have called the tool
				Expect(hasToolUse).To(BeTrue(), "Model should have called the get_weather tool")
				Expect(message.StopReason).To(Equal(anthropic.MessageStopReasonToolUse))
			})

			It("handles tool_choice parameter", func() {
				message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("Tell me about the weather")),
					},
					Tools: []anthropic.ToolUnionParam{
						anthropic.ToolUnionParam{
							OfTool: &anthropic.ToolParam{
								Name:        "get_weather",
								Description: anthropic.Opt("Get the current weather"),
								InputSchema: anthropic.ToolInputSchemaParam{
									Type: constant.ValueOf[constant.Object](),
									Properties: map[string]interface{}{
										"location": map[string]interface{}{
											"type": "string",
										},
									},
								},
							},
						},
					},
					ToolChoice: anthropic.ToolChoiceUnionParam{
						OfAuto: &anthropic.ToolChoiceAutoParam{
							Type: constant.ValueOf[constant.Auto](),
						},
					},
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
					Tools: []anthropic.ToolUnionParam{
						anthropic.ToolUnionParam{
							OfTool: &anthropic.ToolParam{
								Name:        "get_weather",
								Description: anthropic.Opt("Get weather"),
								InputSchema: anthropic.ToolInputSchemaParam{
									Type: constant.ValueOf[constant.Object](),
									Properties: map[string]interface{}{
										"location": map[string]interface{}{"type": "string"},
									},
								},
							},
						},
					},
				})

				Expect(err).ToNot(HaveOccurred())

				// Find the tool use block - model must call the tool
				var toolUseID string
				var toolUseName string
				for _, block := range firstMessage.Content {
					if block.Type == "tool_use" {
						toolUseID = block.ID
						toolUseName = block.Name
						break
					}
				}

				// Model must have called the tool
				Expect(toolUseID).ToNot(BeEmpty(), "Model should have called the get_weather tool")

				// Convert ContentBlockUnion to ContentBlockParamUnion for NewAssistantMessage
				contentBlocks := make([]anthropic.ContentBlockParamUnion, len(firstMessage.Content))
				for i, block := range firstMessage.Content {
					if block.Type == "tool_use" {
						var inputMap map[string]interface{}
						if err := json.Unmarshal(block.Input, &inputMap); err == nil {
							contentBlocks[i] = anthropic.NewToolUseBlock(block.ID, inputMap, block.Name)
						} else {
							contentBlocks[i] = anthropic.NewToolUseBlock(block.ID, block.Input, block.Name)
						}
					} else if block.Type == "text" {
						contentBlocks[i] = anthropic.NewTextBlock(block.Text)
					}
				}

				// Send back a tool result and verify it's handled correctly
				secondMessage, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather in SF?")),
						anthropic.NewAssistantMessage(contentBlocks...),
						anthropic.NewUserMessage(
							anthropic.NewToolResultBlock(toolUseID, "Sunny, 72°F", false),
						),
					},
					Tools: []anthropic.ToolUnionParam{
						anthropic.ToolUnionParam{
							OfTool: &anthropic.ToolParam{
								Name:        toolUseName,
								Description: anthropic.Opt("Get weather"),
								InputSchema: anthropic.ToolInputSchemaParam{
									Type: constant.ValueOf[constant.Object](),
									Properties: map[string]interface{}{
										"location": map[string]interface{}{"type": "string"},
									},
								},
							},
						},
					},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(secondMessage.Content).ToNot(BeEmpty())
			})

			It("handles tool calls in streaming mode", func() {
				stream := client.Messages.NewStreaming(context.TODO(), anthropic.MessageNewParams{
					Model:     "gpt-4",
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("What's the weather like in San Francisco?")),
					},
					Tools: []anthropic.ToolUnionParam{
						anthropic.ToolUnionParam{
							OfTool: &anthropic.ToolParam{
								Name:        "get_weather",
								Description: anthropic.Opt("Get the current weather in a given location"),
								InputSchema: anthropic.ToolInputSchemaParam{
									Type: constant.ValueOf[constant.Object](),
									Properties: map[string]interface{}{
										"location": map[string]interface{}{
											"type":        "string",
											"description": "The city and state, e.g. San Francisco, CA",
										},
									},
									Required: []string{"location"},
								},
							},
						},
					},
				})

				message := anthropic.Message{}
				eventCount := 0
				hasContentBlockStart := false
				hasContentBlockDelta := false
				hasContentBlockStop := false

				for stream.Next() {
					event := stream.Current()
					err := message.Accumulate(event)
					Expect(err).ToNot(HaveOccurred())
					eventCount++

					// Check for different event types related to tool use
					switch e := event.AsAny().(type) {
					case anthropic.ContentBlockStartEvent:
						hasContentBlockStart = true
						if e.ContentBlock.Type == "tool_use" {
							// Tool use block detected
						}
					case anthropic.ContentBlockDeltaEvent:
						hasContentBlockDelta = true
					case anthropic.ContentBlockStopEvent:
						hasContentBlockStop = true
					}
				}

				Expect(stream.Err()).ToNot(HaveOccurred())
				Expect(eventCount).To(BeNumerically(">", 0))

				// Verify streaming events were emitted
				Expect(hasContentBlockStart).To(BeTrue(), "Should have content_block_start event")
				Expect(hasContentBlockDelta).To(BeTrue(), "Should have content_block_delta event")
				Expect(hasContentBlockStop).To(BeTrue(), "Should have content_block_stop event")

				// Check accumulated message has tool use
				Expect(message.Content).ToNot(BeEmpty())

				// Model must have called the tool
				foundToolUse := false
				for _, block := range message.Content {
					if block.Type == "tool_use" {
						foundToolUse = true
						Expect(block.Name).To(Equal("get_weather"))
						Expect(block.ID).ToNot(BeEmpty())
					}
				}
				Expect(foundToolUse).To(BeTrue(), "Model should have called the get_weather tool in streaming mode")
				Expect(message.StopReason).To(Equal(anthropic.MessageStopReasonToolUse))
			})
		})
	})
})
