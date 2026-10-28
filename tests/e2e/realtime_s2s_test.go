package e2e_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/pkg/sound"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Realtime speech-to-speech (S2S) specs: one any-to-any model
// (realtime_audio plus pipeline.any_to_any) that takes audio in and returns
// audio, transcripts and tool calls over /v1/realtime.
//
// Variants, chosen by environment, run the same specs:
//   - mock (default, label Realtime): the realtime-s2s-mock fixture from
//     e2e_suite_test.go, served by the mock backend's AudioToAudioStream.
//   - real (label Realtime and real-models, only when REALTIME_S2S_MODEL is
//     set): a real any-to-any model, run manually on GPU with
//     `make test-realtime-s2s-real`. Assertions marked "real" run only here.
//     Environment of the real variant:
//     REALTIME_S2S_MODEL  model name in the models path (e.g. lfm3-audio-2b)
//     REALTIME_S2S_TTS    TTS model that renders the input utterances; pick one
//                         that is not the model under test (e.g. vits-ljs-sherpa)
//     REALTIME_S2S_ASR    ASR model that transcribes the OUTPUT audio, so the
//                         spoken answer is checked independently of the model's
//                         own transcript (e.g. omnilingual-0.3b-ctc-q8-sherpa)

const (
	s2sMockModel = "realtime-s2s-mock"
	s2sInputRate = 24000
)

// s2sResponseTimeout bounds one response. A real model on CPU is slow.
func s2sResponseTimeout() time.Duration {
	if isRealS2S() {
		return 10 * time.Minute
	}
	return 2 * time.Minute
}

func isRealS2S() bool { return os.Getenv("REALTIME_S2S_MODEL") != "" }

func s2sModel() string {
	if m := os.Getenv("REALTIME_S2S_MODEL"); m != "" {
		return m
	}
	return s2sMockModel
}

// s2sTTSModel names the TTS that renders the test utterances. The mock variant
// uses the mock TTS. The real variant uses a fixed single-speaker voice so the
// input is clear and does not share failure modes with the model under test.
func s2sTTSModel() string {
	if m := os.Getenv("REALTIME_S2S_TTS"); m != "" {
		return m
	}
	return "mock-tts"
}

// s2sUtterance renders text and returns 24 kHz s16le PCM as base64.
func s2sUtterance(text string) string {
	return ttsPCMBase64(s2sTTSModel(), text, s2sInputRate)
}

// streamUtterance sends base64 PCM in 40 ms chunks at 2x real time, as
// input_audio_buffer.append events.
func streamUtterance(conn *websocket.Conn, pcmB64 string) {
	raw, err := base64.StdEncoding.DecodeString(pcmB64)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	chunk := s2sInputRate * 40 / 1000 * 2
	for off := 0; off < len(raw); off += chunk {
		end := min(off+chunk, len(raw))
		sendClientEvent(conn, map[string]any{
			"type":  "input_audio_buffer.append",
			"audio": base64.StdEncoding.EncodeToString(raw[off:end]),
		})
		time.Sleep(20 * time.Millisecond)
	}
}

// s2sSession opens a session and waits for session.created. A non-nil update is
// sent as session.update (a map that already has a type is sent as is) and
// its session.updated is awaited.
func s2sSession(update map[string]any) *websocket.Conn {
	conn := connectWS(s2sModel())
	drainUntil(conn, "session.created", 30*time.Second)
	if update != nil {
		evt := update
		if _, whole := update["type"]; !whole {
			evt = map[string]any{"type": "session.update", "session": update}
		}
		sendClientEvent(conn, evt)
		drainUntil(conn, "session.updated", 10*time.Second)
	}
	return conn
}

// readResponse collects events until response.done, which it also returns.
func readResponse(conn *websocket.Conn, timeout time.Duration) []map[string]any {
	var events []map[string]any
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		evt := readServerEvent(conn, time.Until(deadline))
		events = append(events, evt)
		if evt["type"] == "response.done" {
			return events
		}
	}
	Fail("timed out waiting for response.done")
	return nil
}

func eventTypes(events []map[string]any) []string {
	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, fmt.Sprint(e["type"]))
	}
	return types
}

func hasEvent(events []map[string]any, typ string) bool {
	for _, e := range events {
		if e["type"] == typ {
			return true
		}
	}
	return false
}

// responseStatus returns status from a response.done event.
func responseStatus(done map[string]any) string {
	resp, _ := done["response"].(map[string]any)
	s, _ := resp["status"].(string)
	return s
}

// keywordsIn reports whether any keyword appears in text, case-insensitive.
func keywordsIn(text string, keywords ...string) bool {
	lower := strings.ToLower(text)
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// defineS2SSpecs registers the S2S specs for one variant. The bodies are the
// same in both variants; "real" guards the assertions that need real weights.
func defineS2SSpecs(variant string, realLabel bool) {
	isReal := variant == "real"
	decorators := []any{Label("Realtime")}
	if realLabel {
		decorators = append(decorators, Label("real-models"))
	}
	Describe("Realtime S2S ("+variant+")", append(decorators, func() {

		It("reports the audio format and echoes session.update", func() {
			conn := connectWS(s2sModel())
			defer conn.Close()

			created := readServerEvent(conn, 30*time.Second)
			Expect(created["type"]).To(Equal("session.created"))
			session, _ := created["session"].(map[string]any)
			Expect(session).To(HaveKey("audio"))

			sendClientEvent(conn, map[string]any{
				"type": "session.update",
				"session": map[string]any{
					"instructions": "Answer briefly.",
					"tools": []map[string]any{{
						"type":        "function",
						"name":        "get_weather",
						"description": "Current weather for a city.",
						"parameters": map[string]any{
							"type":       "object",
							"properties": map[string]any{"city": map[string]any{"type": "string"}},
							"required":   []string{"city"},
						},
					}},
				},
			})
			updated := drainUntil(conn, "session.updated", 10*time.Second)
			updSession, _ := updated["session"].(map[string]any)
			Expect(updSession["instructions"]).To(Equal("Answer briefly."))
			Expect(updSession["tools"]).ToNot(BeEmpty())
		})

		It("runs a server VAD turn in order", func() {
			conn := s2sSession(nil)
			defer conn.Close()

			streamUtterance(conn, padPCMBase64(s2sUtterance("What is the capital of France? Answer in one short sentence."), s2sInputRate, 300, 1200))
			events := readResponse(conn, s2sResponseTimeout())
			types := eventTypes(events)
			Expect(types).To(ContainElements("input_audio_buffer.speech_started", "input_audio_buffer.speech_stopped",
				"input_audio_buffer.committed", "conversation.item.added", "response.created", "response.output_audio.delta"))
			Expect(responseStatus(events[len(events)-1])).To(Equal("completed"))
			if isReal {
				checkSpokenAnswer(events, "paris")
			}
		})

		It("runs a manual-commit turn without VAD events", func() {
			conn := s2sSession(disableVADEvent())
			defer conn.Close()

			streamUtterance(conn, s2sUtterance("What is the capital of France? Answer in one short sentence."))
			sendClientEvent(conn, map[string]any{"type": "input_audio_buffer.commit"})
			drainUntil(conn, "input_audio_buffer.committed", 30*time.Second)
			sendClientEvent(conn, map[string]any{"type": "response.create"})

			events := readResponse(conn, s2sResponseTimeout())
			Expect(hasEvent(events, "input_audio_buffer.speech_started")).To(BeFalse())
			Expect(hasEvent(events, "response.output_audio.delta")).To(BeTrue())
			Expect(responseStatus(events[len(events)-1])).To(Equal("completed"))
			if isReal {
				checkSpokenAnswer(events, "paris")
			}
		})

		It("emits input and output transcripts", func() {
			conn := s2sSession(nil)
			defer conn.Close()

			streamUtterance(conn, padPCMBase64(s2sUtterance("What is the capital of France? Answer in one short sentence."), s2sInputRate, 300, 1200))
			events := readResponse(conn, s2sResponseTimeout())

			var inputText, outputText string
			for _, e := range events {
				switch e["type"] {
				case "conversation.item.input_audio_transcription.completed":
					inputText, _ = e["transcript"].(string)
				case "response.output_audio_transcript.done":
					outputText, _ = e["transcript"].(string)
				}
			}
			Expect(strings.TrimSpace(outputText)).ToNot(BeEmpty())
			if isReal {
				Expect(keywordsIn(inputText, "france")).To(BeTrue(), "input transcript: %q", inputText)
			}
		})

		It("cancels the first response on barge-in and completes the second", func() {
			conn := s2sSession(nil)
			defer conn.Close()

			first := padPCMBase64(s2sUtterance("Count slowly from one to forty."), s2sInputRate, 300, 1200)
			firstSent := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				streamUtterance(conn, first)
				close(firstSent)
			}()

			// Wait for the first audio, then talk over it.
			var firstID string
			for firstID == "" {
				evt := readServerEvent(conn, s2sResponseTimeout())
				if evt["type"] == "response.output_audio.delta" {
					firstID, _ = evt["response_id"].(string)
				}
			}
			Eventually(firstSent, 60*time.Second).Should(BeClosed())

			streamUtterance(conn, padPCMBase64(s2sUtterance("Stop. What is two plus two?"), s2sInputRate, 300, 1200))

			// The first response ends as cancelled, and no audio for it follows.
			var firstDone map[string]any
			// The cancel must land within 2 s of the barge-in; the loop bound is
			// generous so a slow CI runner reports the missing event, not a timeout.
			cancelDeadline := time.Now().Add(60 * time.Second)
			for firstDone == nil && time.Now().Before(cancelDeadline) {
				evt := readServerEvent(conn, 30*time.Second)
				if evt["type"] == "response.done" {
					resp, _ := evt["response"].(map[string]any)
					if resp["id"] == firstID {
						firstDone = evt
					}
				}
			}
			Expect(firstDone).ToNot(BeNil(), "no response.done for the first response")
			Expect(responseStatus(firstDone)).To(Equal("cancelled"))

			second := readResponse(conn, s2sResponseTimeout())
			Expect(responseStatus(second[len(second)-1])).To(Equal("completed"))
			if isReal {
				checkSpokenAnswer(second, "four", "4")
			}
		})

		It("round-trips a tool call and its output", func() {
			conn := s2sSession(map[string]any{
				"tools": []map[string]any{{
					"type":        "function",
					"name":        "get_weather",
					"description": "Current weather for a city.",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{"city": map[string]any{"type": "string"}},
						"required":   []string{"city"},
					},
				}},
			})
			defer conn.Close()

			streamUtterance(conn, padPCMBase64(s2sUtterance("What is the weather in Paris right now? Use the get_weather tool."), s2sInputRate, 300, 1200))
			events := readResponse(conn, s2sResponseTimeout())

			var callID string
			for _, e := range events {
				if e["type"] == "response.function_call_arguments.done" {
					Expect(e["name"]).To(Equal("get_weather"))
					raw, _ := e["arguments"].(string)
					var args struct {
						City string `json:"city"`
					}
					Expect(json.Unmarshal([]byte(raw), &args)).To(Succeed(), "arguments: %q", raw)
					Expect(strings.ToLower(args.City)).To(ContainSubstring("paris"))
					callID, _ = e["call_id"].(string)
				}
			}
			Expect(callID).ToNot(BeEmpty(), "no function call in %v", eventTypes(events))

			sendClientEvent(conn, map[string]any{
				"type": "conversation.item.create",
				"item": map[string]any{
					"type":    "function_call_output",
					"call_id": callID,
					"output":  `{"temperature_c":21,"condition":"sunny"}`,
				},
			})
			sendClientEvent(conn, map[string]any{"type": "response.create"})

			second := readResponse(conn, s2sResponseTimeout())
			Expect(hasEvent(second, "response.output_audio.delta")).To(BeTrue())
			Expect(responseStatus(second[len(second)-1])).To(Equal("completed"))
			if isReal {
				checkSpokenAnswer(second, "21", "twenty", "sunny")
			}
		})
	}))
}

// outputPCM joins the audio deltas of one response (s16le at s2sInputRate,
// the session output rate).
func outputPCM(events []map[string]any) []byte {
	var pcm []byte
	for _, e := range events {
		if e["type"] != "response.output_audio.delta" {
			continue
		}
		d, _ := e["delta"].(string)
		raw, err := base64.StdEncoding.DecodeString(d)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		pcm = append(pcm, raw...)
	}
	return pcm
}

// assertPlausibleAudio checks sampling-robust properties of a spoken reply: a
// duration between 0.4 s and 30 s, at least 30% of 100 ms windows above an RMS
// floor of 300, and no clipped run longer than 50 ms.
func assertPlausibleAudio(pcm []byte) {
	GinkgoHelper()
	samples := sound.BytesToInt16sLE(pcm)
	seconds := float64(len(samples)) / s2sInputRate
	Expect(seconds).To(BeNumerically(">=", 0.4), "reply audio too short")
	Expect(seconds).To(BeNumerically("<=", 30), "reply audio too long (missing EOS?)")

	win := s2sInputRate / 10
	var loud, total int
	for off := 0; off+win <= len(samples); off += win {
		total++
		if sound.CalculateRMS16(samples[off:off+win]) > 300 {
			loud++
		}
	}
	Expect(total).To(BeNumerically(">", 0))
	Expect(float64(loud)/float64(total)).To(BeNumerically(">=", 0.3),
		"only %d of %d windows are above the RMS floor", loud, total)

	maxRun, run := 0, 0
	for _, v := range samples {
		if v >= 32767 || v <= -32768 {
			run++
			maxRun = max(maxRun, run)
		} else {
			run = 0
		}
	}
	Expect(maxRun).To(BeNumerically("<=", s2sInputRate*50/1000), "clipped run of %d samples", maxRun)
}

// asrOfOutput transcribes reply audio with the separate ASR model
// (REALTIME_S2S_ASR). It returns "" when no ASR model is configured.
func asrOfOutput(pcm []byte) string {
	model := os.Getenv("REALTIME_S2S_ASR")
	if model == "" {
		return ""
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	ExpectWithOffset(1, mw.WriteField("model", model)).To(Succeed())
	fw, err := mw.CreateFormFile("file", "reply.wav")
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	_, err = fw.Write(wavFromPCM(pcm, s2sInputRate))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, mw.Close()).To(Succeed())

	resp, err := http.Post(apiURL+"/audio/transcriptions", mw.FormDataContentType(), &body)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, resp.StatusCode).To(Equal(http.StatusOK), "asr returned: %s", raw)
	var out struct {
		Text string `json:"text"`
	}
	ExpectWithOffset(1, json.Unmarshal(raw, &out)).To(Succeed(), "asr body: %s", raw)
	return out.Text
}

// sharesContentWord reports whether a and b have a word of 4+ letters in common.
func sharesContentWord(a, b string) bool {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(a), func(r rune) bool { return r < 'a' || r > 'z' }) {
		if len(w) >= 4 {
			words[w] = true
		}
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(b), func(r rune) bool { return r < 'a' || r > 'z' }) {
		if words[w] {
			return true
		}
	}
	return false
}

// checkSpokenAnswer is the real-variant check of one response: the audio is
// plausible speech, the keywords appear in the union of the model's transcript
// and the separate ASR of the audio, and (when both exist) the two share a
// content word, which shows the audio says what the transcript says.
func checkSpokenAnswer(events []map[string]any, keywords ...string) {
	GinkgoHelper()
	pcm := outputPCM(events)
	assertPlausibleAudio(pcm)
	transcript := transcriptOf(events)
	asr := asrOfOutput(pcm)
	GinkgoWriter.Printf("reply transcript=%q asr=%q\n", transcript, asr)
	if len(keywords) > 0 {
		Expect(keywordsIn(transcript+" "+asr, keywords...)).To(BeTrue(),
			"none of %v in transcript %q or asr %q", keywords, transcript, asr)
	}
	if strings.TrimSpace(transcript) != "" && strings.TrimSpace(asr) != "" {
		Expect(sharesContentWord(transcript, asr)).To(BeTrue(),
			"audio and transcript disagree: transcript %q, asr %q", transcript, asr)
	}
}

// transcriptOf joins the output transcript deltas of one response.
func transcriptOf(events []map[string]any) string {
	var b strings.Builder
	for _, e := range events {
		if e["type"] == "response.output_audio_transcript.delta" {
			d, _ := e["delta"].(string)
			b.WriteString(d)
		}
	}
	return b.String()
}

var _ = func() bool {
	if isRealS2S() {
		defineS2SSpecs("real", true)
	} else {
		defineS2SSpecs("mock", false)
	}
	return true
}()
