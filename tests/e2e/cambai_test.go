package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// cambaiURL returns the base URL for CAMB AI endpoints (no /v1 prefix).
func cambaiURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", apiPort)
}

var _ = Describe("CAMB AI API Compatibility Tests", Label("CambAI"), func() {
	var httpClient *http.Client

	BeforeEach(func() {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	})

	Describe("TTS Streaming API", func() {
		It("should stream audio from /apis/tts-stream", func() {
			body := `{
				"text": "Hello world from CAMB AI streaming",
				"voice_id": 1,
				"language": "en",
				"speech_model": "mock-model"
			}`
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/tts-stream", strings.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("audio/"))

			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 0), "TTS stream response body should be non-empty")
		})

		It("should return 400 for empty request", func() {
			body := `{}`
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/tts-stream", strings.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			// Should fail because text is empty
			Expect(resp.StatusCode).To(BeNumerically(">=", 400))
		})
	})

	Describe("TTS Async API", func() {
		It("should return a task response from /apis/tts", func() {
			body := `{
				"text": "Hello from async TTS",
				"voice_id": 1,
				"language": 1
			}`
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/tts", strings.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(200))

			var taskResp schema.CambAITaskResponse
			err = json.NewDecoder(resp.Body).Decode(&taskResp)
			Expect(err).ToNot(HaveOccurred())
			Expect(taskResp.TaskID).ToNot(BeEmpty())
			Expect(taskResp.Status).To(Equal("SUCCESS"))
		})

		It("should return audio when polling task status", func() {
			// First create a TTS task
			body := `{
				"text": "Task polling test",
				"voice_id": 1,
				"language": 1
			}`
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/tts", strings.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))

			var taskResp schema.CambAITaskResponse
			err = json.NewDecoder(resp.Body).Decode(&taskResp)
			Expect(err).ToNot(HaveOccurred())

			// Poll the task
			pollReq, err := http.NewRequest("GET", cambaiURL()+"/apis/tts/"+taskResp.TaskID, nil)
			Expect(err).ToNot(HaveOccurred())

			pollResp, err := httpClient.Do(pollReq)
			Expect(err).ToNot(HaveOccurred())
			defer pollResp.Body.Close()

			Expect(pollResp.StatusCode).To(Equal(200))
			Expect(pollResp.Header.Get("Content-Type")).To(HavePrefix("audio/"))

			data, err := io.ReadAll(pollResp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 0))
		})

		It("should return 404 for unknown task ID", func() {
			req, err := http.NewRequest("GET", cambaiURL()+"/apis/tts/nonexistent-task-id", nil)
			Expect(err).ToNot(HaveOccurred())

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(404))
		})
	})

	Describe("Translation API", func() {
		It("should translate text via /apis/translate", func() {
			body := `{
				"texts": ["Hello"],
				"source_language": 1,
				"target_language": 54
			}`
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/translate", strings.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(200))

			var result schema.CambAITaskStatusResponse
			err = json.NewDecoder(resp.Body).Decode(&result)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Status).To(Equal("SUCCESS"))
			Expect(result.Output).ToNot(BeNil())
		})

		It("should stream translation via /apis/translation/stream", func() {
			body := `{
				"text": "Hello world",
				"source_language": 1,
				"target_language": 54
			}`
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/translation/stream", strings.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(200))

			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 0), "Stream should return some text")
		})
	})

	Describe("Sound Generation API", func() {
		It("should generate sound via /apis/text-to-sound", func() {
			body := `{
				"prompt": "rain falling on a tin roof"
			}`
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/text-to-sound", strings.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("audio/"))

			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 0))
		})
	})

	Describe("Voice Management API", func() {
		It("should list voices via /apis/list-voices", func() {
			req, err := http.NewRequest("GET", cambaiURL()+"/apis/list-voices", nil)
			Expect(err).ToNot(HaveOccurred())

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(200))

			var result schema.CambAIListVoicesResponse
			err = json.NewDecoder(resp.Body).Decode(&result)
			Expect(err).ToNot(HaveOccurred())
			// voices list may be empty if no TTS models are flagged, but the endpoint should work
			Expect(result.Voices).ToNot(BeNil())
		})
	})

	Describe("Audio Separation API (stub)", func() {
		It("should return 501 Not Implemented", func() {
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/audio-separation", strings.NewReader(`{}`))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(501))

			var result schema.CambAIErrorResponse
			err = json.NewDecoder(resp.Body).Decode(&result)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Detail).To(ContainSubstring("not currently supported"))
		})
	})

	Describe("Transcription API", func() {
		It("should reject request without audio file", func() {
			req, err := http.NewRequest("POST", cambaiURL()+"/apis/transcribe", strings.NewReader(`{}`))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			// Should fail because no file was uploaded
			Expect(resp.StatusCode).To(BeNumerically(">=", 400))
		})
	})

	Describe("Language ID Mapping", func() {
		It("should map known language IDs correctly", func() {
			Expect(schema.CambAILanguageCodeFromID(1)).To(Equal("en"))
			Expect(schema.CambAILanguageCodeFromID(54)).To(Equal("es"))
			Expect(schema.CambAILanguageCodeFromID(76)).To(Equal("fr"))
			Expect(schema.CambAILanguageCodeFromID(70)).To(Equal("de"))
			Expect(schema.CambAILanguageCodeFromID(12)).To(Equal("ja"))
			Expect(schema.CambAILanguageCodeFromID(13)).To(Equal("zh"))
		})

		It("should return fallback for unknown language IDs", func() {
			result := schema.CambAILanguageCodeFromID(9999)
			Expect(result).To(Equal("lang-9999"))
		})
	})
})
