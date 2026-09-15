package distributed_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/core/schema"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var (
	conformancePNG   = mustConformanceHex("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d4944415408d763f8cfc0f01f00050001ff89993d1d0000000049454e44ae426082")
	conformanceVideo = []byte("\x00\x00\x00\x18ftypisomMOCK-VIDEO")
	conformanceGLB   = mustConformanceHex("676c5446020000000c000000")
)

type conformanceFixtures struct {
	inlineImage  string
	imageDataURI string
	audio        []byte
}

type stagingTopologyCoverage struct {
	method            string
	ownerPublicPath   string
	relayProtocolPath string
}

// A raw backend client cannot execute inside a compiled frontend's in-memory
// TunnelRegistry. Owner-side coverage therefore uses the real frontend route
// that invokes the override in that process; only the gaps use an authenticated
// external peer, which necessarily exercises the non-owner relay path.
var fileStagingTopologyCoverage = []stagingTopologyCoverage{
	{"LoadModel", "/v1/chat/completions (cold load)", "Backend/LoadModel"},
	{"Predict", "/v1/chat/completions", "Backend/Predict"},
	{"PredictStream", "/v1/chat/completions (stream)", "Backend/PredictStream"},
	{"GenerateImage", "/v1/images/generations", "Backend/GenerateImage"},
	{"UpscaleImage", "/v1/images/upscale", "Backend/UpscaleImage"},
	{"GenerateVideo", "/video", "Backend/GenerateVideo"},
	{"Generate3D", "/3d/generations", "Backend/Generate3D"},
	{"TTS", "/v1/audio/speech", "Backend/TTS"},
	{"TTSStream", "/v1/audio/speech (stream)", "Backend/TTSStream"},
	{"SoundGeneration", "/v1/sound-generation", "Backend/SoundGeneration"},
	{"SoundDetection", "/v1/audio/classification", "Backend/SoundDetection"},
	{"Detect", "/v1/detection", "Backend/Detect"},
	{"Depth", "/v1/depth", "Backend/Depth"},
	{"Diarize", "/v1/audio/diarization", "Backend/Diarize"},
	{"VoiceVerify", "/v1/voice/verify", "Backend/VoiceVerify"},
	{"VoiceAnalyze", "/v1/voice/analyze", "Backend/VoiceAnalyze"},
	{"VoiceEmbed", "/v1/voice/embed", "Backend/VoiceEmbed"},
	{"AudioTransform", "/audio/transformations", "Backend/AudioTransform"},
	{"AudioTranscription", "/v1/audio/transcriptions", "Backend/AudioTranscription"},
	{"AudioTranscriptionStream", "/v1/audio/transcriptions (stream)", "Backend/AudioTranscriptionStream"},
	{"ExportModel", "/api/finetune/jobs/:id/export", "Backend/ExportModel"},
	{"StartQuantization", "/api/quantization/jobs", "Backend/StartQuantization"},
	{"QuantizationProgress", "/api/quantization/jobs/:id/progress", "Backend/QuantizationProgress"},
	{"StopQuantization", "/api/quantization/jobs/:id/stop", "Backend/StopQuantization"},
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
	return conformancePostMultipartNamed(client, baseURL, path, fileField, "frontend-only.wav", fields, file)
}

func conformancePostMultipartNamed(client *http.Client, baseURL, path, fileField, fileName string, fields map[string]string, file []byte) (*http.Response, []byte) {
	GinkgoHelper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		Expect(writer.WriteField(key, value)).To(Succeed())
	}
	part, err := writer.CreateFormFile(fileField, fileName)
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
	Expect(imageArtifact).To(Equal(conformanceArtifact(conformancePNG, "src="+conformanceDigest(conformancePNG))))
	resp, payload = conformancePostJSON(client, baseURL, "/video", map[string]any{
		"model": model, "prompt": "fixture", "response_format": "b64_json", "start_image": inlineImage,
	})
	expectConformanceStatus(resp, payload)
	videoArtifact := conformanceB64Item(payload)
	Expect(videoArtifact).To(Equal(conformanceArtifact(conformanceVideo, "start_image="+conformanceDigest(conformancePNG))))
	resp, payload = conformancePostJSON(client, baseURL, "/3d/generations", map[string]any{
		"model": model, "image": inlineImage, "response_format": "b64_json",
	})
	expectConformanceStatus(resp, payload)
	assetArtifact := conformanceB64Item(payload)
	Expect(assetArtifact).To(Equal(conformanceArtifact(conformanceGLB, "src="+conformanceDigest(conformancePNG))))

	By("staging a frontend-side GLB through the canonical remesh route")
	resp, payload = conformancePostMultipartNamed(client, baseURL, "/3d/remesh", "mesh", "frontend-only.glb", map[string]string{
		"model": model, "detail": "0.5",
	}, conformanceGLB)
	expectConformanceStatus(resp, payload)
	remeshDigest := sha256.Sum256(conformanceGLB)
	Expect(payload).To(Equal(append(append([]byte(nil), conformanceGLB...), []byte(fmt.Sprintf("\nMOCK-INPUTS:src=sha256:%x\n", remeshDigest))...)))

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
	upscaleDigest := sha256.Sum256(conformancePNG)
	Expect(upscaledPayload).To(Equal(conformanceArtifact(conformancePNG, fmt.Sprintf("src=sha256:%x", upscaleDigest))))

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
	inpaintSource, err := json.Marshal(map[string]string{
		"image": base64.StdEncoding.EncodeToString(conformancePNG), "mask_image": base64.StdEncoding.EncodeToString(conformancePNG),
	})
	Expect(err).ToNot(HaveOccurred())
	inpaintSource = append(inpaintSource, '\n')
	Expect(inpaintPayload).To(Equal(conformanceArtifact(conformancePNG,
		"src="+conformanceDigest(inpaintSource), fmt.Sprintf("ref_image[0]=sha256:%x", upscaleDigest), fmt.Sprintf("ref_image[1]=sha256:%x", upscaleDigest))))

	By("covering face verification, analysis and embedding")
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/verify", map[string]any{
		"model": model, "img1": imageDataURI, "img2": imageDataURI,
	})
	expectConformanceStatus(resp, payload)
	var faceVerify schema.FaceVerifyResponse
	Expect(json.Unmarshal(payload, &faceVerify)).To(Succeed())
	Expect(faceVerify).To(Equal(schema.FaceVerifyResponse{
		Verified: true, Distance: 0.05, Threshold: 0.25, Confidence: 95, Model: "mock-face",
		Img1Area: schema.FacialArea{X: 1, Y: 2, W: 3, H: 4},
		Img2Area: schema.FacialArea{X: 5, Y: 6, W: 7, H: 8},
	}))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/analyze", map[string]any{
		"model": model, "img": imageDataURI, "actions": []string{"age", "gender", "emotion"},
	})
	expectConformanceStatus(resp, payload)
	var faceAnalyze schema.FaceAnalyzeResponse
	Expect(json.Unmarshal(payload, &faceAnalyze)).To(Succeed())
	Expect(faceAnalyze).To(Equal(schema.FaceAnalyzeResponse{Faces: []schema.FaceAnalysis{{
		Region: schema.FacialArea{X: 1, Y: 2, W: 3, H: 4}, FaceConfidence: 0.98,
		Age: 34, DominantGender: "Woman", Gender: map[string]float32{"Woman": 0.9},
		DominantEmotion: "happy", Emotion: map[string]float32{"happy": 0.8},
	}}}))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/embed", map[string]any{
		"model": model, "img": imageDataURI,
	})
	expectConformanceStatus(resp, payload)
	var faceEmbed schema.FaceEmbedResponse
	Expect(json.Unmarshal(payload, &faceEmbed)).To(Succeed())
	Expect(faceEmbed.Model).To(Equal(model))
	Expect(faceEmbed.Dim).To(Equal(768))
	Expect(faceEmbed.Embedding).To(HaveLen(768))
	Expect(faceEmbed.Embedding[:5]).To(Equal([]float32{0, 0.01, 0.02, 0.03, 0.04}))
	Expect(faceEmbed.Embedding[767]).To(Equal(float32(0.67)))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/register", map[string]any{
		"model": model, "img": imageDataURI, "name": "fixture-face",
	})
	expectConformanceStatus(resp, payload)
	var faceRegistration schema.FaceRegisterResponse
	Expect(json.Unmarshal(payload, &faceRegistration)).To(Succeed())
	Expect(faceRegistration.ID).ToNot(BeEmpty())
	Expect(faceRegistration.Name).To(Equal("fixture-face"))
	Expect(faceRegistration.RegisteredAt).ToNot(BeZero())
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/identify", map[string]any{
		"model": model, "img": imageDataURI, "top_k": 1,
	})
	expectConformanceStatus(resp, payload)
	var faceIdentification schema.FaceIdentifyResponse
	Expect(json.Unmarshal(payload, &faceIdentification)).To(Succeed())
	Expect(faceIdentification.Matches).To(HaveLen(1))
	Expect(faceIdentification.Matches[0]).To(Equal(schema.FaceIdentifyMatch{
		ID: faceRegistration.ID, Name: "fixture-face", Distance: 0, Confidence: 100, Match: true,
	}))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/face/forget", map[string]any{"id": faceRegistration.ID})
	Expect(resp.StatusCode).To(Equal(http.StatusNoContent), string(payload))
	Expect(payload).To(BeEmpty())

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
	var voiceVerify schema.VoiceVerifyResponse
	Expect(json.Unmarshal(payload, &voiceVerify)).To(Succeed())
	Expect(voiceVerify.Verified).To(BeTrue())
	Expect(voiceVerify.Distance).To(BeNumerically("~", 0.0000193, 0.000001))
	Expect(voiceVerify.Threshold).To(Equal(float32(0.25)))
	Expect(voiceVerify.Confidence).To(Equal(float32(0)))
	Expect(voiceVerify.Model).To(Equal("mock-speaker"))
	Expect(voiceVerify.ProcessingTimeMs).To(Equal(float32(0)))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/analyze", map[string]any{
		"model": model, "audio": voiceAudio, "actions": []string{"age", "gender", "emotion"},
	})
	expectConformanceStatus(resp, payload)
	var voiceAnalyze schema.VoiceAnalyzeResponse
	Expect(json.Unmarshal(payload, &voiceAnalyze)).To(Succeed())
	Expect(voiceAnalyze).To(Equal(schema.VoiceAnalyzeResponse{Segments: []schema.VoiceAnalysis{{
		Start: 0, End: 1, Age: 42, DominantGender: "female", Gender: map[string]float32{"female": 0.95},
		DominantEmotion: "neutral", Emotion: map[string]float32{"neutral": 0.9},
	}}}))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/embed", map[string]any{
		"model": model, "audio": voiceAudio,
	})
	expectConformanceStatus(resp, payload)
	var voiceEmbed schema.VoiceEmbedResponse
	Expect(json.Unmarshal(payload, &voiceEmbed)).To(Succeed())
	Expect(voiceEmbed).To(Equal(schema.VoiceEmbedResponse{
		Embedding: []float32{0.7071, 0.7071}, Dim: 2, Model: "mock-speaker",
	}))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/register", map[string]any{
		"model": model, "audio": voiceAudio, "name": "fixture-voice",
	})
	expectConformanceStatus(resp, payload)
	var voiceRegistration schema.VoiceRegisterResponse
	Expect(json.Unmarshal(payload, &voiceRegistration)).To(Succeed())
	Expect(voiceRegistration.ID).ToNot(BeEmpty())
	Expect(voiceRegistration.Name).To(Equal("fixture-voice"))
	Expect(voiceRegistration.RegisteredAt).ToNot(BeZero())
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/identify", map[string]any{
		"model": model, "audio": voiceAudio, "top_k": 1,
	})
	expectConformanceStatus(resp, payload)
	var voiceIdentification schema.VoiceIdentifyResponse
	Expect(json.Unmarshal(payload, &voiceIdentification)).To(Succeed())
	Expect(voiceIdentification.Matches).To(HaveLen(1))
	Expect(voiceIdentification.Matches[0]).To(Equal(schema.VoiceIdentifyMatch{
		ID:         voiceRegistration.ID,
		Name:       "fixture-voice",
		Distance:   float32(1.9252300262451172e-05),
		Confidence: float32(99.99230194091797),
		Match:      true,
	}))
	resp, payload = conformancePostJSON(client, baseURL, "/v1/voice/forget", map[string]any{"id": voiceRegistration.ID})
	Expect(resp.StatusCode).To(Equal(http.StatusNoContent), string(payload))
	Expect(payload).To(BeEmpty())

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
	var diarization schema.DiarizationResult
	Expect(json.Unmarshal(payload, &diarization)).To(Succeed())
	Expect(diarization).To(Equal(schema.DiarizationResult{
		Task: "diarize", Duration: 3.5, Language: "en", NumSpeakers: 2,
		Segments: []schema.DiarizationSegment{
			{Id: 0, Speaker: "SPEAKER_00", Label: "5", Start: 0, End: 1, Text: "hello there"},
			{Id: 1, Speaker: "SPEAKER_01", Label: "2", Start: 1, End: 2, Text: "general kenobi"},
			{Id: 2, Speaker: "SPEAKER_00", Label: "5", Start: 2, End: 3.5, Text: "you are a bold one"},
		},
		Speakers: []schema.DiarizationSpeaker{
			{Id: "SPEAKER_00", Label: "5", TotalSpeechDuration: 2.5, SegmentCount: 2},
			{Id: "SPEAKER_01", Label: "2", TotalSpeechDuration: 1, SegmentCount: 1},
		},
	}))

	By("covering token classification through the public PII inference route")
	resp, payload = conformancePostJSON(client, baseURL, "/api/pii/analyze", map[string]any{
		"detectors": []string{model}, "text": "Alice visited Rome",
	})
	expectConformanceStatus(resp, payload)
	var piiAnalysis schema.PIIAnalyzeResponse
	Expect(json.Unmarshal(payload, &piiAnalysis)).To(Succeed())
	Expect(piiAnalysis.Entities).To(Equal([]schema.PIIEntity{{
		EntityType: "PER", Source: "ner", Start: 0, End: 5, Score: 0.99, Action: "mask",
	}}))
	Expect(piiAnalysis.Blocked).To(BeFalse())
	Expect(piiAnalysis.CorrelationID).ToNot(BeEmpty())

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
	var rerank schema.JINARerankResponse
	Expect(json.Unmarshal(payload, &rerank)).To(Succeed())
	Expect(rerank).To(Equal(schema.JINARerankResponse{
		Model: model,
		Usage: schema.JINAUsageInfo{TotalTokens: 20, PromptTokens: 20},
		Results: []schema.JINADocumentResult{
			{Index: 0, Document: schema.JINAText{Text: "alpha"}, RelevanceScore: float64(float32(0.9))},
			{Index: 1, Document: schema.JINAText{Text: "beta"}, RelevanceScore: 0.7999999523162842},
		},
	}))
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

func conformanceDigest(data []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

func conformanceArtifact(base []byte, markers ...string) []byte {
	result := append([]byte(nil), base...)
	if len(markers) != 0 {
		result = append(result, []byte("\nMOCK-INPUTS:"+strings.Join(markers, "; ")+"\n")...)
	}
	return result
}

func writeConformanceFixture(dir, name string, data []byte) string {
	GinkgoHelper()
	path := filepath.Join(dir, name)
	Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(Succeed())
	Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
	return path
}

// installConformanceDiskCapacity keeps the production disk admission check
// enabled while making its input independent of the CI host's current free
// space. The trigger also clamps later heartbeats, so a run cannot cross the
// threshold half way through as other jobs consume the shared filesystem.
func installConformanceDiskCapacity(db *gorm.DB, available uint64) {
	GinkgoHelper()
	Expect(db.Exec(`CREATE TABLE e2e_disk_capacity (
singleton boolean PRIMARY KEY DEFAULT true,
available_disk bigint NOT NULL
)`).Error).ToNot(HaveOccurred())
	Expect(db.Exec(`INSERT INTO e2e_disk_capacity (singleton, available_disk) VALUES (true, ?)`, available).Error).ToNot(HaveOccurred())
	Expect(db.Exec(`CREATE FUNCTION e2e_clamp_node_disk() RETURNS trigger AS $$
BEGIN
  SELECT available_disk INTO NEW.available_disk FROM e2e_disk_capacity WHERE singleton = true;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql`).Error).ToNot(HaveOccurred())
	Expect(db.Exec(`CREATE TRIGGER e2e_clamp_node_disk
BEFORE INSERT OR UPDATE OF available_disk ON backend_nodes
FOR EACH ROW EXECUTE FUNCTION e2e_clamp_node_disk()`).Error).ToNot(HaveOccurred())
	setConformanceDiskCapacity(db, available)
}

func setConformanceDiskCapacity(db *gorm.DB, available uint64) {
	GinkgoHelper()
	Expect(db.Exec(`UPDATE e2e_disk_capacity SET available_disk = ? WHERE singleton = true`, available).Error).ToNot(HaveOccurred())
	Expect(db.Model(&nodes.BackendNode{}).Where("1 = 1").Update("available_disk", available).Error).ToNot(HaveOccurred())
	var capacities []uint64
	Expect(db.Model(&nodes.BackendNode{}).Order("name").Pluck("available_disk", &capacities).Error).ToNot(HaveOccurred())
	Expect(capacities).ToNot(BeEmpty())
	for _, got := range capacities {
		Expect(got).To(Equal(available))
	}
}

// runFileStagingConformance uses the same peer pool, worker dialer, gRPC
// client factory and HTTP file stager as a production frontend. The test
// process joins as a replica and therefore reaches the worker through the
// compiled owner's peer endpoint; no worker address or test-only route is
// used. Public calls above cover both the owner's direct tunnel and the other
// compiled frontend's relay. This helper fills the protocol-only gaps.
func runFileStagingConformance(c *cluster.Cluster, db *gorm.DB, owners *tunnelOwners, workerID, model string, ownerFrontend, relayFrontend int) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	DeferCleanup(cancel)

	var node nodes.BackendNode
	Expect(db.WithContext(ctx).First(&node, "id = ?", workerID).Error).ToNot(HaveOccurred())
	var loaded nodes.NodeModel
	Expect(db.WithContext(ctx).
		Where("node_id = ? AND model_name = ? AND state = ?", workerID, model, "loaded").
		First(&loaded).Error).ToNot(HaveOccurred())
	Expect(loaded.WorkerLocalAddress).ToNot(BeEmpty())

	peerID := "e2e-peer-" + requestIDForTest()
	peerCredential := joinAsPeer(owners.roster, peerID)
	peers := clustersvc.NewPeerPool(peerID, c.RegistrationToken(), peerCredential, owners.registry)
	DeferCleanup(peers.Close)
	tunnels := clustersvc.NewTunnelRegistry(owners.registry, peerID)
	dialer := clustersvc.NewWorkerDialer(tunnels, peers)
	factory, err := nodes.NewTunnelClientFactory(c.RegistrationToken(), dialer.GRPCDialerFor)
	Expect(err).ToNot(HaveOccurred())
	raw, err := factory.NewClientForNode(workerID, loaded.WorkerLocalAddress, false)
	Expect(err).ToNot(HaveOccurred())
	stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
		if nodeID != workerID {
			return "", fmt.Errorf("unexpected node %q", nodeID)
		}
		return nodes.WorkerHTTPHost(node.ID, node.HTTPAddress), nil
	}, c.RegistrationToken(), func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
		return dialer.DialerFor(nodeID, clustersvc.StreamTagHTTP)
	})
	backend := nodes.NewFileStagingClient(raw, stager, workerID)

	fixtureDir := GinkgoT().TempDir()
	image := []byte("frontend-only-image")
	video := []byte("frontend-only-video")
	audio := append(make([]byte, 44), []byte("frontend-only-audio")...)
	voice := append(make([]byte, 44), []byte("frontend-only-voice")...)
	refA := []byte("frontend-only-reference-a")
	refB := []byte("frontend-only-reference-b")
	imagePath := writeConformanceFixture(fixtureDir, "inputs/image.png", image)
	videoPath := writeConformanceFixture(fixtureDir, "inputs/video.mp4", video)
	audioPath := writeConformanceFixture(fixtureDir, "inputs/audio.wav", audio)
	voicePath := writeConformanceFixture(fixtureDir, "inputs/voice.wav", voice)
	refAPath := writeConformanceFixture(fixtureDir, "inputs/ref-a.wav", refA)
	refBPath := writeConformanceFixture(fixtureDir, "inputs/ref-b.wav", refB)
	ttsModelPath := writeConformanceFixture(fixtureDir, model+".onnx", []byte(tinyArtifact()))

	By("loading the staged model, its companion files, and extended protocol fields")
	workerModels, err := c.WorkerModelsDir(0)
	Expect(err).ToNot(HaveOccurred())
	remoteModelDir := filepath.Join(workerModels, model)
	loadOptions := &pb.ModelOptions{
		Model: model, ModelPath: remoteModelDir,
		ModelFile:          filepath.Join(remoteModelDir, model+".onnx"),
		DraftModel:         filepath.Join(remoteModelDir, model+"-draft.gguf"),
		MMProj:             filepath.Join(remoteModelDir, model+"-mmproj.gguf"),
		OriginalConfigFile: filepath.Join(remoteModelDir, model+"-original.yaml"),
		EngineArgs:         `{"extended_protocol":true}`,
		EnvVars:            map[string]string{"FIXTURE_ENV": "preserved"},
	}
	loadResult, err := backend.LoadModel(ctx, loadOptions)
	Expect(err).ToNot(HaveOccurred())
	Expect(loadResult.Success).To(BeTrue(), loadResult.Message)
	for _, marker := range []string{
		"model_file=" + conformanceDigest([]byte(tinyArtifact())),
		"model_companion=" + conformanceDigest([]byte(`{"companion":true}`)),
		"draft_model=" + conformanceDigest([]byte("frontend-only-draft")),
		"mmproj=" + conformanceDigest([]byte("frontend-only-mmproj")),
		"original_config_file=" + conformanceDigest([]byte("fixture: original-config\n")),
	} {
		Expect(loadResult.Message).To(ContainSubstring(marker))
	}
	loadEcho, err := backend.Predict(ctx, &pb.PredictOptions{Prompt: "ECHO_LOAD_PARAMS"})
	Expect(err).ToNot(HaveOccurred())
	var echoedLoad map[string]string
	Expect(json.Unmarshal(loadEcho.Message, &echoedLoad)).To(Succeed())
	Expect(echoedLoad).To(Equal(map[string]string{
		"model": model, "model_file": loadOptions.ModelFile, "draft_model": loadOptions.DraftModel,
		"mmproj": loadOptions.MMProj, "engine_args": loadOptions.EngineArgs,
		"original_config_file": loadOptions.OriginalConfigFile, "fixture_env": "preserved",
	}))

	By("staging every Predict and PredictStream multimodal path through the peer transport")
	predict, err := backend.Predict(ctx, &pb.PredictOptions{
		Prompt: "ECHO_FIXTURE_INPUTS", Images: []string{imagePath}, Videos: []string{videoPath}, Audios: []string{audioPath},
	})
	Expect(err).ToNot(HaveOccurred())
	for _, marker := range []string{"image[0]=" + conformanceDigest(image), "video[0]=" + conformanceDigest(video), "audio[0]=" + conformanceDigest(audio)} {
		Expect(string(predict.Message)).To(ContainSubstring(marker))
	}
	var streamed strings.Builder
	Expect(backend.PredictStream(ctx, &pb.PredictOptions{
		Prompt: "ECHO_FIXTURE_INPUTS", Images: []string{imagePath}, Videos: []string{videoPath}, Audios: []string{audioPath},
	}, func(reply *pb.Reply) { streamed.Write(reply.Message) })).To(Succeed())
	for _, digest := range []string{conformanceDigest(image), conformanceDigest(video), conformanceDigest(audio)} {
		Expect(streamed.String()).To(ContainSubstring(digest))
	}

	By("staging every path-bearing analysis and conversion request through the peer transport")
	upscaleOut := filepath.Join(fixtureDir, "outputs/upscaled.png")
	upscale, err := backend.UpscaleImage(ctx, &pb.UpscaleImageRequest{Src: imagePath, Dst: upscaleOut, Scale: 2})
	Expect(err).ToNot(HaveOccurred())
	Expect(upscale.Success).To(BeTrue(), upscale.Message)
	Expect(os.ReadFile(upscaleOut)).To(Equal(conformanceArtifact(conformancePNG, "src="+conformanceDigest(image))))
	detect, err := backend.Detect(ctx, &pb.DetectOptions{Src: imagePath})
	Expect(err).ToNot(HaveOccurred())
	Expect(detect.Detections).To(HaveLen(1))
	depthDir := filepath.Join(fixtureDir, "outputs/depth")
	depth, err := backend.Depth(ctx, &pb.DepthRequest{Src: imagePath, Dst: depthDir, Exports: []string{"glb"}})
	Expect(err).ToNot(HaveOccurred())
	Expect(depth.ExportPaths).To(Equal([]string{filepath.Join(depthDir, "nested", "depth.txt")}))
	Expect(os.ReadFile(depth.ExportPaths[0])).To(Equal([]byte("src=" + conformanceDigest(image))))
	diarized, err := backend.Diarize(ctx, &pb.DiarizeRequest{Dst: audioPath, Language: "it", IncludeText: true})
	Expect(err).ToNot(HaveOccurred())
	Expect(diarized.Language).To(Equal("it"))
	verified, err := backend.VoiceVerify(ctx, &pb.VoiceVerifyRequest{Audio1: audioPath, Audio2: audioPath})
	Expect(err).ToNot(HaveOccurred())
	Expect(verified.Verified).To(BeTrue())
	analyzed, err := backend.VoiceAnalyze(ctx, &pb.VoiceAnalyzeRequest{Audio: audioPath})
	Expect(err).ToNot(HaveOccurred())
	Expect(analyzed.Segments).To(HaveLen(1))
	embedded, err := backend.VoiceEmbed(ctx, &pb.VoiceEmbedRequest{Audio: audioPath})
	Expect(err).ToNot(HaveOccurred())
	Expect(embedded.Model).To(Equal("mock-speaker"))
	transformOut := filepath.Join(fixtureDir, "outputs/transformed.wav")
	transformed, err := backend.AudioTransform(ctx, &pb.AudioTransformRequest{AudioPath: audioPath, ReferencePath: voicePath, Dst: transformOut})
	Expect(err).ToNot(HaveOccurred())
	Expect(transformed.Dst).To(Equal(transformOut))
	Expect(transformed.ReferenceProvided).To(BeTrue())
	Expect(os.ReadFile(transformOut)).To(Equal(conformanceArtifact(audio, "reference="+conformanceDigest(voice))))

	By("preserving inline data URIs across every newly staged peer method")
	inline := "data:application/octet-stream;base64,AAAA"
	_, err = backend.UpscaleImage(ctx, &pb.UpscaleImageRequest{Src: inline})
	Expect(err).ToNot(HaveOccurred())
	_, err = backend.Detect(ctx, &pb.DetectOptions{Src: inline})
	Expect(err).ToNot(HaveOccurred())
	_, err = backend.Depth(ctx, &pb.DepthRequest{Src: inline})
	Expect(err).ToNot(HaveOccurred())
	_, err = backend.Diarize(ctx, &pb.DiarizeRequest{Dst: inline})
	Expect(err).ToNot(HaveOccurred())
	_, err = backend.VoiceVerify(ctx, &pb.VoiceVerifyRequest{Audio1: inline, Audio2: inline})
	Expect(err).ToNot(HaveOccurred())
	_, err = backend.VoiceAnalyze(ctx, &pb.VoiceAnalyzeRequest{Audio: inline})
	Expect(err).ToNot(HaveOccurred())
	_, err = backend.VoiceEmbed(ctx, &pb.VoiceEmbedRequest{Audio: inline})
	Expect(err).ToNot(HaveOccurred())
	_, err = backend.AudioTransform(ctx, &pb.AudioTransformRequest{AudioPath: inline, ReferencePath: inline})
	Expect(err).ToNot(HaveOccurred())

	By("staging image references and retrieving image, video and 3D outputs")
	imageOut := filepath.Join(fixtureDir, "outputs/generated.png")
	imageResult, err := backend.GenerateImage(ctx, &pb.GenerateImageRequest{Src: imagePath, RefImages: []string{imagePath, imagePath}, Dst: imageOut})
	Expect(err).ToNot(HaveOccurred())
	Expect(imageResult.Success).To(BeTrue(), imageResult.Message)
	Expect(imageResult.Message).To(ContainSubstring("src=" + conformanceDigest(image)))
	Expect(imageResult.Message).To(ContainSubstring("ref_image[1]=" + conformanceDigest(image)))
	generatedImage, err := os.ReadFile(imageOut)
	Expect(err).ToNot(HaveOccurred())
	Expect(generatedImage).To(Equal(conformanceArtifact(conformancePNG,
		"src="+conformanceDigest(image), "ref_image[0]="+conformanceDigest(image), "ref_image[1]="+conformanceDigest(image))))

	videoOut := filepath.Join(fixtureDir, "outputs/generated.mp4")
	videoResult, err := backend.GenerateVideo(ctx, &pb.GenerateVideoRequest{
		StartImage: imagePath, EndImage: imagePath, Audio: audioPath, Dst: videoOut,
		Params: map[string]string{"extended-protocol": "preserved"},
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(videoResult.Success).To(BeTrue(), videoResult.Message)
	for _, marker := range []string{"start_image=" + conformanceDigest(image), "end_image=" + conformanceDigest(image), "audio=" + conformanceDigest(audio)} {
		Expect(videoResult.Message).To(ContainSubstring(marker))
	}
	generatedVideo, err := os.ReadFile(videoOut)
	Expect(err).ToNot(HaveOccurred())
	Expect(generatedVideo).To(Equal(conformanceArtifact(conformanceVideo,
		"start_image="+conformanceDigest(image), "end_image="+conformanceDigest(image), "audio="+conformanceDigest(audio))))

	assetOut := filepath.Join(fixtureDir, "outputs/generated.glb")
	assetResult, err := backend.Generate3D(ctx, &pb.Generate3DRequest{
		Src: imagePath, Dst: assetOut, Quality: "1024", Params: map[string]string{"extended-protocol": "preserved"},
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(assetResult.Success).To(BeTrue(), assetResult.Message)
	Expect(assetResult.Message).To(ContainSubstring("src=" + conformanceDigest(image)))
	generatedAsset, err := os.ReadFile(assetOut)
	Expect(err).ToNot(HaveOccurred())
	Expect(generatedAsset).To(Equal(conformanceArtifact(conformanceGLB, "src="+conformanceDigest(image))))

	By("staging TTS model, voice and every multiple-reference input")
	references, err := json.Marshal([]map[string]string{{"audio": refAPath, "text": "a"}, {"audio": refBPath, "text": "b"}})
	Expect(err).ToNot(HaveOccurred())
	ttsRequest := &pb.TTSRequest{
		Text: "fixture", Model: ttsModelPath, Voice: voicePath, Dst: filepath.Join(fixtureDir, "outputs/tts.wav"),
		Params: map[string]string{"multi_reference_cond": string(references)},
	}
	ttsResult, err := backend.TTS(ctx, ttsRequest)
	Expect(err).ToNot(HaveOccurred())
	Expect(ttsResult.Success).To(BeTrue(), ttsResult.Message)
	for _, marker := range []string{"model=" + conformanceDigest([]byte(tinyArtifact())), "voice=" + conformanceDigest(voice), "reference[0]=" + conformanceDigest(refA), "reference[1]=" + conformanceDigest(refB)} {
		Expect(ttsResult.Message).To(ContainSubstring(marker))
	}
	ttsBytes, err := os.ReadFile(ttsRequest.Dst)
	Expect(err).ToNot(HaveOccurred())
	Expect(ttsBytes[:4]).To(Equal([]byte("RIFF")))
	var ttsStream []*pb.Reply
	ttsStreamRequest := &pb.TTSRequest{Text: "fixture stream", Voice: voicePath, Params: map[string]string{"multi_reference_cond": string(references)}}
	Expect(backend.TTSStream(ctx, ttsStreamRequest, func(reply *pb.Reply) { ttsStream = append(ttsStream, reply) })).To(Succeed())
	Expect(ttsStream).ToNot(BeEmpty())
	Expect(string(ttsStream[0].Message)).To(ContainSubstring(conformanceDigest(voice)))
	Expect(string(ttsStream[0].Message)).To(ContainSubstring(conformanceDigest(refB)))

	By("staging sound generation, detection, and unary and streaming transcription inputs")
	soundOut := filepath.Join(fixtureDir, "outputs/sound.wav")
	src := audioPath
	soundResult, err := backend.SoundGeneration(ctx, &pb.SoundGenerationRequest{Text: "fixture", Src: &src, Dst: soundOut})
	Expect(err).ToNot(HaveOccurred())
	Expect(soundResult.Success).To(BeTrue(), soundResult.Message)
	Expect(soundResult.Message).To(ContainSubstring("src=" + conformanceDigest(audio)))
	soundBytes, err := os.ReadFile(soundOut)
	Expect(err).ToNot(HaveOccurred())
	Expect(soundBytes[:4]).To(Equal([]byte("RIFF")))
	detection, err := backend.SoundDetection(ctx, &pb.SoundDetectionRequest{Src: audioPath, TopK: 1})
	Expect(err).ToNot(HaveOccurred())
	Expect(detection.Detections).To(HaveLen(1))
	Expect(detection.Detections[0].Label).To(ContainSubstring("src=" + conformanceDigest(audio)))
	transcript, err := backend.AudioTranscription(ctx, &pb.TranscriptRequest{Dst: audioPath})
	Expect(err).ToNot(HaveOccurred())
	Expect(transcript.Text).To(ContainSubstring("audio=" + conformanceDigest(audio)))
	var transcriptStream []*pb.TranscriptStreamResponse
	Expect(backend.AudioTranscriptionStream(ctx, &pb.TranscriptRequest{Dst: audioPath}, func(reply *pb.TranscriptStreamResponse) {
		transcriptStream = append(transcriptStream, reply)
	})).To(Succeed())
	Expect(transcriptStream).ToNot(BeEmpty())
	Expect(transcriptStream[len(transcriptStream)-1].GetFinalResult().GetText()).To(ContainSubstring("audio=" + conformanceDigest(audio)))

	By("retrieving nested export and completed quantization outputs")
	exportDir := filepath.Join(fixtureDir, "exported-model")
	exportResult, err := backend.ExportModel(ctx, &pb.ExportModelRequest{
		CheckpointPath: imagePath, Model: videoPath, OutputPath: exportDir, ExportFormat: "gguf",
		ExtraOptions: map[string]string{"extended-protocol": "preserved"},
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(exportResult.Success).To(BeTrue(), exportResult.Message)
	Expect(exportResult.Message).To(ContainSubstring("checkpoint=" + conformanceDigest(image)))
	Expect(exportResult.Message).To(ContainSubstring("model=" + conformanceDigest(video)))
	Expect(os.ReadFile(filepath.Join(exportDir, "nested", "weights.bin"))).To(Equal([]byte("MOCK-EXPORTED-WEIGHTS\n")))
	Expect(os.ReadFile(filepath.Join(exportDir, "nested", "config.json"))).To(Equal([]byte("{\"mock\":true}\n")))

	quantDir := filepath.Join(fixtureDir, "quantized")
	jobID := "binary-conformance-quantization-" + requestIDForTest()
	job, err := backend.StartQuantization(ctx, &pb.QuantizationRequest{
		Model: imagePath, QuantizationType: "q4_k_m", OutputDir: quantDir, JobId: jobID,
		ExtraOptions: map[string]string{"extended-protocol": "preserved"},
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(job.Success).To(BeTrue(), job.Message)
	Expect(job.Message).To(ContainSubstring("model=" + conformanceDigest(image)))
	var progress []*pb.QuantizationProgressUpdate
	Expect(backend.QuantizationProgress(ctx, &pb.QuantizationProgressRequest{JobId: jobID}, func(update *pb.QuantizationProgressUpdate) {
		progress = append(progress, update)
	})).To(Succeed())
	Expect(progress).To(HaveLen(1))
	Expect(progress[0].Status).To(Equal("completed"))
	Expect(progress[0].ProgressPercent).To(Equal(float32(100)))
	Expect(progress[0].OutputFile).To(Equal(filepath.Join(quantDir, "nested", jobID+".gguf")))
	Expect(os.ReadFile(progress[0].OutputFile)).To(Equal([]byte("MOCK-GGUF:q4_k_m\n")))

	stopJobID := "binary-conformance-stop-" + requestIDForTest()
	stopJob, err := backend.StartQuantization(ctx, &pb.QuantizationRequest{Model: imagePath, JobId: stopJobID})
	Expect(err).ToNot(HaveOccurred())
	Expect(stopJob.Success).To(BeTrue(), stopJob.Message)
	stopResult, err := backend.StopQuantization(ctx, &pb.QuantizationStopRequest{JobId: stopJobID})
	Expect(err).ToNot(HaveOccurred())
	Expect(stopResult.Success).To(BeTrue(), stopResult.Message)

	Expect(owners.ownerIndexOf(c, 2, workerID)).To(Equal(ownerFrontend))
	Expect(relayFrontend).To(Equal(1 - ownerFrontend))
}

func requestIDForTest() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

var _ = Describe("Binary backend feature conformance", Label("Distributed"), Label("Cluster"), Label("BinaryConformance"), func() {
	It("binary backend feature conformance through the tunnel owner and a peer relay", func() {
		const model = "conformance"
		fixtures := newConformanceFixtures()
		c, dsn := startClusterOnFreshDB(2, 2, withMockModel(model), func(o *cluster.Options) {
			o.ConformanceStaging = true
			o.SpreadWorkerRegistrations = true
			o.Models[model+".onnx"] = tinyArtifact()
			o.Models[model+".onnx.json"] = `{"companion":true}`
			o.Models[model+"-original.yaml"] = "fixture: original-config\n"
			o.Models[model+"-draft.gguf"] = "frontend-only-draft"
			o.Models[model+"-mmproj.gguf"] = "frontend-only-mmproj"
			o.Models[model+".yaml"] = fmt.Sprintf(`name: %s
backend: mock-backend
parameters:
  model: %s.onnx
draft_model: %s-draft.gguf
mmproj: %s-mmproj.gguf
diffusers:
  original_config_file: %s-original.yaml
known_usecases:
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
`, model, model, model, model, model)
		})
		client := inferenceClient(c)
		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ConsistOf(c.WorkerName(0), c.WorkerName(1)), probe.describe)
		workerID := probe.idOf(c.WorkerName(0))
		Expect(workerID).ToNot(BeEmpty())

		db := openClusterDB(dsn)
		installConformanceDiskCapacity(db, 1<<30)
		By("proving disk-headroom admission remains enforced below the store-model floor")
		resp, payload := conformancePostJSON(client, c.FrontendURL(0), "/stores/set", map[string]any{
			"store": "capacity-rejection", "backend": "mock-backend",
			"keys": [][]float32{{1}}, "values": []string{"must-not-store"},
		})
		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError), string(payload))
		Expect(string(payload)).To(ContainSubstring("no node has enough free disk for the model"))
		setConformanceDiskCapacity(db, 8<<30)

		pinModelToNode(c, client, 0, model, workerID, "conformance-primary")

		owners := newTunnelOwners(db)
		var ownerFrontend int
		Eventually(func() int {
			ownerFrontend = owners.ownerIndexOf(c, 2, workerID)
			return ownerFrontend
		}, instanceRosterTimeout, instanceRosterPoll).Should(BeElementOf(0, 1), owners.describe)
		relayFrontend := 1 - ownerFrontend
		By("pinning the explicit owner-public and relay-protocol staging topology")
		Expect(fileStagingTopologyCoverage).To(HaveLen(24))
		for _, coverage := range fileStagingTopologyCoverage {
			Expect(coverage.method).ToNot(BeEmpty())
			Expect(coverage.ownerPublicPath).ToNot(BeEmpty(), coverage.method)
			Expect(coverage.relayProtocolPath).To(Equal("Backend/"+coverage.method), coverage.method)
		}

		runPublicBackendConformance(client, c.FrontendURL(ownerFrontend), model, fixtures)
		servedBy(c, client, ownerFrontend, model, workerID, probe.idOf(c.WorkerName(1)))
		Expect(owners.ownerIndexOf(c, 2, workerID)).To(Equal(ownerFrontend))

		runPublicBackendConformance(client, c.FrontendURL(relayFrontend), model, fixtures)
		servedBy(c, client, relayFrontend, model, workerID, probe.idOf(c.WorkerName(1)))
		Expect(owners.ownerIndexOf(c, 2, workerID)).To(Equal(ownerFrontend))

		runFileStagingConformance(c, db, owners, workerID, model, ownerFrontend, relayFrontend)

		By("proving the frontend-only model artifact arrived intact at the worker")
		workerModels, err := c.WorkerModelsDir(0)
		Expect(err).ToNot(HaveOccurred())
		staged, err := os.ReadFile(filepath.Join(workerModels, model, model+".onnx"))
		Expect(err).ToNot(HaveOccurred())
		Expect(staged).To(Equal([]byte(tinyArtifact())))
		Expect(os.ReadFile(filepath.Join(workerModels, model, model+".onnx.json"))).To(Equal([]byte(`{"companion":true}`)))
		Expect(os.ReadFile(filepath.Join(workerModels, model, model+"-draft.gguf"))).To(Equal([]byte("frontend-only-draft")))
		Expect(os.ReadFile(filepath.Join(workerModels, model, model+"-mmproj.gguf"))).To(Equal([]byte("frontend-only-mmproj")))
		Expect(os.ReadFile(filepath.Join(workerModels, model, model+"-original.yaml"))).To(Equal([]byte("fixture: original-config\n")))
		Expect(strings.Contains(workerModels, "worker-0")).To(BeTrue())

		By("shutting down the real worker without orphaning its loaded backend")
		backendPIDs, err := c.WorkerBackendPIDs(0, "mock-backend")
		Expect(err).ToNot(HaveOccurred())
		Expect(backendPIDs).ToNot(BeEmpty(), "conformance requests never started a mock backend")
		c.Stop()
		Eventually(func() []int {
			var survivors []int
			for _, pid := range backendPIDs {
				if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
					survivors = append(survivors, pid)
				}
			}
			return survivors
		}, 5*time.Second, 100*time.Millisecond).Should(BeEmpty(), "mock backends survived cluster teardown")
	})
})
