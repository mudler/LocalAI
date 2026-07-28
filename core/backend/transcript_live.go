package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sound"
	"github.com/mudler/xlog"
)

// LiveTranscriptionEvent is one streamed event from a live (bidirectional)
// transcription session. Delta/Eou/Eob/Words arrive as the user speaks; Final
// is set exactly once, on the terminal event after Close flushes the decode
// tail. Eou means the model judged the user yielded the turn; Eob means a
// backchannel ("uh-huh") ended — callers must NOT treat Eob as a turn
// boundary.
type LiveTranscriptionEvent struct {
	Delta string
	Eou   bool
	Eob   bool
	Words []schema.TranscriptionWord
	Final *schema.TranscriptionResult
}

// LiveTranscriptionSession is a handle on an open live transcription stream.
// Feed pushes 16 kHz mono float PCM; Close signals end-of-audio, waits for
// the backend's terminal Final event to be delivered, and releases the
// stream.
type LiveTranscriptionSession interface {
	Feed(pcm []float32) error
	Close() error
}

// liveCloseDrainTimeout bounds how long Close waits for the backend to flush
// the decode tail before force-cancelling the stream. Finalize is one short
// engine call; seconds here means the backend is wedged.
const liveCloseDrainTimeout = 10 * time.Second

type liveTranscriptionSession struct {
	stream    grpcPkg.AudioTranscriptionLiveClient
	cancel    context.CancelFunc
	recvDone  chan struct{}
	recvErr   error // written by the recv goroutine before recvDone closes
	closeOnce sync.Once
	closeErr  error
	trace     *liveTraceState // nil when tracing was disabled at open
}

func (s *liveTranscriptionSession) Feed(pcm []float32) error {
	s.trace.addPCM(pcm)
	return s.stream.Send(&proto.TranscriptLiveRequest{
		Payload: &proto.TranscriptLiveRequest_Audio{Audio: &proto.TranscriptLiveAudio{Pcm: pcm}},
	})
}

func (s *liveTranscriptionSession) Close() error {
	s.closeOnce.Do(func() {
		err := s.stream.CloseSend()
		select {
		case <-s.recvDone:
		case <-time.After(liveCloseDrainTimeout):
			xlog.Warn("live transcription: backend did not finalize in time; cancelling stream")
			s.cancel()
			<-s.recvDone
		}
		s.cancel()
		if err == nil {
			err = s.recvErr
		}
		s.closeErr = err
		s.trace.record(err)
	})
	return s.closeErr
}

// liveSampleRate is the PCM rate of a live transcription session, fixed by
// the session config sent in ModelTranscriptionLive.
const liveSampleRate = 16000

// liveTraceState accumulates what the per-turn backend trace needs while a
// live session runs: a bounded copy of the fed PCM for the audio snippet,
// the decode outputs, and timing. One trace is recorded at Close — the live
// path never touches the unary transcription wrapper, so without this a
// streaming-only pipeline produced no transcription traces at all. Feed and
// the recv goroutine run concurrently; mu guards the accumulators.
type liveTraceState struct {
	appConfig *config.ApplicationConfig
	modelName string
	backend   string
	language  string
	started   time.Time

	mu          sync.Mutex
	pcm         []byte // first trace.MaxSnippetSeconds of fed audio, int16 LE
	fedSamples  int    // ALL samples fed, beyond the snippet cap
	deltaEvents int
	eouEvents   int
	eobEvents   int
	finalText   string
}

func newLiveTraceState(modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, language string) *liveTraceState {
	if !appConfig.EnableTracing {
		return nil
	}
	trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
	return &liveTraceState{
		appConfig: appConfig,
		modelName: modelConfig.Name,
		backend:   modelConfig.Backend,
		language:  language,
		started:   time.Now(),
	}
}

func (ts *liveTraceState) addPCM(pcm []float32) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.fedSamples += len(pcm)
	maxBytes := trace.MaxSnippetSeconds * liveSampleRate * 2
	if room := (maxBytes - len(ts.pcm)) / 2; room > 0 {
		if len(pcm) > room {
			pcm = pcm[:room]
		}
		ts.pcm = append(ts.pcm, sound.Float32sToInt16LEBytes(pcm)...)
	}
}

func (ts *liveTraceState) observe(ev LiveTranscriptionEvent) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ev.Delta != "" {
		ts.deltaEvents++
	}
	if ev.Eou {
		ts.eouEvents++
	}
	if ev.Eob {
		ts.eobEvents++
	}
	if ev.Final != nil {
		ts.finalText = ev.Final.Text
	}
}

func (ts *liveTraceState) record(closeErr error) {
	if ts == nil || !ts.appConfig.EnableTracing {
		return
	}
	ts.mu.Lock()
	data := map[string]any{
		"source":       "live_stream",
		"language":     ts.language,
		"result_text":  ts.finalText,
		"eou_events":   ts.eouEvents,
		"eob_events":   ts.eobEvents,
		"delta_events": ts.deltaEvents,
	}
	if snippet := trace.AudioSnippetFromPCM(ts.pcm, liveSampleRate, ts.fedSamples*2, ts.appConfig.TracingMaxBodyBytes); snippet != nil {
		maps.Copy(data, snippet)
	}
	summary := "live -> " + ts.finalText
	ts.mu.Unlock()

	bt := trace.BackendTrace{
		Timestamp: ts.started,
		Duration:  time.Since(ts.started),
		Type:      trace.BackendTraceTranscription,
		ModelName: ts.modelName,
		Backend:   ts.backend,
		Summary:   trace.TruncateString(summary, 200),
		Data:      data,
	}
	if closeErr != nil {
		bt.Error = closeErr.Error()
	}
	trace.RecordBackendTrace(bt)
}

// ModelTranscriptionLive loads the transcription backend, opens the
// bidirectional AudioTranscriptionLive RPC, sends the session config, and
// BLOCKS until the backend's ready ack. A grpcerrors.
// IsLiveTranscriptionUnsupported error means the backend (or the loaded
// model) cannot do live transcription and the caller should degrade to the
// unary/file path. After a successful return, onEvent is invoked from a
// background goroutine — in order, one event at a time — for every response
// the backend streams, ending with the Final event triggered by Close.
func ModelTranscriptionLive(ctx context.Context, language string,
	ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig,
	onEvent func(LiveTranscriptionEvent)) (LiveTranscriptionSession, error) {

	transcriptionModel, err := loadTranscriptionModel(ctx, ml, modelConfig, appConfig)
	if err != nil {
		return nil, err
	}

	// The derived cancel out-lives this call inside the session: Close uses
	// it to unwind the stream (and, in embed mode, the server-side recv
	// pump, which only stops on send-close or context cancellation).
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := transcriptionModel.AudioTranscriptionLive(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}

	fail := func(err error) (LiveTranscriptionSession, error) {
		_ = stream.CloseSend()
		cancel()
		return nil, err
	}

	if err := stream.Send(&proto.TranscriptLiveRequest{
		Payload: &proto.TranscriptLiveRequest_Config{Config: &proto.TranscriptLiveConfig{
			Language:   language,
			SampleRate: liveSampleRate,
		}},
	}); err != nil {
		return fail(err)
	}

	// Ready-ack contract: the backend answers a successful open with a
	// {ready:true} response before any transcript data; unsupported
	// backends surface Unimplemented here instead.
	ack, err := stream.Recv()
	if err != nil {
		return fail(err)
	}
	if !ack.GetReady() {
		return fail(fmt.Errorf("live transcription: backend %q broke the ready-ack contract (first response carried data)", modelConfig.Backend))
	}

	s := &liveTranscriptionSession{
		stream:   stream,
		cancel:   cancel,
		recvDone: make(chan struct{}),
		trace:    newLiveTraceState(modelConfig, appConfig, language),
	}

	go func() {
		defer close(s.recvDone)
		for {
			resp, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && streamCtx.Err() == nil {
					xlog.Warn("live transcription stream ended unexpectedly", "error", err)
					s.recvErr = err
				}
				return
			}
			ev := liveEventFromProto(resp)
			if ev.Delta == "" && !ev.Eou && !ev.Eob && len(ev.Words) == 0 && ev.Final == nil {
				continue // duplicate ready ack / keep-alive: nothing to deliver
			}
			s.trace.observe(ev)
			onEvent(ev)
		}
	}()

	return s, nil
}

func liveEventFromProto(r *proto.TranscriptLiveResponse) LiveTranscriptionEvent {
	ev := LiveTranscriptionEvent{
		Delta: r.GetDelta(),
		Eou:   r.GetEou(),
		Eob:   r.GetEob(),
	}
	for _, w := range r.GetWords() {
		ev.Words = append(ev.Words, schema.TranscriptionWord{
			Start: time.Duration(w.Start),
			End:   time.Duration(w.End),
			Text:  w.Text,
		})
	}
	if r.GetFinalResult() != nil {
		ev.Final = transcriptResultFromProto(r.GetFinalResult())
	}
	return ev
}
