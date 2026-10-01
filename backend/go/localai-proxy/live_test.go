package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// wsUpstream is a fake upstream /v1/realtime endpoint. Each spec scripts the
// server side of the session in script, which runs on the upgraded socket.
func wsUpstream(script func(c *websocket.Conn, r *http.Request)) *fakeUpstream {
	upgrader := websocket.Upgrader{}
	return newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime" {
			http.NotFound(w, r)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		script(c, r)
	})
}

func wsSend(c *websocket.Conn, v any) {
	defer GinkgoRecover()
	Expect(c.WriteJSON(v)).To(Succeed())
}

// wsRecv reads the next client event; ok is false once the client is gone.
func wsRecv(c *websocket.Conn) (map[string]any, bool) {
	var ev map[string]any
	if err := c.ReadJSON(&ev); err != nil {
		return nil, false
	}
	return ev, true
}

// wsHandshake plays the upstream's session setup and returns the client's
// session.update event.
func wsHandshake(c *websocket.Conn) map[string]any {
	defer GinkgoRecover()
	wsSend(c, map[string]any{"type": "session.created", "session": map[string]any{}})
	upd, ok := wsRecv(c)
	Expect(ok).To(BeTrue())
	Expect(upd["type"]).To(Equal("session.update"))
	wsSend(c, map[string]any{"type": "session.updated", "session": map[string]any{}})
	return upd
}

// wsDrain reads client events until the client closes, returning the
// decoded PCM16 samples of every input_audio_buffer.append.
func wsDrain(c *websocket.Conn) [][]int16 {
	var frames [][]int16
	for {
		ev, ok := wsRecv(c)
		if !ok {
			return frames
		}
		if ev["type"] != "input_audio_buffer.append" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(ev["audio"].(string))
		if err != nil {
			return frames
		}
		samples := make([]int16, len(raw)/2)
		for i := range samples {
			samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
		frames = append(frames, samples)
	}
}

type liveCall struct {
	in   chan *pb.TranscriptLiveRequest
	out  chan *pb.TranscriptLiveResponse
	errc chan error
}

// startLive runs AudioTranscriptionLive the way pkg/grpc/server.go does:
// buffered channels, the caller owning in and the backend owning out.
func startLive(p *LocalAIProxy) *liveCall {
	lc := &liveCall{
		in:   make(chan *pb.TranscriptLiveRequest, 4),
		out:  make(chan *pb.TranscriptLiveResponse, 4),
		errc: make(chan error, 1),
	}
	go func() { lc.errc <- p.AudioTranscriptionLive(lc.in, lc.out) }()
	return lc
}

func (lc *liveCall) config(lang string, rate int32) {
	lc.in <- &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Config{
		Config: &pb.TranscriptLiveConfig{Language: lang, SampleRate: rate},
	}}
}

func (lc *liveCall) audio(pcm ...float32) {
	lc.in <- &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Audio{
		Audio: &pb.TranscriptLiveAudio{Pcm: pcm},
	}}
}

func (lc *liveCall) next() *pb.TranscriptLiveResponse {
	var r *pb.TranscriptLiveResponse
	EventuallyWithOffset(1, lc.out, 2*time.Second).Should(Receive(&r))
	return r
}

// finish asserts the call returned within 2 s and out is closed, and
// returns the call's error.
func (lc *liveCall) finish() error {
	var err error
	EventuallyWithOffset(1, lc.errc, 2*time.Second).Should(Receive(&err))
	for range lc.out {
	}
	return err
}

// liveGoroutines counts goroutines still running code from live.go, so a
// spec can prove the bridge left nothing blocked behind.
func liveGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "localai-proxy/live.go:") {
			n++
		}
	}
	return n
}

func ev(typ string) map[string]any { return map[string]any{"type": typ} }

func item(typ, id string) map[string]any { return map[string]any{"type": typ, "item_id": id} }

func completed(id, transcript string) map[string]any {
	return map[string]any{"type": "conversation.item.input_audio_transcription.completed", "item_id": id, "transcript": transcript}
}

// stallAfterUpdate plays session.created, reads the session.update and then
// never answers, as a hung upstream would, until the spec ends.
func stallAfterUpdate(c *websocket.Conn, gone chan<- struct{}) {
	wsSend(c, map[string]any{"type": "session.created", "session": map[string]any{}})
	wsRecv(c)
	for {
		if _, ok := wsRecv(c); !ok {
			close(gone)
			return
		}
	}
}

func withPipeline(o *pb.ModelOptions) {
	o.Options = append(o.Options, "realtime_pipeline:remote-pipe")
}

var _ = Describe("AudioTranscriptionLive", func() {
	It("reports live transcription unsupported without realtime_pipeline", func() {
		up := newFakeUpstream()
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, nil))

		err := lc.finish()
		Expect(grpcerrors.IsLiveTranscriptionUnsupported(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("realtime_pipeline"))
		Expect(up.recorded()).To(BeEmpty())
	})

	It("rejects a first message that is not a config", func() {
		up := wsUpstream(func(*websocket.Conn, *http.Request) {})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.audio(0.1)

		Expect(status.Code(lc.finish())).To(Equal(codes.InvalidArgument))
	})

	It("opens the pipeline session and acks ready only after session.updated", func() {
		gotURL := make(chan string, 1)
		gotAuth := make(chan string, 1)
		gotUpdate := make(chan map[string]any, 1)
		release := make(chan struct{})
		up := wsUpstream(func(c *websocket.Conn, r *http.Request) {
			gotURL <- r.URL.RequestURI()
			gotAuth <- r.Header.Get("Authorization")
			wsSend(c, map[string]any{"type": "session.created", "session": map[string]any{}})
			upd, _ := wsRecv(c)
			gotUpdate <- upd
			<-release
			wsSend(c, map[string]any{"type": "session.updated", "session": map[string]any{}})
			wsDrain(c)
		})
		DeferCleanup(up.Close)
		keyFile := writeInput("key", "sekret\n")
		p := loadProxy(up, func(o *pb.ModelOptions) {
			withPipeline(o)
			o.Proxy.ApiKeyFile = keyFile
		})

		lc := startLive(p)
		lc.config("it", 24000)

		Eventually(gotURL, 2*time.Second).Should(Receive(Equal("/v1/realtime?model=remote-pipe")))
		Expect(gotAuth).To(Receive(Equal("Bearer sekret")))
		var upd map[string]any
		Eventually(gotUpdate, 2*time.Second).Should(Receive(&upd))
		raw, err := json.Marshal(upd)
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(MatchJSON(`{"type":"session.update","session":{"type":"transcription","audio":{"input":{
			"format":{"type":"audio/pcm","rate":24000},
			"transcription":{"model":"remote-pipe","language":"it"},
			"turn_detection":{"type":"server_vad"}}}}}`))

		Consistently(lc.out, 200*time.Millisecond).ShouldNot(Receive())
		close(release)
		Expect(lc.next().GetReady()).To(BeTrue())

		close(lc.in)
		Expect(lc.finish()).To(Succeed())
	})

	It("defaults the session rate to 16000", func() {
		gotUpdate := make(chan map[string]any, 1)
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			gotUpdate <- wsHandshake(c)
			wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("", 0)
		Expect(lc.next().GetReady()).To(BeTrue())

		var upd map[string]any
		Expect(gotUpdate).To(Receive(&upd))
		format := upd["session"].(map[string]any)["audio"].(map[string]any)["input"].(map[string]any)["format"]
		Expect(format).To(HaveKeyWithValue("rate", BeNumerically("==", 16000)))

		close(lc.in)
		Expect(lc.finish()).To(Succeed())
	})

	It("forwards audio as base64 PCM16 appends", func() {
		frames := make(chan [][]int16, 1)
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsHandshake(c)
			frames <- wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		Expect(lc.next().GetReady()).To(BeTrue())

		lc.audio(0, 0.5, -1, 1, 2)
		lc.audio(-0.25)
		lc.audio(float32(math.NaN()))
		close(lc.in)
		Expect(lc.finish()).To(Succeed())

		Eventually(frames, 2*time.Second).Should(Receive(Equal([][]int16{
			{0, 16383, -32767, 32767, 32767},
			{-8191},
			{0},
		})))
	})

	It("maps deltas and completions to Delta and Eou, and finishes with the full text", func() {
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsHandshake(c)
			wsSend(c, ev("input_audio_buffer.speech_started"))
			wsSend(c, ev("input_audio_buffer.speech_stopped"))
			wsSend(c, item("input_audio_buffer.committed", "a"))
			wsSend(c, map[string]any{"type": "conversation.item.input_audio_transcription.delta", "item_id": "a", "delta": "hel"})
			wsSend(c, map[string]any{"type": "conversation.item.input_audio_transcription.delta", "item_id": "a", "delta": "lo"})
			wsSend(c, completed("a", "hello world"))
			wsSend(c, ev("input_audio_buffer.speech_started"))
			wsSend(c, ev("input_audio_buffer.speech_stopped"))
			wsSend(c, item("input_audio_buffer.committed", "b"))
			wsSend(c, completed("b", "again"))
			wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		Expect(lc.next().GetReady()).To(BeTrue())

		Expect(lc.next()).To(SatisfyAll(
			WithTransform((*pb.TranscriptLiveResponse).GetDelta, Equal("hel")),
			WithTransform((*pb.TranscriptLiveResponse).GetEou, BeFalse())))
		Expect(lc.next().GetDelta()).To(Equal("lo"))
		r := lc.next()
		Expect(r.GetDelta()).To(Equal(" world"))
		Expect(r.GetEou()).To(BeTrue())
		r = lc.next()
		Expect(r.GetDelta()).To(Equal("again"))
		Expect(r.GetEou()).To(BeTrue())

		close(lc.in)
		Expect(lc.next().GetFinalResult().GetText()).To(Equal("hello world again"))
		Expect(lc.finish()).To(Succeed())
	})

	It("waits for a committed utterance before the final result", func() {
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsHandshake(c)
			wsSend(c, ev("input_audio_buffer.speech_started"))
			wsSend(c, ev("input_audio_buffer.speech_stopped"))
			wsSend(c, item("input_audio_buffer.committed", "a"))
			wsSend(c, map[string]any{"type": "conversation.item.input_audio_transcription.delta", "item_id": "a", "delta": "late"})
			// The client closes its side once it sees the delta; the
			// transcription completes later.
			time.Sleep(300 * time.Millisecond)
			wsSend(c, completed("a", "late words"))
			wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		Expect(lc.next().GetReady()).To(BeTrue())
		Expect(lc.next().GetDelta()).To(Equal("late"))
		close(lc.in)

		r := lc.next()
		Expect(r.GetDelta()).To(Equal(" words"))
		Expect(r.GetEou()).To(BeTrue())
		Expect(lc.next().GetFinalResult().GetText()).To(Equal("late words"))
		Expect(lc.finish()).To(Succeed())
	})

	It("does not hold the close for a turn the upstream discarded", func() {
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsHandshake(c)
			wsSend(c, ev("input_audio_buffer.speech_started"))
			wsSend(c, ev("input_audio_buffer.speech_stopped"))
			wsSend(c, item("input_audio_buffer.committed", "a"))
			wsSend(c, completed("a", "kept"))
			// A stop that is never committed, then nothing more.
			wsSend(c, ev("input_audio_buffer.speech_started"))
			wsSend(c, ev("input_audio_buffer.speech_stopped"))
			wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		Expect(lc.next().GetReady()).To(BeTrue())
		Expect(lc.next().GetDelta()).To(Equal("kept"))
		close(lc.in)

		start := time.Now()
		Expect(lc.next().GetFinalResult().GetText()).To(Equal("kept"))
		Expect(lc.finish()).To(Succeed())
		Expect(time.Since(start)).To(BeNumerically("<", finalWait/2))
	})

	It("ends with Unavailable on an upstream error event during setup", func() {
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsSend(c, map[string]any{"type": "session.created", "session": map[string]any{}})
			wsRecv(c)
			wsSend(c, map[string]any{"type": "error", "error": map[string]any{
				"type": "invalid_request_error", "code": "session_update_error",
				"message": "model is not a valid pipeline model: remote-pipe",
			}})
			wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)

		err := lc.finish()
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		Expect(err.Error()).To(ContainSubstring("not a valid pipeline model"))
	})

	It("ends with Unavailable when a transcription fails", func() {
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsHandshake(c)
			wsSend(c, map[string]any{"type": "conversation.item.input_audio_transcription.failed", "item_id": "a",
				"error": map[string]any{"message": "backend crashed"}})
			wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		Expect(lc.next().GetReady()).To(BeTrue())

		err := lc.finish()
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		Expect(err.Error()).To(ContainSubstring("backend crashed"))
	})

	It("maps a refused upgrade to the upstream status", func() {
		up := newFakeUpstreamWithHandler(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "loading", http.StatusServiceUnavailable)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)

		Expect(status.Code(lc.finish())).To(Equal(codes.Unavailable))
	})

	It("upstream disconnect ends the stream", func() {
		before := liveGoroutines()
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsHandshake(c)
			// Drop the socket mid-session, as a crashed upstream would.
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		Expect(lc.next().GetReady()).To(BeTrue())
		// The caller keeps its send side open; the bridge must not wait on it.

		Expect(status.Code(lc.finish())).To(Equal(codes.Unavailable))
		Eventually(liveGoroutines, time.Second).Should(Equal(before))
		close(lc.in)
	})

	It("gives up with Canceled when the caller closes before the ready ack", func() {
		before := liveGoroutines()
		gone := make(chan struct{})
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) { stallAfterUpdate(c, gone) })
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		lc.audio(0.1)
		close(lc.in)

		Expect(status.Code(lc.finish())).To(Equal(codes.Canceled))
		Eventually(gone, 2*time.Second).Should(BeClosed(), "upstream socket left open")
		Eventually(liveGoroutines, time.Second).Should(Equal(before))
	})

	It("bounds a hung setup by request_timeout_seconds", func() {
		before := liveGoroutines()
		gone := make(chan struct{})
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) { stallAfterUpdate(c, gone) })
		DeferCleanup(up.Close)
		p := loadProxy(up, func(o *pb.ModelOptions) {
			withPipeline(o)
			o.Proxy.RequestTimeoutSeconds = 1
		})
		lc := startLive(p)
		lc.config("en", 16000)

		var err error
		Eventually(lc.errc, 3*time.Second).Should(Receive(&err))
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		Expect(lc.out).To(BeClosed())
		Eventually(gone, 2*time.Second).Should(BeClosed(), "upstream socket left open")
		Eventually(liveGoroutines, time.Second).Should(Equal(before))
		close(lc.in)
	})

	It("bounds a hung setup by default when request_timeout_seconds is unset", func() {
		Expect(defaultLiveSetupTimeout).To(BeNumerically(">=", 2*time.Minute))
		saved := liveSetupTimeout
		liveSetupTimeout = 300 * time.Millisecond
		DeferCleanup(func() { liveSetupTimeout = saved })

		before := liveGoroutines()
		gone := make(chan struct{})
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) { stallAfterUpdate(c, gone) })
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)

		Expect(status.Code(lc.finish())).To(Equal(codes.Unavailable))
		Eventually(gone, 2*time.Second).Should(BeClosed(), "upstream socket left open")
		Eventually(liveGoroutines, time.Second).Should(Equal(before))
		close(lc.in)
	})

	It("forwards audio sent before the ready ack once ready", func() {
		release := make(chan struct{})
		frames := make(chan [][]int16, 1)
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) {
			wsSend(c, map[string]any{"type": "session.created", "session": map[string]any{}})
			wsRecv(c)
			<-release
			wsSend(c, map[string]any{"type": "session.updated", "session": map[string]any{}})
			frames <- wsDrain(c)
		})
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		lc.audio(0.5)
		close(release)
		Expect(lc.next().GetReady()).To(BeTrue())
		close(lc.in)
		Expect(lc.finish()).To(Succeed())
		Eventually(frames, 2*time.Second).Should(Receive(Equal([][]int16{{16383}})))
	})

	It("refuses more than the backlog cap of audio before the ready ack", func() {
		gone := make(chan struct{})
		up := wsUpstream(func(c *websocket.Conn, _ *http.Request) { stallAfterUpdate(c, gone) })
		DeferCleanup(up.Close)
		lc := startLive(loadProxy(up, withPipeline))
		lc.config("en", 16000)
		second := make([]float32, 16000)
		for range maxBacklogSeconds + 1 {
			select {
			case lc.in <- &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Audio{Audio: &pb.TranscriptLiveAudio{Pcm: second}}}:
			case <-time.After(2 * time.Second):
				Fail("bridge stopped reading audio before the cap")
			}
		}

		err := lc.finish()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("before the live transcription session was ready"))
		close(lc.in)
	})
})
