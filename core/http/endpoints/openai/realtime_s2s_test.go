package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fakeS2SStream is a scripted backend stream. script runs inline in Send, so a
// control message can trigger the backend's answer.
type fakeS2SStream struct {
	mu     sync.Mutex
	sent   []*proto.AudioToAudioRequest
	recv   chan *proto.AudioToAudioResponse
	script func(f *fakeS2SStream, req *proto.AudioToAudioRequest)
	once   sync.Once
	owner  *s2sSession // flushed before the sent requests are read
}

func newFakeS2SStream() *fakeS2SStream {
	return &fakeS2SStream{recv: make(chan *proto.AudioToAudioResponse, 256)}
}

func (f *fakeS2SStream) Send(req *proto.AudioToAudioRequest) error {
	f.mu.Lock()
	f.sent = append(f.sent, req)
	f.mu.Unlock()
	if f.script != nil {
		f.script(f, req)
	}
	return nil
}

func (f *fakeS2SStream) Recv() (*proto.AudioToAudioResponse, error) {
	r, ok := <-f.recv
	if !ok {
		return nil, io.EOF
	}
	return r, nil
}

func (f *fakeS2SStream) Close() error {
	f.once.Do(func() { close(f.recv) })
	return nil
}

func (f *fakeS2SStream) push(event string, pcm []byte, meta any) {
	b, _ := json.Marshal(meta)
	f.recv <- &proto.AudioToAudioResponse{Event: event, Pcm: pcm, SampleRate: 24000, Meta: b}
}

func (f *fakeS2SStream) requests() []*proto.AudioToAudioRequest {
	if f.owner != nil {
		f.owner.flush()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*proto.AudioToAudioRequest(nil), f.sent...)
}

// controls returns the control event names in send order.
func (f *fakeS2SStream) controls() []string {
	var out []string
	for _, r := range f.requests() {
		if c := r.GetControl(); c != nil {
			out = append(out, c.GetEvent())
		}
	}
	return out
}

// eofStream fails like a gRPC client stream whose call was rejected: Send
// returns a bare io.EOF and Recv has the real status.
type eofStream struct {
	status error
	failed chan struct{}
	once   sync.Once
}

func (e *eofStream) Send(*proto.AudioToAudioRequest) error {
	e.once.Do(func() { close(e.failed) })
	return io.EOF
}

func (e *eofStream) Recv() (*proto.AudioToAudioResponse, error) {
	<-e.failed
	return nil, e.status
}

func (e *eofStream) Close() error { return nil }

func isControl(r *proto.AudioToAudioRequest, event string) bool {
	return r.GetControl() != nil && r.GetControl().GetEvent() == event
}

func eventTypeNames(evs []types.ServerEvent) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, string(e.ServerEventType()))
	}
	return out
}

// fromResponseCreated returns the event names starting at response.created.
func fromResponseCreated(evs []types.ServerEvent) []string {
	names := eventTypeNames(evs)
	for i, n := range names {
		if n == "response.created" {
			return names[i:]
		}
	}
	return nil
}

var _ = Describe("realtime speech-to-speech", func() {
	const rate = 16000

	newHarness := func(stream *fakeS2SStream, m *fakeModel) (*Session, *fakeTransport, *Conversation) {
		st := newS2SSession(stream, nil, nil)
		st.inputRate, st.outputRate = rate, 24000
		stream.owner = st
		session := &Session{
			s2s:                     st,
			TurnDetection:           &types.TurnDetectionUnion{ServerVad: &types.ServerVad{}},
			InputAudioTranscription: &types.AudioTranscription{},
			ModelInterface:          m,
			ModelConfig:             &config.ModelConfig{},
			InputSampleRate:         rate,
			OutputSampleRate:        24000,
			respSink:                newResponseSink(),
		}
		session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
		DeferCleanup(func() {
			session.sessionCancel()
			session.respSink.shutdown()
			st.close()
		})
		return session, &fakeTransport{}, &Conversation{}
	}
	baseModel := func() *fakeModel {
		return &fakeModel{
			cfg:             &config.ModelConfig{},
			transcribeFinal: &schema.TranscriptionResult{Text: "hello there"},
		}
	}
	replyScript := func(f *fakeS2SStream, req *proto.AudioToAudioRequest) {
		if isControl(req, "response.create") {
			f.push("response.audio_transcript.delta", nil, map[string]any{"delta": "hi "})
			f.push("response.audio.delta", []byte{1, 2, 3, 4}, nil)
			f.push("response.audio_transcript.delta", nil, map[string]any{"delta": "there"})
			f.push("response.audio.delta", []byte{5, 6, 7, 8}, nil)
			f.push("response.done", nil, map[string]any{"status": "completed", "text": "hi there", "usage": map[string]any{"output_tokens": 2}})
		}
	}

	It("answers a manual commit and response.create in the documented order", func() {
		stream := newFakeS2SStream()
		stream.script = replyScript
		session, tr, conv := newHarness(stream, baseModel())

		session.s2sEndTurn()
		session.issueClientCommit([]byte{1, 2, 3, 4}, conv, tr)
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()

		Expect(fromResponseCreated(tr.events())).To(Equal([]string{
			"response.created",
			"response.output_item.added",
			"response.content_part.added",
			"response.output_audio_transcript.delta",
			"response.output_audio.delta",
			"response.output_audio_transcript.delta",
			"response.output_audio.delta",
			"response.output_audio.done",
			"response.output_audio_transcript.done",
			"response.content_part.done",
			"response.output_item.done",
			"response.done",
		}))
		Expect(tr.transcriptDeltaText()).To(Equal("hi there"))
		Expect(stream.controls()).To(Equal([]string{"input_audio_buffer.commit", "response.create"}))

		// A client commit does not start a response on its own.
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		// The user item is added and its transcript reported (T7).
		names := eventTypeNames(tr.events())
		Expect(names).To(ContainElements("conversation.item.added", "conversation.item.input_audio_transcription.completed"))
	})

	It("reports a failed input transcription without breaking the response", func() {
		stream := newFakeS2SStream()
		stream.script = replyScript
		m := baseModel()
		m.transcribeFinal = nil
		m.transcribeErr = errors.New("asr down")
		session, tr, conv := newHarness(stream, m)

		session.s2sEndTurn()
		session.issueClientCommit([]byte{1, 2, 3, 4}, conv, tr)
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()

		names := eventTypeNames(tr.events())
		Expect(names).To(ContainElement("conversation.item.input_audio_transcription.failed"))
		Expect(names).ToNot(ContainElement("conversation.item.input_audio_transcription.completed"))
		Expect(names[len(names)-1]).To(Equal("response.done"))
		Expect(tr.countEvents(types.ServerEventTypeResponseOutputAudioTranscriptDone)).To(Equal(1))
	})

	It("round-trips a tool call and its output", func() {
		stream := newFakeS2SStream()
		creates := 0
		stream.script = func(f *fakeS2SStream, req *proto.AudioToAudioRequest) {
			if !isControl(req, "response.create") {
				return
			}
			creates++
			if creates == 1 {
				f.push("response.function_call_arguments.done", nil, map[string]any{"call_id": "x", "name": "get_weather", "arguments": `{"city":"Paris"}`})
				f.push("response.done", nil, map[string]any{"status": "completed"})
				return
			}
			replyScript(f, req)
		}
		session, tr, conv := newHarness(stream, baseModel())

		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()

		Expect(fromResponseCreated(tr.events())).To(Equal([]string{
			"response.created",
			"response.output_item.added",
			"response.function_call_arguments.delta",
			"response.function_call_arguments.done",
			"response.output_item.done",
			"response.done",
		}))
		var callID string
		for _, e := range tr.events() {
			if d, ok := e.(types.ResponseFunctionCallArgumentsDoneEvent); ok {
				Expect(d.Name).To(Equal("get_weather"))
				Expect(d.Arguments).To(Equal(`{"city":"Paris"}`))
				callID = d.CallID
			}
		}
		Expect(callID).ToNot(BeEmpty())

		// What the item.create handler does for the client's tool result.
		conv.Lock.Lock()
		conv.Items = append(conv.Items, &types.MessageItemUnion{FunctionCallOutput: &types.MessageItemFunctionCallOutput{
			ID: generateItemID(), CallID: callID, Output: `{"temperature_c":21}`,
		}})
		conv.Lock.Unlock()
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()

		var outputCtl *proto.AudioToAudioControl
		reqs := stream.requests()
		for i, r := range reqs {
			if isControl(r, "conversation.item.create") {
				outputCtl = r.GetControl()
				Expect(isControl(reqs[i+1], "response.create")).To(BeTrue(), "the result precedes the response")
			}
		}
		Expect(outputCtl).ToNot(BeNil())
		var payload map[string]string
		Expect(json.Unmarshal(outputCtl.GetPayload(), &payload)).To(Succeed())
		Expect(payload).To(HaveKeyWithValue("type", "function_call_output"))
		Expect(payload).To(HaveKeyWithValue("call_id", callID))
		Expect(tr.countEvents(types.ServerEventTypeResponseDone)).To(Equal(2))
		Expect(tr.countEvents(types.ServerEventTypeResponseOutputAudioDelta)).To(Equal(2))
	})

	It("cancels on barge-in before the new speech and forwards no audio after it", func() {
		stream := newFakeS2SStream()
		cancelled := make(chan struct{})
		stream.script = func(f *fakeS2SStream, req *proto.AudioToAudioRequest) {
			switch {
			case isControl(req, "response.create"):
				go func() {
					for {
						select {
						case <-cancelled:
							return
						case <-time.After(5 * time.Millisecond):
							f.push("response.audio.delta", []byte{1, 1}, nil)
						}
					}
				}()
			case isControl(req, "response.cancel"):
				close(cancelled)
				// In-flight audio the backend had already produced.
				for range 3 {
					f.push("response.audio.delta", []byte{0xEE, 0xEE}, nil)
				}
				f.push("response.done", nil, map[string]any{"status": "cancelled"})
			}
		}
		session, tr, conv := newHarness(stream, baseModel())
		session.InputAudioBuffer = make([]byte, rate*2) // one second of the new speech

		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		Eventually(func() int { return tr.countEvents(types.ServerEventTypeResponseOutputAudioDelta) }, 2*time.Second).Should(BeNumerically(">=", 2))

		// What the VAD does on speech onset: start the turn, then cancel.
		session.s2sBeginTurn(0)
		session.respSink.cancel(respcoord.SourceVAD)
		session.respSink.wait()

		reqs := stream.requests()
		cancelAt, frameAt := -1, -1
		for i, r := range reqs {
			if isControl(r, "response.cancel") && cancelAt < 0 {
				cancelAt = i
			}
			if r.GetFrame() != nil && frameAt < 0 {
				frameAt = i
			}
		}
		Expect(cancelAt).To(BeNumerically(">=", 0))
		Expect(frameAt).To(BeNumerically(">", cancelAt), "the cancel goes before the first frame")
		Expect(stream.controls()).To(ContainElement("response.cancel"))
		Expect(countCancels(stream)).To(Equal(1))

		for _, e := range tr.events() {
			if d, ok := e.(types.ResponseOutputAudioDeltaEvent); ok {
				Expect(d.Delta).ToNot(Equal("7u4="), "audio produced after the cancel reached the client")
			}
		}
		names := eventTypeNames(tr.events())
		Expect(names[len(names)-1]).To(Equal("response.done"))
		var done types.ResponseDoneEvent
		for _, e := range tr.events() {
			if d, ok := e.(types.ResponseDoneEvent); ok {
				done = d
			}
		}
		Expect(done.Response.Status).To(Equal(types.ResponseStatusCancelled))
	})

	It("forwards a VAD turn from speech start minus the pre-roll", func() {
		stream := newFakeS2SStream()
		session, _, _ := newHarness(stream, baseModel())
		session.InputAudioBuffer = make([]byte, 3*rate*2) // 3 s

		// Before the turn opens nothing goes out.
		session.s2sOnAppendLocked(make([]byte, 640))
		Expect(stream.requests()).To(BeEmpty())

		session.s2sBeginTurn(2.0) // forwards from 1.7 s: 1.3 s of the buffer
		Expect(framesBytes(stream)).To(Equal(int(1.3 * rate * 2)))

		session.s2sOnAppendLocked(make([]byte, 640))
		Expect(framesBytes(stream)).To(Equal(int(1.3*rate*2) + 640))

		session.s2sEndTurn()
		Expect(isControl(stream.requests()[len(stream.requests())-1], "input_audio_buffer.commit")).To(BeTrue())
		session.s2sOnAppendLocked(make([]byte, 640))
		Expect(framesBytes(stream)).To(Equal(int(1.3*rate*2)+640), "forwarding stops at the commit")
	})

	It("holds manual-commit audio back until the commit", func() {
		stream := newFakeS2SStream()
		session, _, conv := newHarness(stream, baseModel())
		session.TurnDetection = nil
		chunk := make([]byte, 640)
		session.AudioBufferLock.Lock()
		session.InputAudioBuffer = append(session.InputAudioBuffer, chunk...)
		session.s2sOnAppendLocked(chunk)
		session.AudioBufferLock.Unlock()
		Expect(stream.requests()).To(BeEmpty(), "idle audio must not reach the model's context")

		session.s2sCommitAudio(chunk, conv)
		Expect(framesBytes(stream)).To(Equal(640))
		Expect(stream.controls()).To(Equal([]string{"input_audio_buffer.commit"}))
	})

	It("discards cleared manual-commit audio for good", func() {
		stream := newFakeS2SStream()
		session, tr, conv := newHarness(stream, baseModel())
		session.TurnDetection = nil
		session.AudioBufferLock.Lock()
		session.InputAudioBuffer = make([]byte, 640)
		session.s2sOnAppendLocked(make([]byte, 640))
		session.AudioBufferLock.Unlock()

		clearInputAudio(session)
		commitClientAudio(session, conv, tr)
		session.respSink.wait()
		Expect(framesBytes(stream)).To(BeZero())
	})

	It("commits uncommitted audio before a response.create asks for an answer", func() {
		stream := newFakeS2SStream()
		stream.script = replyScript
		session, tr, conv := newHarness(stream, baseModel())
		session.TurnDetection = nil
		session.InputAudioBuffer = make([]byte, 640)
		Expect(session.hasBufferedInput()).To(BeTrue())

		commitClientAudio(session, conv, tr)
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()

		Expect(session.hasBufferedInput()).To(BeFalse())
		Expect(stream.controls()).To(Equal([]string{"input_audio_buffer.commit", "response.create"}))
	})

	It("keeps a text item ahead of the audio committed after it", func() {
		stream := newFakeS2SStream()
		session, _, conv := newHarness(stream, baseModel())
		session.TurnDetection = nil
		conv.Items = append(conv.Items, &types.MessageItemUnion{User: &types.MessageItemUser{
			ID:      "t1",
			Content: []types.MessageContentInput{{Type: types.MessageContentTypeInputText, Text: "hello"}},
		}})

		session.s2sCommitAudio(make([]byte, 640), conv)

		var kinds []string
		for _, r := range stream.requests() {
			switch {
			case r.GetFrame() != nil:
				kinds = append(kinds, "frame")
			case r.GetControl() != nil:
				kinds = append(kinds, r.GetControl().GetEvent())
			}
		}
		Expect(kinds).To(Equal([]string{"conversation.item.create", "frame", "input_audio_buffer.commit"}))
	})

	It("resamples a committed turn to the backend input rate", func() {
		stream := newFakeS2SStream()
		session, _, conv := newHarness(stream, baseModel())
		session.InputSampleRate = 24000
		session.s2sCommitAudio(make([]byte, 24000*2/10), conv) // 100 ms at 24 kHz
		Expect(framesBytes(stream)).To(BeNumerically("~", rate*2/10, 8))
	})

	It("reports an open VAD turn so a tool result can be refused", func() {
		stream := newFakeS2SStream()
		session, _, _ := newHarness(stream, baseModel())
		Expect(session.s2sUserTurnOpen()).To(BeFalse())
		session.s2sBeginTurn(0)
		Expect(session.s2sUserTurnOpen()).To(BeTrue())
		session.s2sEndTurn()
		Expect(session.s2sUserTurnOpen()).To(BeFalse())
	})

	It("calls onDead when the stream ends outside a response", func() {
		stream := newFakeS2SStream()
		st := newS2SSession(stream, nil, nil)
		got := make(chan error, 1)
		st.setOnDead(func(err error) { got <- err })
		stream.Close()
		Eventually(got).Should(Receive())
		st.close()
	})

	It("does not call onDead for a deliberate close", func() {
		stream := newFakeS2SStream()
		st := newS2SSession(stream, nil, nil)
		got := make(chan error, 1)
		st.setOnDead(func(err error) { got <- err })
		st.close()
		Consistently(got, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("surfaces the real status when Send fails with a bare EOF", func() {
		stream := &eofStream{status: status.Error(codes.Unimplemented, "no such method"), failed: make(chan struct{})}
		st := newS2SSession(stream, nil, nil)
		DeferCleanup(st.close)
		err := st.open(context.Background(), s2sConfig{})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(errors.Unwrap(err))).To(Equal(codes.Unimplemented))
	})

	It("reports a backend error outside a response", func() {
		stream := newFakeS2SStream()
		got := make(chan string, 1)
		st := newS2SSession(stream, nil, func(typ, msg string) { got <- typ + ": " + msg })
		DeferCleanup(st.close)
		stream.push("error", nil, map[string]any{"type": "invalid_request_error", "message": "instructions cannot change"})
		Eventually(got).Should(Receive(Equal("invalid_request_error: instructions cannot change")))
	})

	Describe("prepareRealtimeConfig for any-to-any models", func() {
		yes := true
		newCfg := func() *config.ModelConfig {
			return &config.ModelConfig{Name: "s2s", Pipeline: config.Pipeline{AnyToAny: &yes}}
		}
		It("fills llm and transcription, leaves VAD and TTS empty", func() {
			cfg := newCfg()
			_, _, ok := prepareRealtimeConfig(cfg)
			Expect(ok).To(BeTrue())
			Expect(cfg.Pipeline.LLM).To(Equal("s2s"))
			Expect(cfg.Pipeline.Transcription).To(Equal("s2s"))
			Expect(cfg.Pipeline.VAD).To(BeEmpty())
			Expect(cfg.Pipeline.TTS).To(BeEmpty())
		})
		It("keeps a configured VAD and transcription model", func() {
			cfg := newCfg()
			cfg.Pipeline.VAD, cfg.Pipeline.Transcription = "silero", "asr"
			_, _, ok := prepareRealtimeConfig(cfg)
			Expect(ok).To(BeTrue())
			Expect(cfg.Pipeline.VAD).To(Equal("silero"))
			Expect(cfg.Pipeline.Transcription).To(Equal("asr"))
		})
		DescribeTable("rejects features that need a separate LLM call",
			func(mutate func(*config.ModelConfig), want string) {
				cfg := newCfg()
				mutate(cfg)
				code, msg, ok := prepareRealtimeConfig(cfg)
				Expect(ok).To(BeFalse())
				Expect(code).To(Equal("invalid_pipeline"))
				Expect(msg).To(ContainSubstring(want))
			},
			Entry("classifier", func(c *config.ModelConfig) { c.Pipeline.Classifier = &config.PipelineClassifier{Enabled: true} }, "classifier"),
			Entry("compaction", func(c *config.ModelConfig) { c.Pipeline.Compaction = &config.PipelineCompaction{Enabled: true} }, "compaction"),
			Entry("streaming llm", func(c *config.ModelConfig) { c.Pipeline.Streaming.LLM = &yes }, "streaming.llm"),
			Entry("voice recognition", func(c *config.ModelConfig) {
				c.Pipeline.VoiceRecognition = &config.PipelineVoiceRecognition{Model: "spk"}
			}, "voice_recognition"),
			Entry("sound detection", func(c *config.ModelConfig) { c.Pipeline.SoundDetection = "ced" }, "sound_detection"),
		)
	})

	It("returns errS2SOnly for chained-pipeline operations", func() {
		m := &anyToAnyModel{}
		_, err := m.Predict(context.Background(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		Expect(errors.Is(err, errS2SOnly)).To(BeTrue())
		_, _, err = m.TTS(context.Background(), "x", "", "")
		Expect(errors.Is(err, errS2SOnly)).To(BeTrue())
		_, err = m.VAD(context.Background(), &schema.VADRequest{})
		Expect(err).To(HaveOccurred(), "no VAD model configured")
	})
})

func countCancels(f *fakeS2SStream) int {
	n := 0
	for _, c := range f.controls() {
		if c == "response.cancel" {
			n++
		}
	}
	return n
}

// framesBytes totals the PCM bytes of the frames sent so far.
func framesBytes(f *fakeS2SStream) int {
	n := 0
	for _, r := range f.requests() {
		if fr := r.GetFrame(); fr != nil {
			n += len(fr.GetPcm())
		}
	}
	return n
}
