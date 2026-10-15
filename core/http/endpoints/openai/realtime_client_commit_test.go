package openai

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

// Regression tests for the client-commit LLM/STT race. With client-driven
// turns (turn_detection null, e.g. a client VAD / speaker gate) the client
// sends input_audio_buffer.commit and response.create back-to-back. Without the
// fix, response.create triggers the LLM immediately while the commit's STT is
// still in flight, so the LLM (fast) wins the race and answers an EMPTY
// transcript; the response on the real transcript is then discarded ("skipping
// response: turn context cancelled during transcription"). The fix makes the
// response wait for the in-flight commit's STT (and user-item append) before
// the LLM starts.
var _ = Describe("realtime client-commit LLM/STT ordering", func() {
	utt := []byte{1, 2, 3, 4}

	newModel := func(texts ...string) *heldTranscribeModel {
		return &heldTranscribeModel{
			fakeModel: &fakeModel{
				cfg:             &config.ModelConfig{},
				predictTokens:   []string{"ok"},
				predictResp:     backend.LLMResponse{Response: "ok"},
				ttsStreamChunks: [][]byte{{1}},
				ttsStreamRate:   24000,
			},
			texts:    texts,
			release:  make(chan struct{}),
			started:  make(chan int32, 8),
			finished: make(chan int32, 8),
		}
	}

	newSession := func(m *heldTranscribeModel) *Session {
		on := true
		session := &Session{
			OutputSampleRate:        24000,
			InputAudioTranscription: &types.AudioTranscription{},
			ModelInterface:          m,
			ModelConfig: &config.ModelConfig{
				Pipeline: config.Pipeline{Streaming: config.PipelineStreaming{LLM: &on, TTS: &on}},
			},
			respSink: newResponseSink(),
		}
		session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
		return session
	}

	userMsgTextsOf := func(m *heldTranscribeModel) []string {
		var out []string
		for _, msg := range m.lastMessages {
			if msg.Role == string(types.MessageRoleUser) {
				out = append(out, msg.StringContent)
			}
		}
		return out
	}

	It("does not start the LLM until the client commit's STT is complete", func() {
		m := newModel("hello there")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// Client commit — its transcription is held by the double.
		session.issueClientCommit(utt, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Back-to-back response.create (supersedes the commit's own response).
		// With the fix it WAITS for the held STT: no response is created while
		// the transcription is in flight.
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		Consistently(func() int {
			return tr.countEvents(types.ServerEventTypeResponseCreated)
		}, 200*time.Millisecond).Should(BeZero())

		// Release the STT; the commit appends its user item and the waiting
		// response now starts on the real transcript.
		close(m.release)
		session.respSink.wait()

		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		// The LLM saw the user's transcript, not an empty one.
		Expect(userMsgTextsOf(m)).To(Equal([]string{"hello there"}))
	})

	It("still responds to a standalone response.create with no commit in flight", func() {
		m := newModel()
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// No commit in flight: the response must not wait (no deadlock).
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()

		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
	})
	// responseDones returns the response.done payloads sent so far.
	responseDones := func(tr *fakeTransport) []types.Response {
		var out []types.Response
		for _, ev := range tr.events() {
			if d, ok := ev.(types.ResponseDoneEvent); ok {
				out = append(out, d.Response)
			}
		}
		return out
	}

	// predictCalled reports whether the response LLM ran: Predict records the
	// prompt it was given.
	predictCalled := func(m *heldTranscribeModel) bool { return m.lastMessages != nil }

	DescribeTable("does not run the LLM when the commit produced no user item",
		func(texts []string, errs []error) {
			m := newModel(texts...)
			m.errs = errs
			session := newSession(m)
			tr := &fakeTransport{}
			conv := &Conversation{}

			session.issueClientCommit(utt, conv, tr)
			Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))
			session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
			close(m.release)
			session.respSink.wait()

			Expect(predictCalled(m)).To(BeFalse())
			Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
			dones := responseDones(tr)
			Expect(dones).To(HaveLen(1))
			Expect(dones[0].Status).To(Equal(types.ResponseStatusFailed))
			Expect(dones[0].StatusDetails).NotTo(BeNil())
			Expect(dones[0].StatusDetails.Error).NotTo(BeNil())
			Expect(dones[0].StatusDetails.Error.Code).To(Equal("input_not_committed"))
			conv.Lock.Lock()
			defer conv.Lock.Unlock()
			Expect(conv.Items).To(BeEmpty())
		},
		Entry("STT error", []string{"x"}, []error{errors.New("stt down")}),
		Entry("empty transcript", []string{"   "}, nil),
	)

	It("closes the response when it is cancelled while waiting for the commit", func() {
		m := newModel("hello there")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		session.issueClientCommit(utt, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))
		session.issueClientResponse(types.ResponseCreateParams{Metadata: map[string]string{"k": "v"}}, conv, tr)
		Consistently(func() int { return len(responseDones(tr)) }, 100*time.Millisecond).Should(BeZero())

		session.respSink.cancel(respcoord.SourceClient)
		Eventually(func() []types.Response { return responseDones(tr) }, "2s").Should(HaveLen(1))
		dones := responseDones(tr)
		Expect(dones[0].Status).To(Equal(types.ResponseStatusCancelled))
		Expect(dones[0].Metadata).To(HaveKeyWithValue("k", "v"))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		Expect(predictCalled(m)).To(BeFalse())

		close(m.release)
		session.respSink.wait()
		Expect(responseDones(tr)).To(HaveLen(1))
	})

	It("closes the response when a newer response supersedes it during the wait", func() {
		m := newModel("hello there")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		session.issueClientCommit(utt, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		Eventually(func() []types.Response { return responseDones(tr) }, "2s").Should(HaveLen(1))
		Expect(responseDones(tr)[0].Status).To(Equal(types.ResponseStatusCancelled))

		close(m.release)
		session.respSink.wait()
		dones := responseDones(tr)
		Expect(dones).To(HaveLen(2))
		Expect(dones[1].Status).To(Equal(types.ResponseStatusCompleted))
	})

	It("orders commit, commit, response: the LLM sees both turns in speech order", func() {
		m := newModel("first turn", "second turn")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		session.issueClientCommit(utt, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))
		session.issueClientCommit(utt, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(2))))
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)

		// The second transcription finishes first, but the response must wait
		// for the whole chain.
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))
		Consistently(func() int {
			return tr.countEvents(types.ServerEventTypeResponseCreated)
		}, 200*time.Millisecond).Should(BeZero())

		close(m.release)
		session.respSink.wait()

		Expect(userMsgTextsOf(m)).To(Equal([]string{"first turn", "second turn"}))
		dones := responseDones(tr)
		Expect(dones).To(HaveLen(1))
		Expect(dones[0].Status).To(Equal(types.ResponseStatusCompleted))
	})

	It("does not fail a standalone response after an earlier commit failed", func() {
		m := newModel("x")
		m.errs = []error{errors.New("stt down")}
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		close(m.release)
		session.issueClientCommit(utt, conv, tr)
		session.respSink.wait()
		Expect(predictCalled(m)).To(BeFalse())

		// The failed commit is long finished: a later response.create is not
		// about it and runs on the history as it is.
		session.issueClientResponse(types.ResponseCreateParams{}, conv, tr)
		session.respSink.wait()
		Expect(predictCalled(m)).To(BeTrue())
		dones := responseDones(tr)
		Expect(dones).To(HaveLen(1))
		Expect(dones[0].Status).To(Equal(types.ResponseStatusCompleted))
	})
})
