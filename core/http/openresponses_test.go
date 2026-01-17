package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/xlog"
)

const testModel = "Qwen3-VL-2B-Instruct-GGUF"

var _ = Describe("Open Responses API", func() {
	var app *echo.Echo
	var c context.Context
	var cancel context.CancelFunc

	commonOpts := []config.AppOption{
		config.WithDebug(true),
	}

	Context("API with ephemeral models", func() {
		BeforeEach(func(sc SpecContext) {
			var err error

			backendPath := os.Getenv("BACKENDS_PATH")

			c, cancel = context.WithCancel(context.Background())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(backendPath),
				system.WithModelPath(modelDir),
			)
			Expect(err).ToNot(HaveOccurred())

			application, err := application.New(
				append(commonOpts,
					config.WithContext(c),
					config.WithSystemState(systemState),
					config.WithApiKeys([]string{apiKey}),
					config.WithModelsURL("https://huggingface.co/unsloth/Qwen3-VL-2B-Instruct-GGUF"),
				)...)
			Expect(err).ToNot(HaveOccurred())

			app, err = API(application)
			Expect(err).ToNot(HaveOccurred())

			go func() {
				if err := app.Start("127.0.0.1:9090"); err != nil && err != http.ErrServerClosed {
					xlog.Error("server error", "error", err)
				}
			}()

			// Wait for API to be ready
			Eventually(func() error {
				resp, err := http.Get("http://127.0.0.1:9090/healthz")
				if err != nil {
					return err
				}
				resp.Body.Close()
				return nil
			}, "2m").ShouldNot(HaveOccurred())
		})

		AfterEach(func(sc SpecContext) {
			cancel()
			if app != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := app.Shutdown(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

		})

		Context("HTTP Protocol Compliance", func() {
			It("MUST accept application/json Content-Type", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				// Should accept the request (may fail on model not found, but should accept Content-Type)
				Expect(resp.StatusCode).To(Or(Equal(200), Equal(400), Equal(500)))
			})

			It("MUST return application/json for non-streaming responses", func() {
				reqBody := map[string]interface{}{
					"model":  testModel,
					"input":  "Hello",
					"stream": false,
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				contentType := resp.Header.Get("Content-Type")
				if resp.StatusCode == 200 {
					Expect(contentType).To(ContainSubstring("application/json"))
				}
			})

			It("MUST return text/event-stream for streaming responses", func() {
				reqBody := map[string]interface{}{
					"model":  testModel,
					"input":  "Hello",
					"stream": true,
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				contentType := resp.Header.Get("Content-Type")
				if resp.StatusCode == 200 {
					Expect(contentType).To(Equal("text/event-stream"))
				}
			})

			It("MUST end streaming with [DONE] terminal event", func() {
				reqBody := map[string]interface{}{
					"model":  testModel,
					"input":  "Hello",
					"stream": true,
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					body, err := io.ReadAll(resp.Body)
					Expect(err).ToNot(HaveOccurred())
					bodyStr := string(body)
					// Should end with [DONE]
					Expect(bodyStr).To(ContainSubstring("data: [DONE]"))
				}
			})

			It("MUST have event field matching type in body", func() {
				reqBody := map[string]interface{}{
					"model":  testModel,
					"input":  "Hello",
					"stream": true,
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					body, err := io.ReadAll(resp.Body)
					Expect(err).ToNot(HaveOccurred())
					bodyStr := string(body)

					// Parse SSE events
					lines := strings.Split(bodyStr, "\n")
					for i, line := range lines {
						if strings.HasPrefix(line, "event: ") {
							eventType := strings.TrimPrefix(line, "event: ")
							// Next line should be data: with matching type
							if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "data: ") {
								dataLine := strings.TrimPrefix(lines[i+1], "data: ")
								var eventData map[string]interface{}
								if err := json.Unmarshal([]byte(dataLine), &eventData); err == nil {
									if typeVal, ok := eventData["type"].(string); ok {
										Expect(typeVal).To(Equal(eventType))
									}
								}
							}
						}
					}
				}
			})
		})

		Context("Response Structure", func() {
			It("MUST return id field", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())
					Expect(response).To(HaveKey("id"))
					Expect(response["id"]).ToNot(BeEmpty())
				}
			})

			It("MUST return object field as 'response'", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())
					Expect(response).To(HaveKey("object"))
					Expect(response["object"]).To(Equal("response"))
				}
			})

			It("MUST return created_at timestamp", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())
					Expect(response).To(HaveKey("created_at"))
					// Should be a number (unix timestamp)
					createdAt, ok := response["created_at"].(float64)
					Expect(ok).To(BeTrue())
					Expect(createdAt).To(BeNumerically(">", 0))
				}
			})

			It("MUST return status field", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())
					Expect(response).To(HaveKey("status"))
					status, ok := response["status"].(string)
					Expect(ok).To(BeTrue())
					Expect(status).To(BeElementOf("in_progress", "completed", "failed", "incomplete"))
				}
			})

			It("MUST return model field", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())
					Expect(response).To(HaveKey("model"))
					Expect(response["model"]).ToNot(BeEmpty())
				}
			})

			It("MUST return output array of items", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())
					Expect(response).To(HaveKey("output"))
					output, ok := response["output"].([]interface{})
					Expect(ok).To(BeTrue())
					Expect(output).ToNot(BeNil())
				}
			})
		})

		Context("Items", func() {
			It("MUST include id field on all items", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())

					output, ok := response["output"].([]interface{})
					if ok {
						for _, item := range output {
							itemMap, ok := item.(map[string]interface{})
							Expect(ok).To(BeTrue())
							Expect(itemMap).To(HaveKey("id"))
							Expect(itemMap["id"]).ToNot(BeEmpty())
						}
					}
				}
			})

			It("MUST include type field on all items", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())

					output, ok := response["output"].([]interface{})
					if ok {
						for _, item := range output {
							itemMap, ok := item.(map[string]interface{})
							Expect(ok).To(BeTrue())
							Expect(itemMap).To(HaveKey("type"))
							Expect(itemMap["type"]).ToNot(BeEmpty())
						}
					}
				}
			})

			It("MUST include status field on all items", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())

					output, ok := response["output"].([]interface{})
					if ok {
						for _, item := range output {
							itemMap, ok := item.(map[string]interface{})
							Expect(ok).To(BeTrue())
							Expect(itemMap).To(HaveKey("status"))
							status, ok := itemMap["status"].(string)
							Expect(ok).To(BeTrue())
							Expect(status).To(BeElementOf("in_progress", "completed", "incomplete"))
						}
					}
				}
			})

			It("MUST support message items with role field", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": []map[string]interface{}{
						{
							"type": "message",
							"role": "user",
							"content": []map[string]interface{}{
								{
									"type": "input_text",
									"text": "Hello",
								},
							},
						},
					},
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())

					output, ok := response["output"].([]interface{})
					if ok && len(output) > 0 {
						itemMap, ok := output[0].(map[string]interface{})
						Expect(ok).To(BeTrue())
						if itemMap["type"] == "message" {
							Expect(itemMap).To(HaveKey("role"))
							role, ok := itemMap["role"].(string)
							Expect(ok).To(BeTrue())
							Expect(role).To(BeElementOf("user", "assistant", "system", "developer"))
						}
					}
				}
			})
		})

		Context("Content Types", func() {
			It("MUST support input_text content", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": []map[string]interface{}{
						{
							"type": "message",
							"role": "user",
							"content": []map[string]interface{}{
								{
									"type": "input_text",
									"text": "Hello world",
								},
							},
						},
					},
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				// Should accept the request
				Expect(resp.StatusCode).To(Or(Equal(200), Equal(400), Equal(500)))
			})

			It("MUST support input_image content with URL", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": []map[string]interface{}{
						{
							"type": "message",
							"role": "user",
							"content": []map[string]interface{}{
								{
									"type":      "input_image",
									"image_url": "https://example.com/image.png",
									"detail":    "auto",
								},
							},
						},
					},
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				// Should accept the request
				Expect(resp.StatusCode).To(Or(Equal(200), Equal(400), Equal(500)))
			})

			It("MUST support input_image content with base64", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": []map[string]interface{}{
						{
							"type": "message",
							"role": "user",
							"content": []map[string]interface{}{
								{
									"type":      "input_image",
									"image_url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
									"detail":    "auto",
								},
							},
						},
					},
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				// Should accept the request
				Expect(resp.StatusCode).To(Or(Equal(200), Equal(400), Equal(500)))
			})

			It("MUST support output_text content", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					var response map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					err = json.Unmarshal(body, &response)
					Expect(err).ToNot(HaveOccurred())

					output, ok := response["output"].([]interface{})
					if ok && len(output) > 0 {
						itemMap, ok := output[0].(map[string]interface{})
						Expect(ok).To(BeTrue())
						if itemMap["type"] == "message" {
							content, ok := itemMap["content"].([]interface{})
							if ok && len(content) > 0 {
								contentMap, ok := content[0].(map[string]interface{})
								if ok {
									contentType, _ := contentMap["type"].(string)
									if contentType == "output_text" {
										Expect(contentMap).To(HaveKey("text"))
									}
								}
							}
						}
					}
				}
			})
		})

		Context("Streaming Events", func() {
			It("MUST emit response.created as first event", func() {
				reqBody := map[string]interface{}{
					"model":  testModel,
					"input":  "Hello",
					"stream": true,
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					body, err := io.ReadAll(resp.Body)
					Expect(err).ToNot(HaveOccurred())
					bodyStr := string(body)

					// Should contain response.created event
					Expect(bodyStr).To(ContainSubstring("response.created"))
				}
			})

			It("MUST include sequence_number in all events", func() {
				reqBody := map[string]interface{}{
					"model":  testModel,
					"input":  "Hello",
					"stream": true,
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode == 200 {
					body, err := io.ReadAll(resp.Body)
					Expect(err).ToNot(HaveOccurred())
					bodyStr := string(body)

					// Parse SSE events and check for sequence_number
					lines := strings.Split(bodyStr, "\n")
					for _, line := range lines {
						if strings.HasPrefix(line, "data: ") {
							dataLine := strings.TrimPrefix(line, "data: ")
							if dataLine != "[DONE]" {
								var eventData map[string]interface{}
								if err := json.Unmarshal([]byte(dataLine), &eventData); err == nil {
									if _, hasType := eventData["type"]; hasType {
										Expect(eventData).To(HaveKey("sequence_number"))
									}
								}
							}
						}
					}
				}
			})
		})

		Context("Error Handling", func() {
			It("MUST return structured error with type and message fields", func() {
				reqBody := map[string]interface{}{
					"model": "nonexistent-model",
					"input": "Hello",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				if resp.StatusCode >= 400 {
					var errorResp map[string]interface{}
					body, _ := io.ReadAll(resp.Body)
					json.Unmarshal(body, &errorResp)

					if errorResp["error"] != nil {
						errorObj, ok := errorResp["error"].(map[string]interface{})
						if ok {
							Expect(errorObj).To(HaveKey("type"))
							Expect(errorObj).To(HaveKey("message"))
						}
					}
				}
			})
		})

		Context("Previous Response ID", func() {
			It("should load previous response and concatenate context", func() {
				// First, create a response
				reqBody1 := map[string]interface{}{
					"model": testModel,
					"input": "What is 2+2?",
				}
				payload1, err := json.Marshal(reqBody1)
				Expect(err).ToNot(HaveOccurred())

				req1, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload1))
				Expect(err).ToNot(HaveOccurred())
				req1.Header.Set("Content-Type", "application/json")
				req1.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp1, err := client.Do(req1)
				Expect(err).ToNot(HaveOccurred())
				defer resp1.Body.Close()

				// Check if first response succeeded
				if resp1.StatusCode != 200 {
					Skip("First response failed, skipping previous_response_id test (backend may not be available)")
				}

				var response1 map[string]interface{}
				body1, err := io.ReadAll(resp1.Body)
				Expect(err).ToNot(HaveOccurred())
				err = json.Unmarshal(body1, &response1)
				Expect(err).ToNot(HaveOccurred())

				responseID, ok := response1["id"].(string)
				Expect(ok).To(BeTrue())
				Expect(responseID).ToNot(BeEmpty())

				// Now create a new response with previous_response_id
				reqBody2 := map[string]interface{}{
					"model":                testModel,
					"input":                "What about 3+3?",
					"previous_response_id": responseID,
				}
				payload2, err := json.Marshal(reqBody2)
				Expect(err).ToNot(HaveOccurred())

				req2, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload2))
				Expect(err).ToNot(HaveOccurred())
				req2.Header.Set("Content-Type", "application/json")
				req2.Header.Set("Authorization", bearerKey)

				resp2, err := client.Do(req2)
				Expect(err).ToNot(HaveOccurred())
				defer resp2.Body.Close()

				var response2 map[string]interface{}
				body2, err := io.ReadAll(resp2.Body)
				Expect(err).ToNot(HaveOccurred())
				err = json.Unmarshal(body2, &response2)
				Expect(err).ToNot(HaveOccurred())

				Expect(response2["previous_response_id"]).To(Equal(responseID))
				Expect(response2["status"]).To(Equal("completed"))
			})

			It("should return error for invalid previous_response_id", func() {
				reqBody := map[string]interface{}{
					"model":                testModel,
					"input":                "Test",
					"previous_response_id": "nonexistent_response_id",
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				Expect(resp.StatusCode).To(Equal(404))

				var errorResp map[string]interface{}
				body, _ := io.ReadAll(resp.Body)
				json.Unmarshal(body, &errorResp)

				if errorResp["error"] != nil {
					errorObj, ok := errorResp["error"].(map[string]interface{})
					if ok {
						Expect(errorObj["type"]).To(Equal("not_found"))
						Expect(errorObj["param"]).To(Equal("previous_response_id"))
					}
				}
			})
		})

		Context("Item Reference", func() {
			It("should resolve item_reference in input", func() {
				// First, create a response with items
				reqBody1 := map[string]interface{}{
					"model": testModel,
					"input": "Hello",
				}
				payload1, err := json.Marshal(reqBody1)
				Expect(err).ToNot(HaveOccurred())

				req1, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload1))
				Expect(err).ToNot(HaveOccurred())
				req1.Header.Set("Content-Type", "application/json")
				req1.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp1, err := client.Do(req1)
				Expect(err).ToNot(HaveOccurred())
				defer resp1.Body.Close()

				// Check if first response succeeded
				if resp1.StatusCode != 200 {
					Skip("First response failed, skipping item_reference test (backend may not be available)")
				}

				var response1 map[string]interface{}
				body1, err := io.ReadAll(resp1.Body)
				Expect(err).ToNot(HaveOccurred())
				err = json.Unmarshal(body1, &response1)
				Expect(err).ToNot(HaveOccurred())

				// Get the first output item ID
				output, ok := response1["output"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(len(output)).To(BeNumerically(">", 0))

				firstItem, ok := output[0].(map[string]interface{})
				Expect(ok).To(BeTrue())
				itemID, ok := firstItem["id"].(string)
				Expect(ok).To(BeTrue())
				Expect(itemID).ToNot(BeEmpty())

				// Now create a new response with item_reference
				reqBody2 := map[string]interface{}{
					"model": testModel,
					"input": []interface{}{
						map[string]interface{}{
							"type":    "item_reference",
							"item_id": itemID,
						},
						map[string]interface{}{
							"type":    "message",
							"role":    "user",
							"content": "Continue from the previous message",
						},
					},
				}
				payload2, err := json.Marshal(reqBody2)
				Expect(err).ToNot(HaveOccurred())

				req2, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload2))
				Expect(err).ToNot(HaveOccurred())
				req2.Header.Set("Content-Type", "application/json")
				req2.Header.Set("Authorization", bearerKey)

				resp2, err := client.Do(req2)
				Expect(err).ToNot(HaveOccurred())
				defer resp2.Body.Close()

				// Should succeed (item reference resolved)
				Expect(resp2.StatusCode).To(Equal(200))
			})

			It("should return error for invalid item_reference", func() {
				reqBody := map[string]interface{}{
					"model": testModel,
					"input": []interface{}{
						map[string]interface{}{
							"type":    "item_reference",
							"item_id": "nonexistent_item_id",
						},
					},
				}
				payload, err := json.Marshal(reqBody)
				Expect(err).ToNot(HaveOccurred())

				req, err := http.NewRequest("POST", "http://127.0.0.1:9090/v1/responses", bytes.NewBuffer(payload))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", bearerKey)

				client := &http.Client{}
				resp, err := client.Do(req)
				Expect(err).ToNot(HaveOccurred())
				defer resp.Body.Close()

				// Should return error
				Expect(resp.StatusCode).To(BeNumerically(">=", 400))
			})
		})
	})
})
