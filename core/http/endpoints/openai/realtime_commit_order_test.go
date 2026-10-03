package openai

import (
	"context"
	"errors"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

// These specs are the regression tests for the schedules from the review of
// issue #12445 (PR fix/realtime-bargein-vad-commit-cancel), v1–v3:
//
//  1. teardown during an in-flight transcription: the transcription must be
//     cancelled with the session — a detached context (WithoutCancel) would
//     keep the backend job (and the respSink.shutdown join) blocked until the
//     backend was explicitly released;
//  2. out-of-order completion: with the first transcription held and the
//     second completed, the conversation must NOT become [second first] and
//     the second response must not see only [second];
//  3a. a FAILED middle commit (transcription error) must not release the
//     third turn before the first one finishes — response history must not be
//     [third], final history must not be [third first];
//  3b. an EMPTY middle commit (empty transcript / gate rejection behave the
//     same) must not release the third turn early either;
//  3c. the two producers (VAD CommitTurn, client input_audio_buffer.commit)
//     must not be able to reserve a slot and issue it in opposite order —
//     supersession may only cancel a response issued LATER in speech order.
//
// They drive the REAL issue path — Session.issueCommit into the real
// responseSink/respcoord (so coordinator supersession and the spawned
// response goroutines are exercised, not a bare
// commitUtteranceWithTranscript call) — with a transcription double whose
// wait is controlled by the spec and which honours context cancellation like
// a real backend job.

// heldTranscribeModel scripts the commit-path transcription: call n blocks
// until release (or ctx cancellation) and then returns texts[n-1] (or
// errs[n-1], when set). started / finished announce entry/exit of call n so
// specs can sequence the schedules.
type heldTranscribeModel struct {
	*fakeModel
	n         int32
	texts     []string
	errs      []error
	release   chan struct{}
	started   chan int32
	finished  chan int32
}

func (m *heldTranscribeModel) Transcribe(ctx context.Context, _ string, _ string, _ bool, _ bool, _ string) (*schema.TranscriptionResult, error) {
	n := atomic.AddInt32(&m.n, 1)
	select {
	case m.started <- n:
	default:
	}
	if n == 1 {
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if int(n) <= len(m.errs) && m.errs[n-1] != nil {
		select {
		case m.finished <- n:
		default:
		}
		return nil, m.errs[n-1]
	}
	select {
	case m.finished <- n:
	default:
	}
	return &schema.TranscriptionResult{Text: m.texts[n-1]}, nil
}

var _ = Describe("realtime commit order and transcription lifetime (issue #12445)", func() {
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

	// issue drives one commit through the REAL issue path: the shared
	// ordering boundary (slot claim + respSink.issue under one lock) and the
	// real respcoord supersession — NOT a direct
	// commitUtteranceWithTranscript call.
	issue := func(session *Session, parent context.Context, source respcoord.Source, conv *Conversation, tr *fakeTransport) {
		session.issueCommit(parent, source, func(ctx context.Context, slot *commitSlot) {
			commitUtteranceWithTranscript(ctx, utt, nil, nil, "", session, conv, tr, slot)
		})
	}

	userTexts := func(conv *Conversation) []string {
		var out []string
		conv.Lock.Lock()
		defer conv.Lock.Unlock()
		for _, it := range conv.Items {
			if it.User != nil && len(it.User.Content) > 0 {
				out = append(out, it.User.Content[0].Transcript)
			}
		}
		return out
	}

	userMsgTexts := func(msgs schema.Messages) []string {
		var out []string
		for _, msg := range msgs {
			if msg.Role == string(types.MessageRoleUser) {
				out = append(out, msg.StringContent)
			}
		}
		return out
	}

	It("cancels an in-flight transcription at teardown instead of outliving the session", func() {
		m := newModel("first")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		// The first transcription is in flight (held by the double).
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Teardown (conncoord): cancel the session context, then shut down the
		// response sink — which JOINS the response goroutines. The join must
		// return promptly: the transcription is cancelled with the session, not
		// allowed to outlive it (a WithoutCancel context would block here until
		// the backend was released).
		done := make(chan struct{})
		go func() {
			session.sessionCancel()
			session.respSink.shutdown()
			close(done)
		}()

		Eventually(done, "2s").Should(BeClosed())
		Expect(userTexts(conv)).To(BeEmpty(), "a cancelled transcription commits no user item")
	})

	It("commits user items in speech order when the first transcription finishes last", func() {
		m := newModel("first", "second")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// Commit 1 (issued first = speech order) — its transcription is held.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Commit 2 (issued second) supersedes commit 1's response, and its
		// transcription finishes FIRST.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))

		// Now release commit 1; both commit bodies complete.
		close(m.release)
		session.respSink.wait()

		// The conversation is in speech order — NOT [second first].
		Expect(userTexts(conv)).To(Equal([]string{"first", "second"}))
		// The superseded turn got no response of its own; the surviving
		// (second) response was built on the complete, ordered history.
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "second"}))
	})

	It("keeps the user item when a barge-in cancels the turn context during transcription", func() {
		m := newModel("first", "second")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// The first turn is in flight (transcription held).
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Barge-in: the new speech onset cancels the in-flight turn's response
		// context (turncoord: respSink.cancel(SourceVAD)) while its
		// transcription is still running.
		session.respSink.cancel(respcoord.SourceVAD)

		// The held transcription of the barged-in turn survives the barge-in
		// (session context) and completes; the turn commits its item but gets
		// no response of its own.
		close(m.release)
		session.respSink.wait()
		Expect(userTexts(conv)).To(Equal([]string{"first"}))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(BeZero())

		// The barge-in turn then commits and responds on the complete history.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		session.respSink.wait()

		Expect(userTexts(conv)).To(Equal([]string{"first", "second"}))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		// The response was built on the complete, ordered history.
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "second"}))
	})

	It("a failed middle commit does not release later turns before earlier ones finish", func() {
		m := newModel("first", "second", "third")
		m.errs = []error{nil, errors.New("stt backend down"), nil}
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// Turn A (issued first) — its transcription is held.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Turn B (issued second, supersedes A) — its transcription FAILS.
		// Without the release gate, B would close its slot on the error return
		// without waiting for A, releasing turn C early.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))

		// Turn C (issued third, supersedes B) — its transcription completes...
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(3))))
		// ...but C's item append must still wait for A (through B's slot), so
		// no response has been created yet.
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(BeZero())

		// Release A; everything drains in order.
		close(m.release)
		session.respSink.wait()

		// B contributes no item; C's response saw the earlier turn — NOT
		// [third] — and the conversation is in speech order — NOT
		// [third first].
		Expect(userTexts(conv)).To(Equal([]string{"first", "third"}))
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "third"}))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
	})

	It("an empty middle commit does not release later turns before earlier ones finish", func() {
		m := newModel("first", "", "third")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// Turn A (issued first) — its transcription is held.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Turn B (issued second, supersedes A) — its transcription returns an
		// EMPTY transcript: no item, no response, early return.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))

		// Turn C (issued third) — completes, but must still wait for A.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(3))))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(BeZero())

		close(m.release)
		session.respSink.wait()

		Expect(userTexts(conv)).To(Equal([]string{"first", "third"}))
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "third"}))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
	})

	It("orders commits across the VAD and client producers (VAD first)", func() {
		m := newModel("first", "second")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// VAD issues turn A (held). The client then issues turn B — through
		// the same shared boundary, so B can only be issued AFTER A's slot is
		// claimed and A's response issued; B supersedes A (the later issue
		// wins), never the other way around.
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))
		issue(session, context.Background(), respcoord.SourceClient, conv, tr)
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))

		close(m.release)
		session.respSink.wait()

		// A's item survives (committed, no response); B's response is built on
		// the complete, ordered history.
		Expect(userTexts(conv)).To(Equal([]string{"first", "second"}))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "second"}))
	})

	It("orders commits across the VAD and client producers (client first)", func() {
		m := newModel("first", "second")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}

		// Client issues turn A (held); VAD then issues turn B — same shared
		// boundary, opposite producer order.
		issue(session, context.Background(), respcoord.SourceClient, conv, tr)
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))
		issue(session, context.Background(), respcoord.SourceVAD, conv, tr)
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))

		close(m.release)
		session.respSink.wait()

		Expect(userTexts(conv)).To(Equal([]string{"first", "second"}))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "second"}))
	})
})
