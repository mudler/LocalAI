package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

const (
	realtimePath = "/v1/realtime"

	// defaultLiveSampleRate is what TranscriptLiveConfig.sample_rate 0 means.
	defaultLiveSampleRate = 16000

	// finalWait bounds how long a closing session waits for an utterance the
	// upstream still has in flight. The upstream transcribes only after its
	// VAD sees the speech end, so a slow model can finish after the client
	// stops sending; waiting forever would pin the gRPC call on a hung
	// upstream.
	finalWait = 5 * time.Second

	// commitGrace is how long a speech_stopped waits for its
	// input_audio_buffer.committed. The upstream sends the two back to back,
	// so a stop with no commit inside this window is a discarded turn and
	// must not hold a closing session for the full finalWait.
	commitGrace = 500 * time.Millisecond

	// maxBacklogSeconds caps the audio held before the ready ack. Callers
	// wait for the ack before streaming, so more than this means a client
	// that ignores the contract, and holding it unbounded while a cold
	// upstream loads models would grow memory without limit.
	maxBacklogSeconds = 5

	// liveWriteTimeout turns an upstream that stops reading into an error
	// instead of a call blocked on a full socket buffer.
	liveWriteTimeout = 10 * time.Second

	// liveHandshakeTimeout bounds the WebSocket upgrade only. The upstream
	// warms the pipeline models before it sends session.created, which can
	// take minutes on a cold box, so the setup phase after the upgrade has
	// its own, longer bound.
	liveHandshakeTimeout = 30 * time.Second

	// defaultLiveSetupTimeout bounds the session setup (upgrade to
	// session.updated) when request_timeout_seconds is unset. Core waits for
	// the ready ack with a plain Recv, so without a bound a hung upstream
	// would hold the call, and the failover that should move the stage to
	// the next target, forever. It is generous because a cold upstream loads
	// the pipeline's VAD and transcription models before it answers.
	defaultLiveSetupTimeout = 3 * time.Minute
)

// liveSetupTimeout is defaultLiveSetupTimeout, as a variable so tests can
// exercise the bound without waiting minutes.
var liveSetupTimeout = defaultLiveSetupTimeout

// realtimeEvent holds the fields the bridge reads from any upstream server
// event; unrelated events decode into it harmlessly.
type realtimeEvent struct {
	Type string `json:"type"`
	// ItemID is set on committed, delta, completed and failed events; the
	// upstream uses the committed turn's id for its transcription events.
	ItemID     string `json:"item_id"`
	Delta      string `json:"delta"`
	Transcript string `json:"transcript"`
	Error      *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// AudioTranscriptionLive bridges a live transcription session to the
// upstream's /v1/realtime transcription session, so a realtime pipeline on
// this box can use a remote LocalAI's streaming ASR. The upstream runs its own
// server VAD and transcribes per utterance: each completed utterance becomes
// Delta (whatever the streamed deltas did not already carry) plus Eou. Word
// timings and Eob have no upstream counterpart and stay empty.
//
// Contract (pkg/grpc/server.go): this method closes out, in closes when the
// client half-closes, and errors are returned at once because the caller
// blocks on the first Recv for the ready ack.
func (p *LocalAIProxy) AudioTranscriptionLive(in <-chan *pb.TranscriptLiveRequest, out chan<- *pb.TranscriptLiveResponse) error {
	defer close(out)

	cfg, err := p.config()
	if err != nil {
		return err
	}
	if cfg.realtimePipeline == "" {
		return grpcerrors.LiveTranscriptionUnsupported(backendName, "set the realtime_pipeline backend option")
	}

	first, ok := <-in
	if !ok {
		return nil // the caller closed without sending anything
	}
	lc := first.GetConfig()
	if lc == nil {
		return status.Error(codes.InvalidArgument, "localai-proxy: the first live transcription message must carry a config")
	}
	rate := int(lc.GetSampleRate())
	if rate == 0 {
		rate = defaultLiveSampleRate
	}

	conn, err := p.dialRealtime(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	s := &liveSession{
		conn:      conn,
		out:       out,
		pipeline:  cfg.realtimePipeline,
		language:  lc.GetLanguage(),
		rate:      rate,
		sent:      map[string]string{},
		committed: map[string]bool{},
		events:    make(chan realtimeEvent),
		done:      make(chan struct{}),
	}
	// Closing done releases the reader if it is blocked handing over an
	// event; closing conn (deferred above, runs after this) unblocks its read.
	defer close(s.done)
	go s.readLoop()

	setup := cfg.timeout
	if setup <= 0 {
		setup = liveSetupTimeout
	}
	return s.run(in, setup)
}

// dialRealtime opens the upstream WebSocket. A refused upgrade is mapped like
// any other upstream HTTP reply, so a 5xx trips failover and a 4xx does not.
func (p *LocalAIProxy) dialRealtime(cfg *proxyConfig) (*websocket.Conn, error) {
	u, err := url.Parse(cfg.base + realtimePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "localai-proxy: build %s URL: %v", realtimePath, err)
	}
	// Load only accepts http(s) bases.
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.RawQuery = url.Values{"model": {cfg.realtimePipeline}}.Encode()

	header := http.Header{}
	if cfg.apiKey != "" {
		header.Set("Authorization", "Bearer "+cfg.apiKey)
	}
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: liveHandshakeTimeout,
	}
	conn, resp, err := dialer.Dial(u.String(), header)
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			defer func() { _ = resp.Body.Close() }()
			return nil, statusError(realtimePath, resp)
		}
		return nil, transportError(realtimePath, err)
	}
	return conn, nil
}

// liveSession is the state of one bridged session. Only run touches it; the
// reader goroutine only hands events over.
type liveSession struct {
	conn     *websocket.Conn
	out      chan<- *pb.TranscriptLiveResponse
	pipeline string
	language string
	rate     int

	sent  map[string]string // text already sent as deltas, per upstream item
	final []string          // completed transcripts, in order

	// In-flight tracking, so a closing session waits only for an utterance
	// the upstream will still transcribe. The upstream VAD emits
	// speech_started, then either speech_stopped plus committed (a turn it
	// will transcribe) or nothing at all (a turn it discarded as no speech).
	speaking  bool            // between speech_started and speech_stopped
	stopping  bool            // speech_stopped seen, its committed not yet
	committed map[string]bool // committed items not yet completed

	events  chan realtimeEvent
	readErr error // set before events is closed
	done    chan struct{}
}

// readLoop decodes upstream events until the socket fails. It is the only
// reader, as gorilla/websocket requires.
func (s *liveSession) readLoop() {
	defer close(s.events)
	for {
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			s.readErr = err
			return
		}
		var ev realtimeEvent
		if err := json.Unmarshal(msg, &ev); err != nil {
			xlog.Debug("localai-proxy: skipping undecodable realtime event", "error", err)
			continue
		}
		select {
		case s.events <- ev:
		case <-s.done:
			return
		}
	}
}

// run drives the session: setup, then audio forwarding and event mapping,
// then the drain once the client closes its side. It is also the only writer
// on the socket, so frames never interleave.
func (s *liveSession) run(in <-chan *pb.TranscriptLiveRequest, setupTimeout time.Duration) error {
	var (
		created, ready bool
		inClosed       bool
		backlog        []*pb.TranscriptLiveRequest
		backlogSamples int
		drainTimer     <-chan time.Time
		graceTimer     <-chan time.Time
	)
	setup := time.NewTimer(setupTimeout)
	defer setup.Stop()
	setupTimer := setup.C
	drain := time.NewTimer(finalWait)
	drain.Stop()
	defer drain.Stop()
	grace := time.NewTimer(commitGrace)
	grace.Stop()
	defer grace.Stop()

	for {
		select {
		case req, ok := <-in:
			if !ok {
				in, inClosed = nil, true
				if !ready {
					// The caller gave up waiting for the ready ack (core
					// closes its send side on failure or cancel). Canceled
					// rather than nil: there is no session to report as
					// complete, and failover neither retries nor trips a
					// target on a canceled call.
					return status.Error(codes.Canceled, "localai-proxy: live transcription closed before the upstream session was ready")
				}
				if s.inFlight() {
					drain.Reset(finalWait)
					drainTimer = drain.C
				}
			} else if !ready {
				// Hold audio sent before the ready ack rather than drop it,
				// up to a bound.
				backlogSamples += len(req.GetAudio().GetPcm())
				if backlogSamples > maxBacklogSeconds*s.rate {
					return status.Errorf(codes.InvalidArgument,
						"localai-proxy: more than %d s of audio sent before the live transcription session was ready", maxBacklogSeconds)
				}
				backlog = append(backlog, req)
			} else if err := s.forward(req); err != nil {
				return err
			}

		case ev, ok := <-s.events:
			if !ok {
				return s.upstreamGone()
			}
			switch ev.Type {
			case "session.created":
				if created {
					continue
				}
				created = true
				if err := s.write(s.sessionUpdate()); err != nil {
					return err
				}
			case "session.updated":
				if ready || !created {
					continue
				}
				ready = true
				setupTimer = nil
				s.out <- &pb.TranscriptLiveResponse{Ready: true}
				for _, req := range backlog {
					if err := s.forward(req); err != nil {
						return err
					}
				}
				backlog = nil
			case "error":
				return s.upstreamError("error", ev)
			case "conversation.item.input_audio_transcription.failed":
				return s.upstreamError("transcription failed", ev)
			case "input_audio_buffer.speech_started":
				s.speaking, s.stopping = true, false
			case "input_audio_buffer.speech_stopped":
				if s.speaking {
					s.speaking, s.stopping = false, true
					grace.Reset(commitGrace)
					graceTimer = grace.C
				}
			case "input_audio_buffer.committed":
				s.stopping, graceTimer = false, nil
				if ev.ItemID != "" {
					s.committed[ev.ItemID] = true
				}
			case "conversation.item.input_audio_transcription.delta":
				s.delta(ev)
			case "conversation.item.input_audio_transcription.completed":
				s.completed(ev)
			}

		case <-graceTimer:
			// A stop the upstream never committed: the turn was discarded.
			s.stopping, graceTimer = false, nil

		case <-setupTimer:
			return status.Errorf(codes.Unavailable, "localai-proxy: upstream %s did not set up the transcription session within %s", realtimePath, setupTimeout)

		case <-drainTimer:
			xlog.Warn("localai-proxy: upstream did not finish the last utterance in time; finalizing without it",
				"pipeline", s.pipeline, "wait", finalWait)
			return s.finish()
		}

		if inClosed && !s.inFlight() {
			return s.finish()
		}
	}
}

// inFlight reports an utterance the upstream is still expected to
// transcribe.
func (s *liveSession) inFlight() bool {
	return s.speaking || s.stopping || len(s.committed) > 0
}

func (s *liveSession) sessionUpdate() map[string]any {
	return map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{
				"input": map[string]any{
					"format":         map[string]any{"type": "audio/pcm", "rate": s.rate},
					"transcription":  map[string]any{"model": s.pipeline, "language": s.language},
					"turn_detection": map[string]any{"type": "server_vad"},
				},
			},
		},
	}
}

// forward sends one client message upstream. The upstream session rate is
// fixed at setup, so a second config cannot be honoured.
func (s *liveSession) forward(req *pb.TranscriptLiveRequest) error {
	if req.GetConfig() != nil {
		return status.Error(codes.InvalidArgument, "localai-proxy: a live transcription config is only accepted as the first message")
	}
	pcm := req.GetAudio().GetPcm()
	if len(pcm) == 0 {
		return nil
	}
	return s.write(map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(pcm16LE(pcm)),
	})
}

// pcm16LE converts float samples in [-1, 1] to the little-endian PCM16 the
// realtime API takes. Out-of-range samples are clipped rather than wrapped.
func pcm16LE(pcm []float32) []byte {
	buf := make([]byte, len(pcm)*2)
	for i, f := range pcm {
		v := float64(f)
		if math.IsNaN(v) {
			// A NaN would convert to an arbitrary int16; silence is the
			// only neutral value.
			v = 0
		}
		v = math.Max(-1, math.Min(1, v))
		// #nosec G115 -- two's-complement reinterpretation for little-endian PCM16 encoding, value range already clamped
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(v*math.MaxInt16)))
	}
	return buf
}

func (s *liveSession) delta(ev realtimeEvent) {
	if ev.Delta == "" {
		return
	}
	s.sent[ev.ItemID] += ev.Delta
	s.out <- &pb.TranscriptLiveResponse{Delta: ev.Delta}
}

// completed ends one utterance. Deltas only arrive when the upstream pipeline
// streams its transcription, so the completion carries whatever text the
// deltas did not; if the final transcript diverged from them there is no way
// to retract, and FinalResult carries the authoritative text.
func (s *liveSession) completed(ev realtimeEvent) {
	sent := s.sent[ev.ItemID]
	delete(s.sent, ev.ItemID)
	rest, ok := strings.CutPrefix(ev.Transcript, sent)
	if !ok {
		rest = ""
	}
	delete(s.committed, ev.ItemID)
	if t := strings.TrimSpace(ev.Transcript); t != "" {
		s.final = append(s.final, t)
	}
	s.out <- &pb.TranscriptLiveResponse{Delta: rest, Eou: true}
}

// finish sends the final transcript and closes the upstream session cleanly.
func (s *liveSession) finish() error {
	s.out <- &pb.TranscriptLiveResponse{FinalResult: &pb.TranscriptResult{Text: strings.Join(s.final, " ")}}
	_ = s.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return nil
}

func (s *liveSession) write(v any) error {
	if err := s.conn.SetWriteDeadline(time.Now().Add(liveWriteTimeout)); err != nil {
		return s.writeFailed(err)
	}
	if err := s.conn.WriteJSON(v); err != nil {
		return s.writeFailed(err)
	}
	return nil
}

func (s *liveSession) writeFailed(err error) error {
	xlog.Warn("localai-proxy: realtime write failed", "pipeline", s.pipeline, "error", err)
	return status.Errorf(codes.Unavailable, "localai-proxy: upstream %s: %v", realtimePath, err)
}

// upstreamGone maps a socket that stopped delivering events. It is
// Unavailable even for a clean close: the client did not end the session, so
// the upstream dropped it, and failover should reopen on the next target.
func (s *liveSession) upstreamGone() error {
	err := s.readErr
	if err == nil {
		err = errors.New("connection closed")
	}
	xlog.Warn("localai-proxy: realtime upstream disconnected", "pipeline", s.pipeline, "error", err)
	return status.Errorf(codes.Unavailable, "localai-proxy: upstream %s disconnected: %v", realtimePath, err)
}

func (s *liveSession) upstreamError(what string, ev realtimeEvent) error {
	msg := "no details"
	if ev.Error != nil && ev.Error.Message != "" {
		msg = ev.Error.Message
		if ev.Error.Code != "" {
			msg = fmt.Sprintf("%s (%s)", msg, ev.Error.Code)
		}
	}
	xlog.Warn("localai-proxy: realtime upstream reported an error", "pipeline", s.pipeline, "event", what, "error", msg)
	return status.Errorf(codes.Unavailable, "localai-proxy: upstream %s %s: %s", realtimePath, what, msg)
}
