package openai

import (
	"context"
	"encoding/base64"
	"sync"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/pkg/sound"
)

type liveSpeechModel interface {
	SupportsTTSLive() bool
	TTSLive(context.Context, <-chan string, string, string, func([]byte, int) error) error
}

// liveSpeechPipeline bounds pending text and couples producer lifetime to the
// audio sink: cancellation/error wakes producers and the native input callback.
type liveSpeechPipeline struct {
	text   chan string
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	stop   <-chan struct{}
	audio  []byte
	err    error
}

func newLiveSpeechPipeline(ctx context.Context, run func(context.Context, <-chan string) ([]byte, error)) *liveSpeechPipeline {
	ctx, cancel := context.WithCancel(ctx)
	p := &liveSpeechPipeline{text: make(chan string, 8), done: make(chan struct{}), stop: ctx.Done(), cancel: cancel}
	go func() { defer close(p.done); defer cancel(); p.audio, p.err = run(ctx, p.text) }()
	return p
}
func (p *liveSpeechPipeline) push(text string) bool {
	select {
	case <-p.stop:
		return false
	default:
	}
	select {
	case <-p.stop:
		return false
	case p.text <- text:
		return true
	}
}
func (p *liveSpeechPipeline) finish() ([]byte, error) {
	p.once.Do(func() { close(p.text) })
	<-p.done
	return p.audio, p.err
}
func (p *liveSpeechPipeline) abort() { p.cancel(); _, _ = p.finish() }

func emitLiveSpeech(ctx context.Context, model liveSpeechModel, text <-chan string, t Transport, session *Session, responseID, itemID string) ([]byte, error) {
	var audio []byte
	language := ""
	if session.InputAudioTranscription != nil {
		language = session.InputAudioTranscription.Language
	}
	_, webRTC := t.(*WebRTCTransport)
	err := model.TTSLive(ctx, text, session.Voice, language, func(pcm []byte, rate int) error {
		if len(pcm) == 0 {
			return nil
		}
		if err := t.SendAudio(ctx, pcm, rate); err != nil {
			return err
		}
		if webRTC {
			return nil
		}
		if rate != 0 && rate != session.OutputSampleRate {
			pcm = sound.Int16toBytesLE(sound.ResampleInt16(sound.BytesToInt16sLE(pcm), rate, session.OutputSampleRate))
		}
		audio = append(audio, pcm...)
		return t.SendEvent(types.ResponseOutputAudioDeltaEvent{ResponseID: responseID, ItemID: itemID, OutputIndex: 0, ContentIndex: 0, Delta: base64.StdEncoding.EncodeToString(pcm)})
	})
	return audio, err
}

// emitBufferedSpeech synthesizes independent complete clauses in order. Each
// clause can stream its own backend audio; this is not a native live session.
func emitBufferedSpeech(ctx context.Context, input <-chan string, t Transport, session *Session, responseID, itemID string) ([]byte, error) {
	var audio []byte
	for {
		if err := ctx.Err(); err != nil {
			return audio, err
		}
		select {
		case <-ctx.Done():
			return audio, ctx.Err()
		case clause, ok := <-input:
			if !ok {
				return audio, nil
			}
			if err := ctx.Err(); err != nil {
				return audio, err
			}
			chunk, err := emitSpeech(ctx, t, session, responseID, itemID, clause)
			audio = append(audio, chunk...)
			if err != nil {
				return audio, err
			}
		}
	}
}
