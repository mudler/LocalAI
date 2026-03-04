package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

var _ = Describe("E2E test", func() {
	Context("Generating", func() {
		BeforeEach(func() {
			//
		})

		// Check that the GPU was used
		AfterEach(func() {
			//
		})

		Context("text", func() {
			It("correctly", func() {
				model := "gpt-4"
				resp, err := client.Chat.Completions.New(context.TODO(),
					openai.ChatCompletionNewParams{
						Model: model,
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("How much is 2+2?"),
						},
					})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Choices[0].Message.Content).To(Or(ContainSubstring("4"), ContainSubstring("four")), fmt.Sprint(resp.Choices[0].Message.Content))
			})
		})

		Context("function calls", func() {
			It("correctly invoke", func() {
				params := openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]string{
							"type":        "string",
							"description": "The city and state, e.g. San Francisco, CA",
						},
						"unit": map[string]any{
							"type": "string",
							"enum": []string{"celsius", "fahrenheit"},
						},
					},
					"required": []string{"location"},
				}

				tool := openai.ChatCompletionToolUnionParam{
					OfFunction: &openai.ChatCompletionFunctionToolParam{
						Function: openai.FunctionDefinitionParam{
							Name:        "get_current_weather",
							Description: openai.String("Get the current weather in a given location"),
							Parameters:  params,
						},
					},
				}

				resp, err := client.Chat.Completions.New(context.TODO(),
					openai.ChatCompletionNewParams{
						Model:    openai.ChatModelGPT4,
						Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("What is the weather in Boston today?")},
						Tools:    []openai.ChatCompletionToolUnionParam{tool},
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1), fmt.Sprint(resp))

				msg := resp.Choices[0].Message
				Expect(len(msg.ToolCalls)).To(Equal(1), fmt.Sprint(msg.ToolCalls))
				Expect(msg.ToolCalls[0].Function.Name).To(Equal("get_current_weather"), fmt.Sprint(msg.ToolCalls[0].Function.Name))
				Expect(msg.ToolCalls[0].Function.Arguments).To(ContainSubstring("Boston"), fmt.Sprint(msg.ToolCalls[0].Function.Arguments))
			})
		})

		Context("json", func() {
			It("correctly", func() {
				model := "gpt-4"

				resp, err := client.Chat.Completions.New(context.TODO(),
					openai.ChatCompletionNewParams{
						Model: model,
						Messages: []openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Generate a JSON object of an animal with 'name', 'gender' and 'legs' fields"),
						},
						ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
							OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
						},
					})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1), fmt.Sprint(resp))

				var i map[string]interface{}
				err = json.Unmarshal([]byte(resp.Choices[0].Message.Content), &i)
				Expect(err).ToNot(HaveOccurred())
				Expect(i).To(HaveKey("name"))
				Expect(i).To(HaveKey("gender"))
				Expect(i).To(HaveKey("legs"))
			})
		})

		Context("images", func() {
			It("correctly", func() {
				resp, err := client.Images.Generate(context.TODO(),
					openai.ImageGenerateParams{
						Prompt:  "test",
						Size:    openai.ImageGenerateParamsSize256x256,
						Quality: openai.ImageGenerateParamsQualityLow,
					})
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error sending image request"))
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].URL).To(ContainSubstring("png"), fmt.Sprint(resp.Data[0].URL))
			})
			It("correctly changes the response format to url", func() {
				resp, err := client.Images.Generate(context.TODO(),
					openai.ImageGenerateParams{
						Prompt:         "test",
						Size:           openai.ImageGenerateParamsSize256x256,
						ResponseFormat: openai.ImageGenerateParamsResponseFormatURL,
						Quality:        openai.ImageGenerateParamsQualityLow,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].URL).To(ContainSubstring("png"), fmt.Sprint(resp.Data[0].URL))
			})
			It("correctly changes the response format to base64", func() {
				resp, err := client.Images.Generate(context.TODO(),
					openai.ImageGenerateParams{
						Prompt:         "test",
						Size:           openai.ImageGenerateParamsSize256x256,
						ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].B64JSON).ToNot(BeEmpty(), fmt.Sprint(resp.Data[0].B64JSON))
			})
		})

		Context("embeddings", func() {
			It("correctly", func() {
				resp, err := client.Embeddings.New(context.TODO(),
					openai.EmbeddingNewParams{
						Input: openai.EmbeddingNewParamsInputUnion{
							OfArrayOfStrings: []string{"doc"},
						},
						Model: openai.EmbeddingModelTextEmbeddingAda002,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].Embedding).ToNot(BeEmpty())

				resp2, err := client.Embeddings.New(context.TODO(),
					openai.EmbeddingNewParams{
						Input: openai.EmbeddingNewParamsInputUnion{
							OfArrayOfStrings: []string{"cat"},
						},
						Model: openai.EmbeddingModelTextEmbeddingAda002,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp2.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp2.Data[0].Embedding).ToNot(BeEmpty())
				Expect(resp2.Data[0].Embedding).ToNot(Equal(resp.Data[0].Embedding))

				resp3, err := client.Embeddings.New(context.TODO(),
					openai.EmbeddingNewParams{
						Input: openai.EmbeddingNewParamsInputUnion{
							OfArrayOfStrings: []string{"doc", "cat"},
						},
						Model: openai.EmbeddingModelTextEmbeddingAda002,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp3.Data)).To(Equal(2), fmt.Sprint(resp))
				Expect(resp3.Data[0].Embedding).ToNot(BeEmpty())
				Expect(resp3.Data[0].Embedding).To(Equal(resp.Data[0].Embedding))
				Expect(resp3.Data[1].Embedding).To(Equal(resp2.Data[0].Embedding))
				Expect(resp3.Data[0].Embedding).ToNot(Equal(resp3.Data[1].Embedding))
			})
		})

		Context("vision", func() {
			It("correctly", func() {
				model := "gpt-4o"
				resp, err := client.Chat.Completions.New(context.TODO(),
					openai.ChatCompletionNewParams{
						Model: model,
						Messages: []openai.ChatCompletionMessageParamUnion{
							{
								OfUser: &openai.ChatCompletionUserMessageParam{
									Role: "user",
									Content: openai.ChatCompletionUserMessageParamContentUnion{
										OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
											{
												OfText: &openai.ChatCompletionContentPartTextParam{
													Type: "text",
													Text: "What is in the image?",
												},
											},
											{
												OfImageURL: &openai.ChatCompletionContentPartImageParam{
													ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
														URL:    "https://picsum.photos/id/22/4434/3729",
														Detail: "low",
													},
												},
											},
										},
									},
								},
							},
						},
					})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Choices[0].Message.Content).To(Or(ContainSubstring("man"), ContainSubstring("road")), fmt.Sprint(resp.Choices[0].Message.Content))
			})
		})

		Context("text to audio", func() {
			It("correctly", func() {
				res, err := client.Audio.Speech.New(context.Background(), openai.AudioSpeechNewParams{
					Model: openai.SpeechModelTTS1,
					Input: "Hello!",
					Voice: openai.AudioSpeechNewParamsVoiceAlloy,
				})
				Expect(err).ToNot(HaveOccurred())
				defer res.Body.Close()

				_, err = io.ReadAll(res.Body)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("audio to text", func() {
			It("correctly", func() {
				downloadURL := "https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav"
				file, err := downloadHttpFile(downloadURL)
				Expect(err).ToNot(HaveOccurred())

				fileHandle, err := os.Open(file)
				Expect(err).ToNot(HaveOccurred())
				defer fileHandle.Close()

				transcriptionResp, err := client.Audio.Transcriptions.New(context.Background(), openai.AudioTranscriptionNewParams{
					Model: openai.AudioModelWhisper1,
					File:  fileHandle,
				})
				Expect(err).ToNot(HaveOccurred())
				resp := transcriptionResp.AsTranscription()
				Expect(resp.Text).To(ContainSubstring("This is the"), fmt.Sprint(resp.Text))
			})

			It("with VTT format", func() {
				downloadURL := "https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav"
				file, err := downloadHttpFile(downloadURL)
				Expect(err).ToNot(HaveOccurred())

				fileHandle, err := os.Open(file)
				Expect(err).ToNot(HaveOccurred())
				defer fileHandle.Close()

				var resp string
				_, err = client.Audio.Transcriptions.New(context.Background(), openai.AudioTranscriptionNewParams{
					Model:          openai.AudioModelWhisper1,
					File:           fileHandle,
					ResponseFormat: openai.AudioResponseFormatVTT,
				}, option.WithResponseBodyInto(&resp))
				Expect(err).ToNot(HaveOccurred())
				Expect(resp).To(ContainSubstring("This is the"), resp)
				Expect(resp).To(ContainSubstring("WEBVTT"), resp)
				Expect(resp).To(ContainSubstring("00:00:00.000 -->"), resp)
			})
		})

		Context("vad", func() {
			It("correctly", func() {
				modelName := "silero-vad"
				req := schema.VADRequest{
					BasicModelRequest: schema.BasicModelRequest{
						Model: modelName,
					},
					Audio: SampleVADAudio, // Use hardcoded sample data for now.
				}
				serialized, err := json.Marshal(req)
				Expect(err).To(BeNil())
				Expect(serialized).ToNot(BeNil())

				vadEndpoint := apiEndpoint + "/vad"
				resp, err := http.Post(vadEndpoint, "application/json", bytes.NewReader(serialized))
				Expect(err).To(BeNil())
				Expect(resp).ToNot(BeNil())

				body, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
				deserializedResponse := schema.VADResponse{}
				err = json.Unmarshal(body, &deserializedResponse)
				Expect(err).To(BeNil())
				Expect(deserializedResponse).ToNot(BeZero())
				Expect(deserializedResponse.Segments).ToNot(BeZero())
			})
		})

		Context("reranker", func() {
			It("correctly", func() {
				modelName := "jina-reranker-v1-base-en"
				const query = "Organic skincare products for sensitive skin"
				var documents = []string{
					"Eco-friendly kitchenware for modern homes",
					"Biodegradable cleaning supplies for eco-conscious consumers",
					"Organic cotton baby clothes for sensitive skin",
					"Natural organic skincare range for sensitive skin",
					"Tech gadgets for smart homes: 2024 edition",
					"Sustainable gardening tools and compost solutions",
					"Sensitive skin-friendly facial cleansers and toners",
					"Organic food wraps and storage solutions",
					"All-natural pet food for dogs with allergies",
					"Yoga mats made from recycled materials",
				}
				// Exceed len or requested results
				randomValue := int(GinkgoRandomSeed()) % (len(documents) + 1)
				requestResults := randomValue + 1 // at least 1 results
				// Cap expectResults by the length of documents
				expectResults := min(requestResults, len(documents))
				var maybeSkipTopN = &requestResults
				if requestResults >= len(documents) && int(GinkgoRandomSeed())%2 == 0 {
					maybeSkipTopN = nil
				}

				resp, body := requestRerank(modelName, query, documents, maybeSkipTopN, apiEndpoint)
				Expect(resp.StatusCode).To(Equal(200), fmt.Sprintf("body: %s, response: %+v", body, resp))

				deserializedResponse := schema.JINARerankResponse{}
				err := json.Unmarshal(body, &deserializedResponse)
				Expect(err).To(BeNil())
				Expect(deserializedResponse).ToNot(BeZero())
				Expect(deserializedResponse.Model).To(Equal(modelName))
				Expect(len(deserializedResponse.Results)).To(Equal(expectResults))
				// Assert that relevance scores are in decreasing order
				for i := 1; i < len(deserializedResponse.Results); i++ {
					Expect(deserializedResponse.Results[i].RelevanceScore).To(
						BeNumerically("<=", deserializedResponse.Results[i-1].RelevanceScore),
						fmt.Sprintf("Result at index %d should have lower relevance score than previous result.", i),
					)
				}
				// Assert that each result's index points to the correct document
				for i, result := range deserializedResponse.Results {
					Expect(result.Index).To(
						And(
							BeNumerically(">=", 0),
							BeNumerically("<", len(documents)),
						),
						fmt.Sprintf("Result at position %d has index %d which should be within bounds [0, %d)", i, result.Index, len(documents)),
					)
					Expect(result.Document.Text).To(
						Equal(documents[result.Index]),
						fmt.Sprintf("Result at position %d (index %d) should have document text '%s', but got '%s'",
							i, result.Index, documents[result.Index], result.Document.Text),
					)
				}
				zeroOrNeg := int(GinkgoRandomSeed())%2 - 1 // Results in either -1 or 0
				resp, body = requestRerank(modelName, query, documents, &zeroOrNeg, apiEndpoint)
				Expect(resp.StatusCode).To(Equal(422), fmt.Sprintf("body: %s, response: %+v", body, resp))
			})
		})
	})
})

func downloadHttpFile(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tmpfile, err := os.CreateTemp("", "example")
	if err != nil {
		return "", err
	}
	defer tmpfile.Close()

	_, err = io.Copy(tmpfile, resp.Body)
	if err != nil {
		return "", err
	}

	return tmpfile.Name(), nil
}

func requestRerank(modelName, query string, documents []string, topN *int, apiEndpoint string) (*http.Response, []byte) {
	req := schema.JINARerankRequest{
		BasicModelRequest: schema.BasicModelRequest{
			Model: modelName,
		},
		Query:     query,
		Documents: documents,
		TopN:      topN,
	}

	serialized, err := json.Marshal(req)
	Expect(err).To(BeNil())
	Expect(serialized).ToNot(BeNil())
	rerankerEndpoint := apiEndpoint + "/rerank"
	resp, err := http.Post(rerankerEndpoint, "application/json", bytes.NewReader(serialized))
	Expect(err).To(BeNil())
	Expect(resp).ToNot(BeNil())
	body, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())

	return resp, body
}
