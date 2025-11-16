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
	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
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
				resp, err := client.CreateChatCompletion(context.TODO(),
					openai.ChatCompletionRequest{
						Model: model, Messages: []openai.ChatCompletionMessage{
							{
								Role:    "user",
								Content: "How much is 2+2?",
							},
						}})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Choices[0].Message.Content).To(Or(ContainSubstring("4"), ContainSubstring("four")), fmt.Sprint(resp.Choices[0].Message.Content))
			})
		})

		Context("function calls", func() {
			It("correctly invoke", func() {
				params := jsonschema.Definition{
					Type: jsonschema.Object,
					Properties: map[string]jsonschema.Definition{
						"location": {
							Type:        jsonschema.String,
							Description: "The city and state, e.g. San Francisco, CA",
						},
						"unit": {
							Type: jsonschema.String,
							Enum: []string{"celsius", "fahrenheit"},
						},
					},
					Required: []string{"location"},
				}

				f := openai.FunctionDefinition{
					Name:        "get_current_weather",
					Description: "Get the current weather in a given location",
					Parameters:  params,
				}
				t := openai.Tool{
					Type:     openai.ToolTypeFunction,
					Function: &f,
				}

				dialogue := []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleUser, Content: "What is the weather in Boston today?"},
				}
				resp, err := client.CreateChatCompletion(context.TODO(),
					openai.ChatCompletionRequest{
						Model:    openai.GPT4,
						Messages: dialogue,
						Tools:    []openai.Tool{t},
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

				req := openai.ChatCompletionRequest{
					ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
					Model:          model,
					Messages: []openai.ChatCompletionMessage{
						{

							Role:    "user",
							Content: "Generate a JSON object of an animal with 'name', 'gender' and 'legs' fields",
						},
					},
				}

				resp, err := client.CreateChatCompletion(context.TODO(), req)
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
				req := openai.ImageRequest{
					Prompt:  "test",
					Quality: "1",
					Size:    openai.CreateImageSize256x256,
				}
				resp, err := client.CreateImage(context.TODO(), req)
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error sending image request %+v", req))
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].URL).To(ContainSubstring("png"), fmt.Sprint(resp.Data[0].URL))
			})
			It("correctly changes the response format to url", func() {
				resp, err := client.CreateImage(context.TODO(),
					openai.ImageRequest{
						Prompt:         "test",
						Size:           openai.CreateImageSize256x256,
						Quality:        "1",
						ResponseFormat: openai.CreateImageResponseFormatURL,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].URL).To(ContainSubstring("png"), fmt.Sprint(resp.Data[0].URL))
			})
			It("correctly changes the response format to base64", func() {
				resp, err := client.CreateImage(context.TODO(),
					openai.ImageRequest{
						Prompt:         "test",
						Size:           openai.CreateImageSize256x256,
						Quality:        "1",
						ResponseFormat: openai.CreateImageResponseFormatB64JSON,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].B64JSON).ToNot(BeEmpty(), fmt.Sprint(resp.Data[0].B64JSON))
			})
		})
		Context("embeddings", func() {
			It("correctly", func() {
				resp, err := client.CreateEmbeddings(context.TODO(),
					openai.EmbeddingRequestStrings{
						Input: []string{"doc"},
						Model: openai.AdaEmbeddingV2,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Data[0].Embedding).ToNot(BeEmpty())

				resp2, err := client.CreateEmbeddings(context.TODO(),
					openai.EmbeddingRequestStrings{
						Input: []string{"cat"},
						Model: openai.AdaEmbeddingV2,
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp2.Data)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp2.Data[0].Embedding).ToNot(BeEmpty())
				Expect(resp2.Data[0].Embedding).ToNot(Equal(resp.Data[0].Embedding))

				resp3, err := client.CreateEmbeddings(context.TODO(),
					openai.EmbeddingRequestStrings{
						Input: []string{"doc", "cat"},
						Model: openai.AdaEmbeddingV2,
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
				resp, err := client.CreateChatCompletion(context.TODO(),
					openai.ChatCompletionRequest{
						Model: model, Messages: []openai.ChatCompletionMessage{
							{

								Role: "user",
								MultiContent: []openai.ChatMessagePart{
									{
										Type: openai.ChatMessagePartTypeText,
										Text: "What is in the image?",
									},
									{
										Type: openai.ChatMessagePartTypeImageURL,
										ImageURL: &openai.ChatMessageImageURL{
											URL:    "https://picsum.photos/id/22/4434/3729",
											Detail: openai.ImageURLDetailLow,
										},
									},
								},
							},
						}})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(resp.Choices)).To(Equal(1), fmt.Sprint(resp))
				Expect(resp.Choices[0].Message.Content).To(Or(ContainSubstring("man"), ContainSubstring("road")), fmt.Sprint(resp.Choices[0].Message.Content))
			})
		})
		Context("text to audio", func() {
			It("correctly", func() {
				res, err := client.CreateSpeech(context.Background(), openai.CreateSpeechRequest{
					Model: openai.TTSModel1,
					Input: "Hello!",
					Voice: openai.VoiceAlloy,
				})
				Expect(err).ToNot(HaveOccurred())
				defer res.Close()

				_, err = io.ReadAll(res)
				Expect(err).ToNot(HaveOccurred())

			})
		})
		Context("audio to text", func() {
			It("correctly", func() {

				downloadURL := "https://cdn.openai.com/whisper/draft-20220913a/micro-machines.wav"
				file, err := downloadHttpFile(downloadURL)
				Expect(err).ToNot(HaveOccurred())

				req := openai.AudioRequest{
					Model:    openai.Whisper1,
					FilePath: file,
				}
				resp, err := client.CreateTranscription(context.Background(), req)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.Text).To(ContainSubstring("This is the"), fmt.Sprint(resp.Text))
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
				//Expect(len(deserializedResponse.Results)).To(BeNumerically(">", 0))
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
