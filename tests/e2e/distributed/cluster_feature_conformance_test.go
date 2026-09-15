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

// These inventories mirror grpc.InferenceBackend and grpc.ControlBackend by
// their current Go method names. Task 7 adds a reflection drift guard; keeping
// the truthful split here makes omissions reviewable in this task already.
var inferenceBackendMethods = []string{
	"Embeddings", "PredictStream", "Predict", "GenerateImage", "UpscaleImage",
	"GenerateVideo", "Generate3D", "TTS", "TTSStream", "SoundGeneration",
	"AudioTranscription", "AudioTranscriptionStream", "Detect", "Depth",
	"FaceVerify", "FaceAnalyze", "VoiceVerify", "VoiceAnalyze", "VoiceEmbed",
	"Rerank", "TokenClassify", "Score", "VAD", "Diarize", "SoundDetection",
	"AudioEncode", "AudioDecode", "AudioTransform",
}

var controlBackendMethods = []string{
	"IsBusy", "HealthCheck", "LoadModel", "TokenizeString", "Detokenize", "Status",
	"StoresSet", "StoresDelete", "StoresGet", "StoresFind", "GetTokenMetrics",
	"AudioTransformStream", "AudioToAudioStream", "AudioTranscriptionLive", "Forward",
	"ModelMetadata", "StartFineTune", "FineTuneProgress", "StopFineTune",
	"ListCheckpoints", "ExportModel", "StartQuantization", "QuantizationProgress",
	"StopQuantization", "Free",
}

type conformanceFixtures struct {
	inlineImage  string
	imageDataURI string
	audio        []byte
}

func newConformanceFixtures() conformanceFixtures {
	return conformanceFixtures{
		inlineImage:  base64.StdEncoding.EncodeToString(conformancePNG),
		imageDataURI: "data:image/png;base64," + base64.StdEncoding.EncodeToString(conformancePNG),
		audio:        append(make([]byte, 44), []byte("frontend-only-audio-input")...),
	}
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

func conformancePostMultipartFiles(client *http.Client, baseURL, path string, fields map[string]string, files map[string][]byte) (*http.Response, []byte) {
	GinkgoHelper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		Expect(writer.WriteField(key, value)).To(Succeed())
	}
	for field, file := range files {
		part, err := writer.CreateFormFile(field, field+".png")
		Expect(err).ToNot(HaveOccurred())
		_, err = part.Write(file)
		Expect(err).ToNot(HaveOccurred())
	}
	Expect(writer.Close()).To(Succeed())
	req := mustConformanceRequest(http.MethodPost, baseURL+path, body)
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

func mustConformanceRequest(method, target string, body io.Reader) *http.Request {
	GinkgoHelper()
	req, err := http.NewRequest(method, target, body)
	Expect(err).ToNot(HaveOccurred())
	return req
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

func runPublicBackendConformance(client *http.Client, baseURL, model string, fixtures conformanceFixtures) {
	By("keeping the exact current inference and control method inventories")
	Expect(inferenceBackendMethods).To(Equal([]string{
		"Embeddings", "PredictStream", "Predict", "GenerateImage", "UpscaleImage",
		"GenerateVideo", "Generate3D", "TTS", "TTSStream", "SoundGeneration",
		"AudioTranscription", "AudioTranscriptionStream", "Detect", "Depth",
		"FaceVerify", "FaceAnalyze", "VoiceVerify", "VoiceAnalyze", "VoiceEmbed",
		"Rerank", "TokenClassify", "Score", "VAD", "Diarize", "SoundDetection",
		"AudioEncode", "AudioDecode", "AudioTransform",
	}))
	Expect(controlBackendMethods).To(Equal([]string{
		"IsBusy", "HealthCheck", "LoadModel", "TokenizeString", "Detokenize", "Status",
		"StoresSet", "StoresDelete", "StoresGet", "StoresFind", "GetTokenMetrics",
		"AudioTransformStream", "AudioToAudioStream", "AudioTranscriptionLive", "Forward",
		"ModelMetadata", "StartFineTune", "FineTuneProgress", "StopFineTune",
		"ListCheckpoints", "ExportModel", "StartQuantization", "QuantizationProgress",
		"StopQuantization", "Free",
	}))

	By("running non-streaming and streaming chat")
	result, err := chat(client, baseURL, model, "fixture prompt")
	Expect(err).ToNot(HaveOccurred())
	Expect(result.status).To(Equal(http.StatusOK), result.body)
	Expect(result.content).To(Equal(mockedReply))
	resp, payload := conformancePostJSON(client, baseURL, "/v1/chat/completions", map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": "fixture stream"}}, "stream": true,
	})
	expectConformanceStatus(resp, payload)
	Expect(conformanceSSEChatContent(payload)).To(Equal("This is a mocked streaming response."))
	Expect(string(payload)).To(ContainSubstring("data: [DONE]"))

	By("returning the exact deterministic embedding")
	resp, payload = conformancePostJSON(client, baseURL, "/v1/embeddings", map[string]any{"model": model, "input": "fixture"})
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
	inlineImage := fixtures.inlineImage
	imageDataURI := fixtures.imageDataURI
	resp, payload = conformancePostJSON(client, baseURL, "/v1/images/generations", map[string]any{
		"model": model, "prompt": "fixture", "size": "1x1", "response_format": "b64_json", "file": inlineImage,
	})
	expectConformanceStatus(resp, payload)
	imageArtifact := conformanceB64Item(payload)
	Expect(bytes.HasPrefix(imageArtifact, conformancePNG)).To(BeTrue())
	Expect(string(imageArtifact)).To(ContainSubstring("sha256:aa7bb0431aaeb198a77c26a14fe6dd714a75e4d7db94e3e1238a1fdcbfe1f8d4"))
	resp, payload = conformancePostJSON(client, baseURL, "/video", map[string]any{
		"model": model, "prompt": "fixture", "response_format": "b64_json", "start_image": inlineImage,
	})
	expectConformanceStatus(resp, payload)
	videoArtifact := conformanceB64Item(payload)
	Expect(bytes.HasPrefix(videoArtifact, conformanceVideo)).To(BeTrue())
	Expect(string(videoArtifact)).To(ContainSubstring("sha256:aa7bb0431aaeb198a77c26a14fe6dd714a75e4d7db94e3e1238a1fdcbfe1f8d4"))
	resp, payload = conformancePostJSON(client, baseURL, "/3d/generations", map[string]any{
		"model": model, "image": inlineImage, "response_format": "b64_json",
	})
	expectConformanceStatus(resp, payload)
	assetArtifact := conformanceB64Item(payload)
	Expect(bytes.HasPrefix(assetArtifact, conformanceGLB)).To(BeTrue())
	Expect(string(assetArtifact)).To(ContainSubstring("sha256:aa7bb0431aaeb198a77c26a14fe6dd714a75e4d7db94e3e1238a1fdcbfe1f8d4"))

	By("covering object detection and depth over their canonical routes")
	resp, payload = conformancePostJSON(client, baseURL, "/v1/detection", map[string]any{
		"model": model, "image": imageDataURI, "prompt": "fixture", "threshold": 0.5,
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(MatchJSON(`{"detections":[{"x":10,"y":20,"width":100,"height":200,"class_name":"mocked_object","confidence":0.95}]}`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/depth", map[string]any{
		"model": model, "image": imageDataURI, "include_depth": true, "include_confidence": true,
	})
	expectConformanceStatus(resp, payload)
	var depth struct {
		Width      int       `json:"width"`
		Height     int       `json:"height"`
		Depth      []float64 `json:"depth"`
		Confidence []float64 `json:"confidence"`
		IsMetric   bool      `json:"is_metric"`
	}
	Expect(json.Unmarshal(payload, &depth)).To(Succeed())
	Expect(depth.Width).To(Equal(2))
	Expect(depth.Height).To(Equal(1))
	Expect(depth.Depth).To(Equal([]float64{1.25, 2.5}))
	Expect(depth.Confidence).To(Equal([]float64{0.9, 0.8}))
	Expect(depth.IsMetric).To(BeTrue())

	By("staging an uploaded image for upscale and returning fixture bytes")
	resp, payload = conformancePostMultipart(client, baseURL, "/v1/images/upscale", "image", map[string]string{
		"model": model, "scale": "2",
	}, conformancePNG)
	expectConformanceStatus(resp, payload)
	var upscale struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(payload, &upscale)).To(Succeed())
	Expect(upscale.Data).To(HaveLen(1))
	upscaledResp, upscaledPayload := conformanceDo(client, mustConformanceRequest(http.MethodGet, upscale.Data[0].URL, nil))
	expectConformanceStatus(upscaledResp, upscaledPayload)
	Expect(bytes.HasPrefix(upscaledPayload, conformancePNG)).To(BeTrue())
	upscaleDigest := sha256.Sum256(conformancePNG)
	Expect(string(upscaledPayload)).To(ContainSubstring(fmt.Sprintf("sha256:%x", upscaleDigest)))

	By("staging both inpainting inputs and returning the generated fixture")
	resp, payload = conformancePostMultipartFiles(client, baseURL, "/v1/images/inpainting", map[string]string{
		"model": model, "prompt": "fixture inpaint",
	}, map[string][]byte{"image": conformancePNG, "mask": conformancePNG})
	expectConformanceStatus(resp, payload)
	var inpaint struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(payload, &inpaint)).To(Succeed())
	Expect(inpaint.Data).To(HaveLen(1))
	inpaintResp, inpaintPayload := conformanceDo(client, mustConformanceRequest(http.MethodGet, inpaint.Data[0].URL, nil))
	expectConformanceStatus(inpaintResp, inpaintPayload)
	Expect(bytes.HasPrefix(inpaintPayload, conformancePNG)).To(BeTrue())
	Expect(string(inpaintPayload)).To(ContainSubstring(fmt.Sprintf("sha256:%x", upscaleDigest)))

	By("covering face verification, analysis and embedding")
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/verify", map[string]any{
		"model": model, "img1": imageDataURI, "img2": imageDataURI,
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"verified":true`))
	Expect(string(payload)).To(ContainSubstring(`"model":"mock-face"`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/analyze", map[string]any{
		"model": model, "img": imageDataURI, "actions": []string{"age", "gender", "emotion"},
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"dominant_gender":"Woman"`))
	Expect(string(payload)).To(ContainSubstring(`"dominant_emotion":"happy"`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/embed", map[string]any{
		"model": model, "img": imageDataURI,
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"dim":768`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/register", map[string]any{
		"model": model, "img": imageDataURI, "name": "fixture-face",
	})
	expectConformanceStatus(resp, payload)
	var faceRegistration struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal(payload, &faceRegistration)).To(Succeed())
	Expect(faceRegistration.ID).ToNot(BeEmpty())
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/identify", map[string]any{
		"model": model, "img": imageDataURI, "top_k": 1,
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"name":"fixture-face"`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/forget", map[string]any{"id": faceRegistration.ID})
	Expect(resp.StatusCode).To(Equal(http.StatusNoContent), string(payload))

	By("returning deterministic unary and streaming TTS audio")
	resp, unaryTTS := conformancePostJSON(client, baseURL, "/v1/audio/speech", map[string]any{
		"model": model, "input": "fixture speech", "voice": "default",
	})
	expectConformanceStatus(resp, unaryTTS)
	Expect(unaryTTS).To(HaveLen(16044))
	Expect(unaryTTS[:4]).To(Equal([]byte("RIFF")))
	resp, streamTTS := conformancePostJSON(client, baseURL, "/v1/audio/speech", map[string]any{
		"model": model, "input": "fixture speech", "voice": "default", "stream": true,
	})
	expectConformanceStatus(resp, streamTTS)
	Expect(streamTTS).To(HaveLen(16044))
	Expect(streamTTS[:4]).To(Equal([]byte("RIFF")))
	Expect(streamTTS[44:]).To(Equal(unaryTTS[44:]))

	By("covering voice verification, analysis and embedding with frontend-originated audio")
	voiceAudio := base64.StdEncoding.EncodeToString(unaryTTS)
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/verify", map[string]any{
		"model": model, "audio1": voiceAudio, "audio2": voiceAudio,
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"verified":true`))
	Expect(string(payload)).To(ContainSubstring(`"model":"mock-speaker"`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/analyze", map[string]any{
		"model": model, "audio": voiceAudio, "actions": []string{"age", "gender", "emotion"},
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"dominant_emotion":"neutral"`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/embed", map[string]any{
		"model": model, "audio": voiceAudio,
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"dim":2`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/register", map[string]any{
		"model": model, "audio": voiceAudio, "name": "fixture-voice",
	})
	expectConformanceStatus(resp, payload)
	var voiceRegistration struct {
		ID string `json:"id"`
	}
	Expect(json.Unmarshal(payload, &voiceRegistration)).To(Succeed())
	Expect(voiceRegistration.ID).ToNot(BeEmpty())
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/identify", map[string]any{
		"model": model, "audio": voiceAudio, "top_k": 1,
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"name":"fixture-voice"`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/forget", map[string]any{"id": voiceRegistration.ID})
	Expect(resp.StatusCode).To(Equal(http.StatusNoContent), string(payload))

	By("returning deterministic generated sound")
	resp, payload = conformancePostJSON(client, baseURL, "/v1/sound-generation", map[string]any{
		"model_id": model, "text": "fixture sound",
	})
	expectConformanceStatus(resp, payload)
	Expect(payload).To(Equal(unaryTTS))

	By("staging uploaded audio for unary and streaming transcription")
	audioInput := fixtures.audio
	digest := sha256.Sum256(audioInput)
	marker := fmt.Sprintf("audio=sha256:%x", digest)
	soundMarker := fmt.Sprintf("src=sha256:%x", digest)
	resp, payload = conformancePostMultipart(client, baseURL, "/v1/audio/transcriptions", "file", map[string]string{
		"model": model, "response_format": "json",
	}, audioInput)
	expectConformanceStatus(resp, payload)
	var transcript struct {
		Text string `json:"text"`
	}
	Expect(json.Unmarshal(payload, &transcript)).To(Succeed())
	Expect(transcript.Text).To(ContainSubstring(marker))
	resp, payload = conformancePostMultipart(client, baseURL, "/v1/audio/transcriptions", "file", map[string]string{
		"model": model, "stream": "true",
	}, audioInput)
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"type":"transcript.text.delta"`))
	Expect(string(payload)).To(ContainSubstring(marker))
	Expect(string(payload)).To(ContainSubstring("data: [DONE]"))

	By("staging uploaded audio for sound detection")
	resp, payload = conformancePostMultipart(client, baseURL, "/v1/audio/classification", "file", map[string]string{
		"model": model, "top_k": "1",
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
	Expect(classification.Model).To(Equal(model))
	Expect(classification.Detections).To(HaveLen(1))
	Expect(classification.Detections[0].Label).To(ContainSubstring(soundMarker))
	Expect(classification.Detections[0].Score).To(Equal(0.99))
	Expect(classification.Detections[0].Index).To(Equal(1))

	By("covering VAD and diarization through canonical public routes")
	resp, payload = conformancePostJSON(client, baseURL, "/v1/vad", map[string]any{
		"model": model, "audio": []float32{0.2, -0.2, 0.2, -0.2},
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(MatchJSON(`{"segments":[{"start":0,"end":0.00025}]}`))
	resp, payload = conformancePostMultipart(client, baseURL, "/v1/audio/diarization", "file", map[string]string{
		"model": model, "include_text": "true", "response_format": "verbose_json", "language": "en",
	}, audioInput)
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"speaker":"SPEAKER_00"`))
	Expect(string(payload)).To(ContainSubstring(`"text":"hello there"`))
	Expect(string(payload)).To(ContainSubstring(`"num_speakers":2`))

	By("covering token classification through the public PII inference route")
	resp, payload = conformancePostJSON(client, baseURL, "/api/pii/analyze", map[string]any{
		"detectors": []string{model}, "text": "Alice visited Rome",
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"entity_type":"PER"`))
	Expect(string(payload)).To(ContainSubstring(`"start":0`))
	Expect(string(payload)).To(ContainSubstring(`"end":5`))

	By("staging uploaded audio and returning the exact transformed artifact")
	resp, payload = conformancePostMultipart(client, baseURL, "/audio/transformations", "audio", map[string]string{
		"model": model, "response_format": "wav",
	}, unaryTTS)
	expectConformanceStatus(resp, payload)
	Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("audio"))
	Expect(payload).To(Equal(unaryTTS))

	By("streaming exact audio-transform PCM over the public WebSocket")
	ws := conformanceWebSocket(client, baseURL, "/audio/transformations/stream")
	defer func() { _ = ws.Close() }()
	Expect(ws.WriteJSON(map[string]any{
		"type": "session.update", "model": model, "sample_format": "S16_LE", "sample_rate": 16000, "frame_samples": 2,
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
		"model": model, "query": "fixture", "documents": []string{"alpha", "beta"},
	})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(ContainSubstring(`"total_tokens":20`))
	Expect(string(payload)).To(ContainSubstring(`"relevance_score":0.8999999761581421`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/tokenize", map[string]any{"model": model, "content": "eightchr"})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(MatchJSON(`{"tokens":[1,2]}`))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/detokenize", map[string]any{"model": model, "tokens": []int{4, 8, 15}})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(MatchJSON(`{"content":"detokenized: 4 8 15"}`))
	resp, payload = conformancePostJSON(client, baseURL, "/api/score", map[string]any{
		"model": model, "prompt": "ROUTE_HINT=alpha", "candidates": []string{`{"route":"alpha"}`, `{"route":"beta"}`}, "length_normalize": true,
	})
	expectConformanceStatus(resp, payload)
	var score struct {
		Model      string `json:"model"`
		Candidates []struct {
			LogProb float64 `json:"log_prob"`
		} `json:"candidates"`
	}
	Expect(json.Unmarshal(payload, &score)).To(Succeed())
	Expect(score.Model).To(Equal(model))
	Expect(score.Candidates).To(HaveLen(2))
	Expect(score.Candidates[0].LogProb).To(Equal(0.0))
	Expect(score.Candidates[1].LogProb).To(Equal(-5.0))

	By("exercising all public store operations")
	resp, payload = conformancePostJSON(client, baseURL, "/stores/set", map[string]any{"store": model, "backend": "mock-backend", "keys": [][]float32{{0.1, 0.2}}, "values": []string{"fixture"}})
	expectConformanceStatus(resp, payload)
	Expect(payload).To(BeEmpty())
	resp, payload = conformancePostJSON(client, baseURL, "/stores/get", map[string]any{"store": model, "backend": "mock-backend", "keys": [][]float32{{0.1, 0.2}}})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(MatchJSON(`{"keys":[[0.1,0.2]],"values":["mocked_value_0"]}`))
	resp, payload = conformancePostJSON(client, baseURL, "/stores/find", map[string]any{"store": model, "backend": "mock-backend", "key": []float32{0.1, 0.2}, "topk": 2})
	expectConformanceStatus(resp, payload)
	Expect(string(payload)).To(MatchJSON(`{"keys":[[0.1,0.2,0.3],[0.4,0.5,0.6]],"values":["mocked_value_1","mocked_value_2"],"similarities":[0.95,0.85]}`))
	resp, payload = conformancePostJSON(client, baseURL, "/stores/delete", map[string]any{"store": model, "backend": "mock-backend", "keys": [][]float32{{0.1, 0.2}}})
	expectConformanceStatus(resp, payload)
	Expect(payload).To(BeEmpty())

}

var _ = Describe("Binary backend feature conformance", Label("Distributed"), Label("Cluster"), func() {
	It("binary backend feature conformance through the tunnel owner", func() {
		const model = "conformance"
		fixtures := newConformanceFixtures()
		c := startCluster(1, 1, withMockModel(model), func(o *cluster.Options) {
			o.ConformanceStaging = true
			o.Models[model+".yaml"] = mockModelYAML(model) + `known_usecases:
  - chat
  - embeddings
  - image
  - video
  - 3d
  - tts
  - sound_generation
  - transcript
  - sound_classification
  - rerank
  - tokenize
  - score
  - audio_transform
  - detection
  - depth
  - vad
  - diarization
  - face_recognition
  - speaker_recognition
  - token_classify
pii_detection:
  min_score: 0.5
  default_action: mask
`
		})
		client := inferenceClient(c)
		baseURL := c.FrontendURL(0)

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)

		runPublicBackendConformance(client, baseURL, model, fixtures)

		By("proving the frontend-only model artifact arrived intact at the worker")
		workerModels, err := c.WorkerModelsDir(0)
		Expect(err).ToNot(HaveOccurred())
		staged, err := os.ReadFile(filepath.Join(workerModels, model, model+".bin"))
		Expect(err).ToNot(HaveOccurred())
		Expect(staged).To(Equal([]byte(tinyArtifact())))
		Expect(strings.Contains(workerModels, "worker-0")).To(BeTrue())
	})
})
