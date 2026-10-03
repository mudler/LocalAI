package openai

import (
	"context"
	"sync/atomic"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

// These specs are the regression tests for the two schedules from the review
// of issue #12445 (PR fix/realtime-bargein-vad-commit-cancel):
//
//  1. teardown during an in-flight transcription: the transcription must be
//     cancelled with the session — a detached context (WithoutCancel) would
//     keep the backend job (and the respSink.shutdown join) blocked until the
//     backend was explicitly released;
//  2. out-of-order completion: with the first transcription held and the
//     second completed, the conversation must NOT become [second first] and
//     the second response must not see only [second].
//
// They drive the REAL commit path (commitUtteranceWithTranscript +
// responseSink/respcoord) with a transcription double whose wait is
// controlled by the spec and which honours context cancellation like a real
// backend job.

// heldTranscribeModel scripts the commit-path transcription: call n blocks
// until release (or ctx cancellation) and then returns texts[n-1]. started /
// finished announce entry/exit of call n so specs can sequence the schedules.
type heldTranscribeModel struct {
	*fakeModel
	n         int32
	texts     []string
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
			started:  make(chan int32, 4),
			finished: make(chan int32, 4),
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
		}
		session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
		return session
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

		done := make(chan struct{})
		go func() {
			commitUtteranceWithTranscript(context.Background(), utt, nil, nil, "", session, conv, tr, session.nextCommitSlot())
			close(done)
		}()

		// The first transcription is in flight (held by the double).
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Teardown cancels the session context (conncoord does this BEFORE
		// respSink.shutdown joins the response goroutines). The commit must
		// return promptly — not wait for the backend to finish the job.
		session.sessionCancel()

		Eventually(done, "2s").Should(BeClosed())
		Expect(userTexts(conv)).To(BeEmpty(), "a cancelled transcription commits no user item")
	})

	It("commits user items in speech order even when transcriptions finish out of order", func() {
		m := newModel("first", "second")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}
		var wg sync.WaitGroup

		// Commit 1 (issued first = speech order) — its transcription is held.
		wg.Add(1)
		go func() {
			defer wg.Done()
			commitUtteranceWithTranscript(context.Background(), utt, nil, nil, "", session, conv, tr, session.nextCommitSlot())
		}()
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Commit 2 (issued second) — its transcription finishes FIRST.
		wg.Add(1)
		go func() {
			defer wg.Done()
			commitUtteranceWithTranscript(context.Background(), utt, nil, nil, "", session, conv, tr, session.nextCommitSlot())
		}()
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))

		// Now release commit 1; both commits complete.
		close(m.release)
		wg.Wait()

		// The conversation is in speech order — NOT [second first].
		Expect(userTexts(conv)).To(Equal([]string{"first", "second"}))
		// The second response's LLM context saw both user turns, in order.
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "second"}))
	})

	It("keeps the user item when a barge-in cancels the turn context during transcription", func() {
		m := newModel("first", "second")
		session := newSession(m)
		tr := &fakeTransport{}
		conv := &Conversation{}
		var wg sync.WaitGroup

		ctx1, cancel1 := context.WithCancel(context.Background())
		wg.Add(1)
		go func() {
			defer wg.Done()
			commitUtteranceWithTranscript(ctx1, utt, nil, nil, "", session, conv, tr, session.nextCommitSlot())
		}()
		Eventually(m.started, "2s").Should(Receive(Equal(int32(1))))

		// Barge-in: the new speech onset cancels the in-flight turn's response
		// context while its transcription is still running.
		cancel1()

		wg.Add(1)
		go func() {
			defer wg.Done()
			commitUtteranceWithTranscript(context.Background(), utt, nil, nil, "", session, conv, tr, session.nextCommitSlot())
		}()
		Eventually(m.finished, "2s").Should(Receive(Equal(int32(2))))

		// The held transcription of the barged-in turn survives the barge-in
		// (session context) and completes.
		close(m.release)
		wg.Wait()

		// The barged-in turn's user item is committed — the LLM context keeps
		// the full user input — but it gets NO response of its own.
		Expect(userTexts(conv)).To(Equal([]string{"first", "second"}))
		Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(Equal(1))
		// The response (for the barge-in turn) was built on the complete,
		// ordered history.
		Expect(userMsgTexts(m.lastMessages)).To(Equal([]string{"first", "second"}))
	})
})
