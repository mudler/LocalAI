package distributed_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	conformancePNG   = mustConformanceHex("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d4944415408d763f8cfc0f01f00050001ff89993d1d0000000049454e44ae426082")
	conformanceVideo = []byte("\x00\x00\x00\x18ftypisomMOCK-VIDEO")
	conformanceGLB   = mustConformanceHex("676c5446020000000c000000")
)

// backendCapabilityRoutes is the Task 5 inventory for the binary suite.
// Protocol-only entries deliberately have no invented HTTP test route. They
// remain visible here so the later full protocol inventory can account for
// them without weakening this public-API conformance test.
var backendCapabilityRoutes = []struct {
	capability string
	route      string
}{
	{"Predict", "POST /v1/chat/completions"},
	{"PredictStream", "POST /v1/chat/completions (stream=true)"},
	{"Embedding", "POST /v1/embeddings"},
	{"GenerateImage", "POST /v1/images/generations"},
	{"GenerateVideo", "POST /video"},
	{"Generate3D", "POST /3d/generations"},
	{"TTS", "POST /v1/audio/speech"},
	{"TTSStream", "POST /v1/audio/speech (stream=true)"},
	{"SoundGeneration", "POST /v1/sound-generation"},
	{"AudioTranscription", "POST /v1/audio/transcriptions"},
	{"AudioTranscriptionStream", "POST /v1/audio/transcriptions (stream=true)"},
	{"SoundDetection", "POST /v1/audio/classification"},
	{"Rerank", "POST /v1/rerank"},
	{"TokenizeString", "POST /v1/tokenize"},
	{"Detokenize", "POST /v1/detokenize"},
	{"Score", "POST /api/score"},
	{"StoresSet", "POST /stores/set"},
	{"StoresDelete", "POST /stores/delete"},
	{"StoresGet", "POST /stores/get"},
	{"StoresFind", "POST /stores/find"},
	{"AudioEncode", ""},   // realtime transport internal; no dedicated public route
	{"AudioDecode", ""},   // realtime transport internal; no dedicated public route
	{"ModelMetadata", ""}, // loader-internal probe; no public route
	{"GetMetrics", ""},    // backend monitor plumbing; no direct capability route
	{"Status", ""},        // worker lifecycle health; no inference route
	{"AudioTransform", "POST /audio/transformations"},
	{"AudioTransformStream", "GET /audio/transformations/stream (WebSocket)"},
}

func mustConformanceHex(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return decoded
}

func conformancePostJSON(client *http.Client, baseURL, path string, body any) (*http.Response, []byte) {
	GinkgoHelper()
	encoded, err := json.Marshal(body)
	Expect(err).ToNot(HaveOccurred())
	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(encoded))
	Expect(err).ToNot(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	return conformanceDo(client, req)
}

func conformancePostMultipart(client *http.Client, baseURL, path, fileField string, fields map[string]string, file []byte) (*http.Response, []byte) {
	GinkgoHelper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		Expect(writer.WriteField(key, value)).To(Succeed())
	}
	part, err := writer.CreateFormFile(fileField, "frontend-only.wav")
	Expect(err).ToNot(HaveOccurred())
	_, err = part.Write(file)
	Expect(err).ToNot(HaveOccurred())
	Expect(writer.Close()).To(Succeed())
	req, err := http.NewRequest(http.MethodPost, baseURL+path, body)
	Expect(err).ToNot(HaveOccurred())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return conformanceDo(client, req)
}

func conformanceDo(client *http.Client, req *http.Request) (*http.Response, []byte) {
	GinkgoHelper()
	resp, err := client.Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return resp, payload
}

func expectConformanceStatus(resp *http.Response, payload []byte) {
	GinkgoHelper()
	Expect(resp.StatusCode).To(Equal(http.StatusOK), string(payload))
}

func conformanceB64Item(payload []byte) []byte {
	GinkgoHelper()
	var response struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(payload, &response)).To(Succeed())
	Expect(response.Data).To(HaveLen(1))
	decoded, err := base64.StdEncoding.DecodeString(response.Data[0].B64JSON)
	Expect(err).ToNot(HaveOccurred())
	return decoded
}

func conformanceSSEChatContent(payload []byte) string {
	GinkgoHelper()
	var content strings.Builder
	for _, line := range strings.Split(string(payload), "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content *string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		Expect(json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk)).To(Succeed())
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != nil {
			content.WriteString(*chunk.Choices[0].Delta.Content)
		}
	}
	return content.String()
}

func conformanceWebSocket(client *http.Client, baseURL, path string) *websocket.Conn {
	GinkgoHelper()
	httpURL, err := url.Parse(baseURL)
	Expect(err).ToNot(HaveOccurred())
	header := http.Header{}
	if client.Jar != nil {
		cookies := client.Jar.Cookies(httpURL)
		values := make([]string, 0, len(cookies))
		for _, cookie := range cookies {
			values = append(values, cookie.String())
		}
		header.Set("Cookie", strings.Join(values, "; "))
	}
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + path
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	Expect(err).ToNot(HaveOccurred())
	return conn
}

var _ = Describe("Binary backend feature conformance", Label("Distributed"), Label("Cluster"), func() {
	It("binary backend feature conformance through the tunnel owner", func() {
		c := startCluster(1, 1, withMockModel("conformance"), func(o *cluster.Options) {
			o.Models["conformance.yaml"] = mockModelYAML("conformance") + "known_usecases:\n  - audio_transform\n"
		})
		client := inferenceClient(c)
		baseURL := c.FrontendURL(0)

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)

		By("keeping every public and protocol-only capability in the inventory")
		Expect(backendCapabilityRoutes).To(HaveLen(27))
		for _, entry := range backendCapabilityRoutes {
			Expect(entry.capability).ToNot(BeEmpty())
		}

		By("running non-streaming and streaming chat")
		result, err := chat(client, baseURL, "conformance", "fixture prompt")
		Expect(err).ToNot(HaveOccurred())
		Expect(result.status).To(Equal(http.StatusOK), result.body)
		Expect(result.content).To(Equal(mockedReply))
		resp, payload := conformancePostJSON(client, baseURL, "/v1/chat/completions", map[string]any{
			"model": "conformance", "messages": []map[string]string{{"role": "user", "content": "fixture stream"}}, "stream": true,
		})
		expectConformanceStatus(resp, payload)
		Expect(conformanceSSEChatContent(payload)).To(Equal("This is a mocked streaming response."))
		Expect(string(payload)).To(ContainSubstring("data: [DONE]"))

		By("returning the exact deterministic embedding")
		resp, payload = conformancePostJSON(client, baseURL, "/v1/embeddings", map[string]any{"model": "conformance", "input": "fixture"})
		expectConformanceStatus(resp, payload)
		var embedding struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		Expect(json.Unmarshal(payload, &embedding)).To(Succeed())
		Expect(embedding.Data).To(HaveLen(1))
		Expect(embedding.Data[0].Embedding).To(HaveLen(768))
		Expect(embedding.Data[0].Embedding[:5]).To(Equal([]float64{0, 0.01, 0.02, 0.03, 0.04}))

		By("round-tripping generated image, video and 3D fixture bytes")
		inlineImage := base64.StdEncoding.EncodeToString([]byte("frontend-only-image-input"))
		resp, payload = conformancePostJSON(client, baseURL, "/v1/images/generations", map[string]any{
			"model": "conformance", "prompt": "fixture", "size": "1x1", "response_format": "b64_json", "file": inlineImage,
		})
		expectConformanceStatus(resp, payload)
		Expect(conformanceB64Item(payload)).To(Equal(conformancePNG))
		resp, payload = conformancePostJSON(client, baseURL, "/video", map[string]any{
			"model": "conformance", "prompt": "fixture", "response_format": "b64_json", "start_image": inlineImage,
		})
		expectConformanceStatus(resp, payload)
		Expect(conformanceB64Item(payload)).To(Equal(conformanceVideo))
		resp, payload = conformancePostJSON(client, baseURL, "/3d/generations", map[string]any{
			"model": "conformance", "image": inlineImage, "response_format": "b64_json",
		})
		expectConformanceStatus(resp, payload)
		Expect(conformanceB64Item(payload)).To(Equal(conformanceGLB))

		By("returning deterministic unary and streaming TTS audio")
		resp, unaryTTS := conformancePostJSON(client, baseURL, "/v1/audio/speech", map[string]any{
			"model": "conformance", "input": "fixture speech", "voice": "default",
		})
		expectConformanceStatus(resp, unaryTTS)
		Expect(unaryTTS).To(HaveLen(16044))
		Expect(unaryTTS[:4]).To(Equal([]byte("RIFF")))
		resp, streamTTS := conformancePostJSON(client, baseURL, "/v1/audio/speech", map[string]any{
			"model": "conformance", "input": "fixture speech", "voice": "default", "stream": true,
		})
		expectConformanceStatus(resp, streamTTS)
		Expect(streamTTS).To(HaveLen(16044))
		Expect(streamTTS[:4]).To(Equal([]byte("RIFF")))
		Expect(streamTTS[44:]).To(Equal(unaryTTS[44:]))

		By("returning deterministic generated sound")
		resp, payload = conformancePostJSON(client, baseURL, "/v1/sound-generation", map[string]any{
			"model_id": "conformance", "text": "fixture sound",
		})
		expectConformanceStatus(resp, payload)
		Expect(payload).To(Equal(unaryTTS))

		By("staging uploaded audio for unary and streaming transcription")
		audioInput := append(make([]byte, 44), []byte("frontend-only-audio-input")...)
		digest := sha256.Sum256(audioInput)
		marker := fmt.Sprintf("audio=sha256:%x", digest)
		soundMarker := fmt.Sprintf("src=sha256:%x", digest)
		resp, payload = conformancePostMultipart(client, baseURL, "/v1/audio/transcriptions", "file", map[string]string{
			"model": "conformance", "response_format": "json",
		}, audioInput)
		expectConformanceStatus(resp, payload)
		var transcript struct {
			Text string `json:"text"`
		}
		Expect(json.Unmarshal(payload, &transcript)).To(Succeed())
		Expect(transcript.Text).To(ContainSubstring(marker))
		resp, payload = conformancePostMultipart(client, baseURL, "/v1/audio/transcriptions", "file", map[string]string{
			"model": "conformance", "stream": "true",
		}, audioInput)
		expectConformanceStatus(resp, payload)
		Expect(string(payload)).To(ContainSubstring(`"type":"transcript.text.delta"`))
		Expect(string(payload)).To(ContainSubstring(marker))
		Expect(string(payload)).To(ContainSubstring("data: [DONE]"))

		By("staging uploaded audio for sound detection")
		resp, payload = conformancePostMultipart(client, baseURL, "/v1/audio/classification", "file", map[string]string{
			"model": "conformance", "top_k": "1",
		}, audioInput)
		expectConformanceStatus(resp, payload)
		var classification struct {
			Model      string `json:"model"`
			Detections []struct {
				Label string  `json:"label"`
				Score float64 `json:"score"`
				Index int     `json:"index"`
			} `json:"detections"`
		}
		Expect(json.Unmarshal(payload, &classification)).To(Succeed())
		Expect(classification.Model).To(Equal("conformance"))
		Expect(classification.Detections).To(HaveLen(1))
		Expect(classification.Detections[0].Label).To(ContainSubstring(soundMarker))
		Expect(classification.Detections[0].Score).To(Equal(0.99))
		Expect(classification.Detections[0].Index).To(Equal(1))

		By("staging uploaded audio and returning the exact transformed artifact")
		resp, payload = conformancePostMultipart(client, baseURL, "/audio/transformations", "audio", map[string]string{
			"model": "conformance", "response_format": "wav",
		}, unaryTTS)
		expectConformanceStatus(resp, payload)
		Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("audio"))
		Expect(payload).To(Equal(unaryTTS))

		By("streaming exact audio-transform PCM over the public WebSocket")
		ws := conformanceWebSocket(client, baseURL, "/audio/transformations/stream")
		defer func() { _ = ws.Close() }()
		Expect(ws.WriteJSON(map[string]any{
			"type": "session.update", "model": "conformance", "sample_format": "S16_LE", "sample_rate": 16000, "frame_samples": 2,
		})).To(Succeed())
		stereoPCM := []byte{1, 0, 2, 0, 3, 0, 4, 0}
		Expect(ws.WriteMessage(websocket.BinaryMessage, stereoPCM)).To(Succeed())
		Expect(ws.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
		messageType, transformedPCM, err := ws.ReadMessage()
		Expect(err).ToNot(HaveOccurred())
		Expect(messageType).To(Equal(websocket.BinaryMessage))
		Expect(transformedPCM).To(Equal([]byte{1, 0, 3, 0}))

		By("returning exact rerank, tokenize, detokenize and score structures")
		resp, payload = conformancePostJSON(client, baseURL, "/v1/rerank", map[string]any{
			"model": "conformance", "query": "fixture", "documents": []string{"alpha", "beta"},
		})
		expectConformanceStatus(resp, payload)
		Expect(string(payload)).To(MatchJSON(`{"model":"conformance","usage":{"total_tokens":20,"prompt_tokens":20},"results":[{"index":0,"document":{"text":"alpha"},"relevance_score":0.8999999761581421},{"index":1,"document":{"text":"beta"},"relevance_score":0.7999999523162842}]}`))
		resp, payload = conformancePostJSON(client, baseURL, "/v1/tokenize", map[string]any{"model": "conformance", "content": "eightchr"})
		expectConformanceStatus(resp, payload)
		Expect(string(payload)).To(MatchJSON(`{"tokens":[1,2]}`))
		resp, payload = conformancePostJSON(client, baseURL, "/v1/detokenize", map[string]any{"model": "conformance", "tokens": []int{4, 8, 15}})
		expectConformanceStatus(resp, payload)
		Expect(string(payload)).To(MatchJSON(`{"content":"detokenized: 4 8 15"}`))
		resp, payload = conformancePostJSON(client, baseURL, "/api/score", map[string]any{
			"model": "conformance", "prompt": "ROUTE_HINT=alpha", "candidates": []string{`{"route":"alpha"}`, `{"route":"beta"}`}, "length_normalize": true,
		})
		expectConformanceStatus(resp, payload)
		var score struct {
			Model      string `json:"model"`
			Candidates []struct {
				LogProb float64 `json:"log_prob"`
			} `json:"candidates"`
		}
		Expect(json.Unmarshal(payload, &score)).To(Succeed())
		Expect(score.Model).To(Equal("conformance"))
		Expect(score.Candidates).To(HaveLen(2))
		Expect(score.Candidates[0].LogProb).To(Equal(0.0))
		Expect(score.Candidates[1].LogProb).To(Equal(-5.0))

		By("exercising all public store operations")
		resp, payload = conformancePostJSON(client, baseURL, "/stores/set", map[string]any{"store": "conformance", "backend": "mock-backend", "keys": [][]float32{{0.1, 0.2}}, "values": []string{"fixture"}})
		expectConformanceStatus(resp, payload)
		Expect(payload).To(BeEmpty())
		resp, payload = conformancePostJSON(client, baseURL, "/stores/get", map[string]any{"store": "conformance", "backend": "mock-backend", "keys": [][]float32{{0.1, 0.2}}})
		expectConformanceStatus(resp, payload)
		Expect(string(payload)).To(MatchJSON(`{"keys":[[0.1,0.2]],"values":["mocked_value_0"]}`))
		resp, payload = conformancePostJSON(client, baseURL, "/stores/find", map[string]any{"store": "conformance", "backend": "mock-backend", "key": []float32{0.1, 0.2}, "topk": 2})
		expectConformanceStatus(resp, payload)
		Expect(string(payload)).To(MatchJSON(`{"keys":[[0.1,0.2,0.3],[0.4,0.5,0.6]],"values":["mocked_value_1","mocked_value_2"],"similarities":[0.95,0.85]}`))
		resp, payload = conformancePostJSON(client, baseURL, "/stores/delete", map[string]any{"store": "conformance", "backend": "mock-backend", "keys": [][]float32{{0.1, 0.2}}})
		expectConformanceStatus(resp, payload)
		Expect(payload).To(BeEmpty())

		By("proving the frontend-only model artifact arrived intact at the worker")
		workerModels, err := c.WorkerModelsDir(0)
		Expect(err).ToNot(HaveOccurred())
		staged, err := os.ReadFile(filepath.Join(workerModels, "conformance", "conformance.bin"))
		Expect(err).ToNot(HaveOccurred())
		Expect(staged).To(Equal([]byte(tinyArtifact())))
		Expect(strings.Contains(workerModels, "worker-0")).To(BeTrue())
	})
})
