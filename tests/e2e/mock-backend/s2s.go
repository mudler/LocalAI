// SPDX-License-Identifier: MIT
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The mock AudioToAudioStream is a deterministic stand-in for an any-to-any
// speech model. It follows the contract in backend/backend.proto and the
// llama-cpp realtime session, so the realtime endpoint can be tested without
// weights. Behaviour, per turn:
//
//   - config: session.created with the fixed rates and voices. Tools are kept.
//   - frame / input_audio_buffer.commit: the input is kept until the commit,
//     which records its RMS and replies input_audio_buffer.committed.
//   - response.create: when tools are set and no function_call_output has
//     arrived yet, the first tool is called with {"city":"Paris"}. Otherwise the
//     reply is "mock reply rms=<n>" as transcript pieces, followed by
//     MOCK_S2S_FRAMES 20 ms tone frames (default 50 = 1 s) at 24 kHz, one every
//     20 ms of wall time, so a barge-in can land mid-response.
//   - response.cancel: stops the reply at once and sends response.done with
//     status cancelled.
//
// Where it matters, it keeps the limits of the llama-cpp session:
//
//   - output_modalities from the config is a fixed session mode. Without one,
//     each response picks its mode from the tools it has then: the model
//     chooses with tools, text plus audio without. A forced text plus audio
//     mode with tools makes the model read the tool call out loud.
//   - after the first commit or message, session.update of instructions, voice
//     or tools is refused with an error that does not belong to a response, and
//     a running reply goes on.
//   - committing an empty input buffer is an error that does not end a reply.
//   - response.create while a reply runs is refused.
//   - an error that ends the request of a response.create carries
//     "response_scoped": true in its meta; others do not.
//
// Everything is driven from one receive loop. Each response runs on its own
// goroutine and shares no state with the loop beyond its plan, so only the
// gRPC send is locked.

const (
	mockS2SInputRate     = 16000
	mockS2SOutputRate    = 24000
	mockS2SFrameDuration = 20 * time.Millisecond
	mockS2SToneHz        = 440
	mockS2SToneAmplitude = 8000
	mockS2SDefaultFrames = 50
)

// mockS2SFrames returns how many tone frames a reply has, from
// MOCK_S2S_FRAMES. Read per stream so a spec can change it with t.Setenv.
func mockS2SFrames() int {
	if s := os.Getenv("MOCK_S2S_FRAMES"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			return v
		}
	}
	return mockS2SDefaultFrames
}

type s2sStream struct {
	stream pb.Backend_AudioToAudioStreamServer

	sendMu sync.Mutex
	seq    int64

	configured       bool
	tools            []string
	outputModalities []string
	started          bool
	turnPCM          []byte
	lastRMS          float64
	toolOutputSeen   bool
	calls            int
	resp             *s2sResponse
}

// s2sResponse is one reply in flight. cancel is closed by the receive loop;
// done is closed by the response goroutine once its last event is sent.
type s2sResponse struct {
	cancel chan struct{}
	done   chan struct{}
}

// s2sPlan is everything a response goroutine needs, copied at response.create.
type s2sPlan struct {
	toolName string
	callID   string
	rms      int
	textOnly bool
	frames   int
	// spoken is the text and audio of a model that reads a tool call out loud.
	spoken string
}

// AudioToAudioStream implements the bidirectional S2S RPC.
func (m *MockBackend) AudioToAudioStream(stream pb.Backend_AudioToAudioStreamServer) error {
	s := &s2sStream{stream: stream}
	defer s.stopResponse()

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !s.configured && req.GetConfig() == nil {
			s.sendError("invalid_request_error", "the first message must be a config")
			return nil
		}
		switch p := req.GetPayload().(type) {
		case *pb.AudioToAudioRequest_Config:
			if !s.configure(p.Config) {
				return nil
			}
		case *pb.AudioToAudioRequest_Frame:
			s.frame(p.Frame)
		case *pb.AudioToAudioRequest_Control:
			s.control(p.Control)
		default:
			s.sendError("invalid_request_error", "empty request payload")
		}
	}
}

// configure starts a session, or restarts it on reset. A later config without
// reset only replaces tools and params. It returns false after an error event,
// which ends the stream.
func (s *s2sStream) configure(c *pb.AudioToAudioConfig) bool {
	tools, err := parseS2STools(c.GetTools())
	if err != nil {
		s.sendError("invalid_request_error", "tools: "+err.Error())
		return false
	}
	s.stopResponse()
	first := !s.configured || c.GetReset_()
	if first {
		s.started = false
		s.outputModalities = nil
		s.turnPCM = nil
		s.lastRMS = 0
		s.toolOutputSeen = false
		s.calls = 0
		s.configured = true
		s.send("session.created", nil, 0, map[string]any{
			"input_sample_rate":  mockS2SInputRate,
			"output_sample_rate": mockS2SOutputRate,
			"voices":             []string{"female", "male"},
		})
	}
	s.tools = tools
	if mods := parseS2SModalities(c.GetParams()["output_modalities"]); mods != nil {
		s.outputModalities = mods
	}
	return true
}

func (s *s2sStream) frame(f *pb.AudioToAudioFrame) {
	s.turnPCM = append(s.turnPCM, f.GetPcm()...)
	if f.GetEndOfInput() {
		s.commit()
	}
}

func (s *s2sStream) commit() {
	if len(s.turnPCM) == 0 {
		s.sendError("input_audio_buffer_commit_empty", "input_audio_buffer.commit: the buffer is empty")
		return
	}
	s.started = true
	s.lastRMS = rmsS16LE(s.turnPCM)
	s.turnPCM = nil
	s.send("input_audio_buffer.committed", nil, 0, nil)
}

func (s *s2sStream) control(c *pb.AudioToAudioControl) {
	switch c.GetEvent() {
	case "input_audio_buffer.commit":
		s.commit()
	case "response.create":
		s.responseCreate(c.GetPayload())
	case "response.cancel":
		// The real backend refuses a cancel when nothing is running.
		if s.resp == nil || s.responseFinished() {
			s.sendError("response_cancel_not_active", "response.cancel: there is no response to cancel")
			return
		}
		s.stopResponse()
	case "conversation.item.create":
		s.itemCreate(c.GetPayload())
	case "session.update":
		s.sessionUpdate(c.GetPayload())
	default:
		s.sendError("invalid_request_error", "unknown control event "+c.GetEvent())
	}
}

func (s *s2sStream) itemCreate(payload []byte) {
	var item struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &item); err != nil {
		s.sendError("invalid_request_error", "conversation.item.create: "+err.Error())
		return
	}
	switch item.Type {
	case "function_call_output":
		s.started = true
		s.toolOutputSeen = true
	case "message":
		s.started = true
	default:
		s.sendError("invalid_request_error", "unsupported item type "+item.Type)
	}
}

func (s *s2sStream) sessionUpdate(payload []byte) {
	var upd struct {
		Tools            json.RawMessage `json:"tools"`
		Instructions     string          `json:"instructions"`
		Voice            string          `json:"voice"`
		OutputModalities []string        `json:"output_modalities"`
	}
	if err := json.Unmarshal(payload, &upd); err != nil {
		s.sendError("invalid_request_error", "session.update: "+err.Error())
		return
	}
	if s.started {
		for _, f := range []struct {
			name string
			set  bool
		}{
			{"instructions", upd.Instructions != ""},
			{"tools", len(upd.Tools) > 0 && string(upd.Tools) != "null"},
			{"voice", upd.Voice != ""},
		} {
			if f.set {
				s.sendError("invalid_request_error", "session.update: "+f.name+" cannot change after the first audio or message")
				return
			}
		}
	}
	if len(upd.Tools) > 0 && string(upd.Tools) != "null" {
		tools, err := parseS2STools(string(upd.Tools))
		if err != nil {
			s.sendError("invalid_request_error", "tools: "+err.Error())
			return
		}
		s.tools = tools
	}
	if len(upd.OutputModalities) > 0 {
		s.outputModalities = upd.OutputModalities
	}
	s.send("session.updated", nil, 0, map[string]any{
		"input_sample_rate":  mockS2SInputRate,
		"output_sample_rate": mockS2SOutputRate,
		"voices":             []string{"female", "male"},
	})
}

func (s *s2sStream) responseCreate(payload []byte) {
	if !s.configured {
		s.sendScopedError("invalid_request_error", "response.create before config")
		return
	}
	if s.resp != nil && !s.responseFinished() {
		s.sendScopedError("conversation_already_has_active_response", "response.create: a response is already running")
		return
	}
	var req struct {
		OutputModalities []string `json:"output_modalities"`
		MaxOutputTokens  int      `json:"max_output_tokens"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			s.sendScopedError("invalid_request_error", "response.create: "+err.Error())
			return
		}
	}
	s.stopResponse()

	// The mode is picked when the response starts: a response option, else the
	// fixed session mode, else the default for the tools set now.
	modalities := s.outputModalities
	if len(req.OutputModalities) > 0 {
		modalities = req.OutputModalities
	}
	forced := len(modalities) > 0
	if !forced && len(s.tools) == 0 {
		modalities = []string{"text", "audio"}
	}
	textOnly := len(modalities) == 1 && modalities[0] == "text"

	var plan s2sPlan
	if len(s.tools) > 0 && !s.toolOutputSeen {
		s.calls++
		plan = s2sPlan{toolName: s.tools[0], callID: fmt.Sprintf("call_mock_%d", s.calls)}
		if forced && !textOnly {
			// Forced text plus audio: the model reads the call out loud.
			plan.spoken = "calling " + s.tools[0]
		}
	} else {
		frames := mockS2SFrames()
		if req.MaxOutputTokens > 0 && req.MaxOutputTokens < frames {
			frames = req.MaxOutputTokens
		}
		plan = s2sPlan{
			rms:      int(math.Round(s.lastRMS)),
			textOnly: textOnly,
			frames:   frames,
		}
		if textOnly {
			plan.frames = 0
		}
	}

	r := &s2sResponse{cancel: make(chan struct{}), done: make(chan struct{})}
	s.resp = r
	go s.runResponse(r, plan)
}

func (s *s2sStream) responseFinished() bool {
	select {
	case <-s.resp.done:
		return true
	default:
		return false
	}
}

// stopResponse cancels the reply in flight, if any, and waits until its
// response.done is sent. It is a no-op when the reply already finished.
func (s *s2sStream) stopResponse() {
	r := s.resp
	if r == nil {
		return
	}
	close(r.cancel)
	<-r.done
	s.resp = nil
}

func (s *s2sStream) runResponse(r *s2sResponse, p s2sPlan) {
	defer close(r.done)

	if p.toolName != "" {
		select {
		case <-r.cancel:
			s.finish("cancelled", 0, "")
			return
		default:
		}
		if p.spoken != "" {
			s.send("response.audio_transcript.delta", nil, 0, map[string]any{"delta": p.spoken})
			s.send("response.audio.delta", s2sToneFrame(0), mockS2SOutputRate, nil)
		}
		s.send("response.function_call_arguments.done", nil, 0, map[string]any{
			"call_id":   p.callID,
			"name":      p.toolName,
			"arguments": `{"city":"Paris"}`,
		})
		s.finish("completed", 0, "")
		return
	}

	textEvent := "response.audio_transcript.delta"
	if p.textOnly {
		textEvent = "response.text.delta"
	}
	// The transcript is sent in three pieces, before the audio.
	pieces := []string{"mock ", "reply ", fmt.Sprintf("rms=%d", p.rms)}
	var text strings.Builder
	for _, piece := range pieces {
		select {
		case <-r.cancel:
			s.finish("cancelled", 0, text.String())
			return
		default:
		}
		s.send(textEvent, nil, 0, map[string]any{"delta": piece})
		text.WriteString(piece)
	}

	ticker := time.NewTicker(mockS2SFrameDuration)
	defer ticker.Stop()
	sent := 0
	for i := range p.frames {
		select {
		case <-r.cancel:
			s.finish("cancelled", sent, text.String())
			return
		case <-ticker.C:
		}
		s.send("response.audio.delta", s2sToneFrame(i), mockS2SOutputRate, nil)
		sent++
	}
	s.finish("completed", sent, text.String())
}

func (s *s2sStream) finish(status string, frames int, text string) {
	s.send("response.done", nil, 0, map[string]any{
		"status": status,
		"usage":  map[string]any{"output_tokens": frames},
		"text":   text,
	})
}

func (s *s2sStream) sendError(typ, msg string) {
	s.send("error", nil, 0, map[string]any{"type": typ, "message": msg})
}

// sendScopedError reports that a response.create did not start a response.
func (s *s2sStream) sendScopedError(typ, msg string) {
	s.send("error", nil, 0, map[string]any{"type": typ, "message": msg, "response_scoped": true})
}

// send writes one event. meta is JSON-encoded; nil means "{}". Errors are
// dropped on purpose: a dead stream is torn down by the receive loop.
func (s *s2sStream) send(event string, pcm []byte, sampleRate int32, meta any) {
	metaBytes := []byte("{}")
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			return
		}
		metaBytes = b
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.seq++
	_ = s.stream.Send(&pb.AudioToAudioResponse{
		Event:      event,
		Pcm:        pcm,
		SampleRate: sampleRate,
		Meta:       metaBytes,
		Sequence:   s.seq,
	})
}

// parseS2STools reads an OpenAI-style tool array and returns the function
// names. Both {"type":"function","function":{"name":...}} and a bare
// {"name":...} are accepted. An empty string means no tools.
func parseS2STools(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var defs []struct {
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(raw), &defs); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		name := d.Function.Name
		if name == "" {
			name = d.Name
		}
		if name == "" {
			return nil, errors.New("tool without a name")
		}
		names = append(names, name)
	}
	return names, nil
}

// parseS2SModalities accepts a JSON array or a comma-separated list.
func parseS2SModalities(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var mods []string
	if json.Unmarshal([]byte(raw), &mods) == nil {
		return mods
	}
	for _, m := range strings.Split(raw, ",") {
		if m = strings.TrimSpace(m); m != "" {
			mods = append(mods, m)
		}
	}
	return mods
}

// rmsS16LE is the RMS of mono s16le PCM. A trailing odd byte is ignored.
func rmsS16LE(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sumSq float64
	for i := range n {
		v := float64(int16(uint16(pcm[2*i]) | uint16(pcm[2*i+1])<<8))
		sumSq += v * v
	}
	return math.Sqrt(sumSq / float64(n))
}

// s2sToneFrame returns the i-th 20 ms frame of a 440 Hz tone at 24 kHz, s16le.
// The sample index is absolute, so consecutive frames join without a click.
func s2sToneFrame(i int) []byte {
	samplesPerFrame := mockS2SOutputRate * int(mockS2SFrameDuration/time.Millisecond) / 1000
	pcm := make([]byte, samplesPerFrame*2)
	for j := range samplesPerFrame {
		t := float64(i*samplesPerFrame+j) / mockS2SOutputRate
		v := int16(mockS2SToneAmplitude * math.Sin(2*math.Pi*mockS2SToneHz*t))
		pcm[2*j] = byte(v)
		pcm[2*j+1] = byte(uint16(v) >> 8)
	}
	return pcm
}
