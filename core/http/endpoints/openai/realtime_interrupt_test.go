// SPDX-License-Identifier: MIT
package openai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("realtime interrupt_response", func() {
	decode := func(mode, setting string) *types.TurnDetectionUnion {
		var td types.TurnDetectionUnion
		Expect(json.Unmarshal([]byte(`{"type":"`+mode+`"`+setting+`}`), &td)).To(Succeed())
		return &td
	}
	holdResponse := func(s *Session) context.Context {
		started := make(chan context.Context, 1)
		s.respSink.issue(context.Background(), respcoord.SourceClient, func(ctx context.Context) {
			started <- ctx
			<-ctx.Done()
		})
		DeferCleanup(s.respSink.shutdown)
		var ctx context.Context
		Eventually(started).Should(Receive(&ctx))
		return ctx
	}

	DescribeTable("controls cancellation at speech onset",
		func(mode, setting string, interrupts bool) {
			s := &Session{TurnDetection: decode(mode, setting), InputSampleRate: 16000,
				InputAudioBuffer: make([]byte, 32000), ModelConfig: &config.ModelConfig{},
				ModelInterface: &fakeModel{vadSegments: []schema.VADSegment{{Start: 0.1}}}, respSink: newResponseSink()}
			ctx := holdResponse(s)
			tr := &fakeTransport{}
			sink := newTurnSink(s, &Conversation{}, tr, newLiveTurnState(s, tr), context.Background(), time.Now())
			vadTick(sink, 0.5)
			Expect(tr.countEvents(types.ServerEventTypeInputAudioBufferSpeechStarted)).To(Equal(1))
			Expect(ctx.Err() != nil).To(Equal(interrupts))
		},
		Entry("server false", "server_vad", `,"interrupt_response":false`, false),
		Entry("semantic false", "semantic_vad", `,"interrupt_response":false`, false),
		Entry("server true", "server_vad", `,"interrupt_response":true`, true),
		Entry("semantic true", "semantic_vad", `,"interrupt_response":true`, true),
		Entry("server omitted", "server_vad", "", true),
		Entry("semantic omitted", "semantic_vad", "", true),
	)

	DescribeTable("preserves the active response and commits overlapping speech",
		func(mode string) {
			s := &Session{TurnDetection: decode(mode, `,"interrupt_response":false`),
				InputAudioTranscription: &types.AudioTranscription{},
				ModelInterface:          &fakeModel{cfg: &config.ModelConfig{}, transcribeFinal: &schema.TranscriptionResult{Text: "next turn"}},
				ModelConfig:             &config.ModelConfig{}, respSink: newResponseSink()}
			ctx := holdResponse(s)
			conv, tr := &Conversation{}, &fakeTransport{}
			committed := make(chan struct{})
			s.issueCommit(context.Background(), respcoord.SourceVAD, func(turnCtx context.Context, slot *commitSlot) {
				commitUtteranceWithTranscript(turnCtx, []byte{1, 2, 3, 4}, nil, nil, "", s, conv, tr, slot)
				close(committed)
			})
			Eventually(committed).Should(BeClosed())
			Expect(ctx.Err()).NotTo(HaveOccurred())
			Expect(tr.countEvents(types.ServerEventTypeResponseCreated)).To(BeZero())
			Expect(tr.countEvents(types.ServerEventTypeConversationItemInputAudioTranscriptionCompleted)).To(Equal(1))
			conv.Lock.Lock()
			defer conv.Lock.Unlock()
			Expect(conv.Items).To(HaveLen(1))
			Expect(conv.Items[0].User.Content[0].Transcript).To(Equal("next turn"))
		},
		Entry("server", "server_vad"), Entry("semantic", "semantic_vad"),
	)

	It("applies session updates to interruption and echoes false", func() {
		s := &Session{TurnDetection: defaultTurnDetection(nil), ModelConfig: &config.ModelConfig{}}
		var update types.SessionUpdateEvent
		Expect(json.Unmarshal([]byte(`{"type":"session.update","session":{"type":"realtime","audio":{"input":{"turn_detection":{"type":"semantic_vad","interrupt_response":false}}}}}`), &update)).To(Succeed())
		Expect(updateSession(s, &update.Session, nil, nil, nil, nil, nil, nil)).To(Succeed())
		Expect(s.interruptResponseEnabled()).To(BeFalse())
		raw, err := json.Marshal(s.ToServer())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"interrupt_response":false`))
		Expect(json.Unmarshal([]byte(`{"type":"session.update","session":{"type":"realtime","instructions":"Keep talking"}}`), &update)).To(Succeed())
		Expect(updateSession(s, &update.Session, nil, nil, nil, nil, nil, nil)).To(Succeed())
		Expect(s.interruptResponseEnabled()).To(BeFalse())
		s.TurnDetection = decode("server_vad", `,"interrupt_response":true`)
		Expect(s.interruptResponseEnabled()).To(BeTrue())
	})

	DescribeTable("starts commits when idle and retains explicit client cancellation",
		func(source respcoord.Source) {
			s := &Session{TurnDetection: decode("server_vad", `,"interrupt_response":false`), respSink: newResponseSink()}
			DeferCleanup(s.respSink.shutdown)
			started := make(chan context.Context, 1)
			s.issueCommit(context.Background(), source, func(ctx context.Context, slot *commitSlot) {
				defer close(slot.done)
				started <- ctx
				<-ctx.Done()
			})
			var ctx context.Context
			Eventually(started).Should(Receive(&ctx))
			Expect(ctx.Err()).NotTo(HaveOccurred())
			s.respSink.cancel(respcoord.SourceClient)
			Expect(ctx.Err()).To(MatchError(context.Canceled))
		}, Entry("VAD", respcoord.SourceVAD), Entry("client", respcoord.SourceClient),
	)

	It("keeps explicit client supersession when interruption is disabled", func() {
		s := &Session{TurnDetection: decode("server_vad", `,"interrupt_response":false`), respSink: newResponseSink()}
		ctx := holdResponse(s)
		done := make(chan struct{})
		s.issueCommit(context.Background(), respcoord.SourceClient, func(_ context.Context, slot *commitSlot) {
			close(slot.done)
			close(done)
		})
		Eventually(done).Should(BeClosed())
		Expect(ctx.Err()).To(MatchError(context.Canceled))
	})

	DescribeTable("echoes an explicit false through JSON",
		func(mode string) {
			raw, err := json.Marshal(decode(mode, `,"interrupt_response":false`))
			Expect(err).NotTo(HaveOccurred())
			var fields map[string]any
			Expect(json.Unmarshal(raw, &fields)).To(Succeed())
			Expect(fields).To(HaveKeyWithValue("interrupt_response", false))
		}, Entry("server", "server_vad"), Entry("semantic", "semantic_vad"),
	)
})
