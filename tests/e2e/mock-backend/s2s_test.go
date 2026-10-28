// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// startMockS2S serves MockBackend on a loopback port and returns a client.
// The mock is driven over real gRPC so the tests cover the wire contract too.
func startMockS2S(t *testing.T) pb.BackendClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	pb.RegisterBackendServer(srv, &MockBackend{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///"+lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewBackendClient(conn)
}

func openS2S(t *testing.T, client pb.BackendClient) pb.Backend_AudioToAudioStreamClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	stream, err := client.AudioToAudioStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func sendS2S(t *testing.T, stream pb.Backend_AudioToAudioStreamClient, req *pb.AudioToAudioRequest) {
	t.Helper()
	if err := stream.Send(req); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func recvS2S(t *testing.T, stream pb.Backend_AudioToAudioStreamClient) *pb.AudioToAudioResponse {
	t.Helper()
	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	return evt
}

// untilDone reads events up to and including response.done and returns them.
func untilDone(t *testing.T, stream pb.Backend_AudioToAudioStreamClient) []*pb.AudioToAudioResponse {
	t.Helper()
	var out []*pb.AudioToAudioResponse
	for {
		evt := recvS2S(t, stream)
		out = append(out, evt)
		if evt.GetEvent() == "response.done" || evt.GetEvent() == "error" {
			return out
		}
	}
}

func metaOf(t *testing.T, evt *pb.AudioToAudioResponse) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(evt.GetMeta(), &m); err != nil {
		t.Fatalf("%s meta is not JSON: %v", evt.GetEvent(), err)
	}
	return m
}

func configReq(tools string) *pb.AudioToAudioRequest {
	return &pb.AudioToAudioRequest{Payload: &pb.AudioToAudioRequest_Config{
		Config: &pb.AudioToAudioConfig{Tools: tools},
	}}
}

func controlReq(event, payload string) *pb.AudioToAudioRequest {
	return &pb.AudioToAudioRequest{Payload: &pb.AudioToAudioRequest_Control{
		Control: &pb.AudioToAudioControl{Event: event, Payload: []byte(payload)},
	}}
}

func frameReq(pcm []byte, endOfInput bool) *pb.AudioToAudioRequest {
	return &pb.AudioToAudioRequest{Payload: &pb.AudioToAudioRequest_Frame{
		Frame: &pb.AudioToAudioFrame{Pcm: pcm, EndOfInput: endOfInput},
	}}
}

// tonePCM is n samples of a sine at amplitude a, mono s16le. Its RMS is a/sqrt(2).
func tonePCM(n int, a float64) []byte {
	pcm := make([]byte, n*2)
	for i := range n {
		v := int16(a * math.Sin(2*math.Pi*300*float64(i)/16000))
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
	}
	return pcm
}

const weatherTool = `[{"type":"function","function":{"name":"get_time","parameters":{"type":"object"}}}]`

func TestMockS2SSessionCreated(t *testing.T) {
	stream := openS2S(t, startMockS2S(t))
	sendS2S(t, stream, configReq(""))

	evt := recvS2S(t, stream)
	if evt.GetEvent() != "session.created" {
		t.Fatalf("first event = %q, want session.created", evt.GetEvent())
	}
	m := metaOf(t, evt)
	if m["input_sample_rate"] != float64(16000) || m["output_sample_rate"] != float64(24000) {
		t.Fatalf("rates = %v", m)
	}
	voices, _ := m["voices"].([]any)
	if len(voices) != 2 || voices[0] != "female" || voices[1] != "male" {
		t.Fatalf("voices = %v", m["voices"])
	}
}

func TestMockS2SAudioTurn(t *testing.T) {
	t.Setenv("MOCK_S2S_FRAMES", "4")
	stream := openS2S(t, startMockS2S(t))
	sendS2S(t, stream, configReq(""))
	recvS2S(t, stream) // session.created

	// One second of a 300 Hz tone at amplitude 10000: RMS is about 7071.
	sendS2S(t, stream, frameReq(tonePCM(8000, 10000), false))
	sendS2S(t, stream, frameReq(nil, true))
	if evt := recvS2S(t, stream); evt.GetEvent() != "input_audio_buffer.committed" {
		t.Fatalf("after end_of_input got %q, want input_audio_buffer.committed", evt.GetEvent())
	}

	sendS2S(t, stream, controlReq("response.create", `{"max_output_tokens":200}`))
	events := untilDone(t, stream)

	var text string
	var audio int
	for _, evt := range events {
		switch evt.GetEvent() {
		case "response.audio_transcript.delta":
			text += metaOf(t, evt)["delta"].(string)
		case "response.audio.delta":
			audio++
			if evt.GetSampleRate() != 24000 || len(evt.GetPcm()) != 960 {
				t.Fatalf("audio frame rate=%d bytes=%d, want 24000 and 960", evt.GetSampleRate(), len(evt.GetPcm()))
			}
			if rmsS16LE(evt.GetPcm()) < 1000 {
				t.Fatalf("audio frame is silent")
			}
		}
	}
	if text != "mock reply rms=7071" {
		t.Fatalf("transcript = %q, want %q", text, "mock reply rms=7071")
	}
	if audio != 4 {
		t.Fatalf("audio frames = %d, want 4 (MOCK_S2S_FRAMES)", audio)
	}
	done := events[len(events)-1]
	m := metaOf(t, done)
	if m["status"] != "completed" || m["text"] != text {
		t.Fatalf("response.done meta = %v", m)
	}
}

func TestMockS2STextOnly(t *testing.T) {
	t.Setenv("MOCK_S2S_FRAMES", "4")
	stream := openS2S(t, startMockS2S(t))
	sendS2S(t, stream, configReq(""))
	recvS2S(t, stream)

	sendS2S(t, stream, controlReq("response.create", `{"output_modalities":["text"]}`))
	for _, evt := range untilDone(t, stream) {
		if evt.GetEvent() == "response.audio.delta" {
			t.Fatal("text-only response sent audio")
		}
	}
}

func TestMockS2SToolCallThenContinuation(t *testing.T) {
	t.Setenv("MOCK_S2S_FRAMES", "2")
	stream := openS2S(t, startMockS2S(t))
	sendS2S(t, stream, configReq(weatherTool))
	recvS2S(t, stream)

	sendS2S(t, stream, controlReq("conversation.item.create",
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Call the get_time tool."}]}`))
	sendS2S(t, stream, controlReq("response.create", `{}`))
	events := untilDone(t, stream)
	if len(events) != 2 || events[0].GetEvent() != "response.function_call_arguments.done" {
		t.Fatalf("tool turn events = %v", eventNames(events))
	}
	call := metaOf(t, events[0])
	if call["name"] != "get_time" || call["call_id"] != "call_mock_1" || call["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("call = %v", call)
	}
	if metaOf(t, events[1])["status"] != "completed" {
		t.Fatalf("tool turn done = %v", metaOf(t, events[1]))
	}

	sendS2S(t, stream, controlReq("conversation.item.create",
		`{"type":"function_call_output","call_id":"call_mock_1","output":"{\"time\":\"12:00\"}"}`))
	sendS2S(t, stream, controlReq("response.create", `{}`))
	audio := 0
	for _, evt := range untilDone(t, stream) {
		switch evt.GetEvent() {
		case "response.function_call_arguments.done":
			t.Fatal("second tool call after the output was already sent")
		case "response.audio.delta":
			audio++
		}
	}
	if audio != 2 {
		t.Fatalf("continuation audio frames = %d, want 2", audio)
	}
}

func TestMockS2SCancelStopsReply(t *testing.T) {
	t.Setenv("MOCK_S2S_FRAMES", "500")
	stream := openS2S(t, startMockS2S(t))
	sendS2S(t, stream, configReq(""))
	recvS2S(t, stream)

	sendS2S(t, stream, controlReq("response.create", `{}`))
	for {
		if recvS2S(t, stream).GetEvent() == "response.audio.delta" {
			break
		}
	}
	sendS2S(t, stream, controlReq("response.cancel", `{}`))

	var last *pb.AudioToAudioResponse
	for {
		evt := recvS2S(t, stream)
		if evt.GetEvent() == "response.done" {
			last = evt
			break
		}
		if evt.GetEvent() != "response.audio.delta" && evt.GetEvent() != "response.audio_transcript.delta" {
			t.Fatalf("unexpected event while cancelling: %s", evt.GetEvent())
		}
	}
	if metaOf(t, last)["status"] != "cancelled" {
		t.Fatalf("done = %v, want status cancelled", metaOf(t, last))
	}

	// Nothing follows the cancelled response.done: the stream closes cleanly.
	sendS2S(t, stream, controlReq("response.cancel", `{}`)) // no active reply: a no-op
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("after cancel the stream sent more data: err=%v", err)
	}
}

func TestMockS2SProtocolErrors(t *testing.T) {
	t.Run("first message must be config", func(t *testing.T) {
		stream := openS2S(t, startMockS2S(t))
		sendS2S(t, stream, controlReq("response.create", `{}`))
		evt := recvS2S(t, stream)
		if evt.GetEvent() != "error" {
			t.Fatalf("got %q, want error", evt.GetEvent())
		}
	})
	t.Run("invalid tools end the stream with an error", func(t *testing.T) {
		stream := openS2S(t, startMockS2S(t))
		sendS2S(t, stream, configReq(`{not json`))
		if evt := recvS2S(t, stream); evt.GetEvent() != "error" {
			t.Fatalf("got %q, want error", evt.GetEvent())
		}
	})
	t.Run("unknown control event is an error", func(t *testing.T) {
		stream := openS2S(t, startMockS2S(t))
		sendS2S(t, stream, configReq(""))
		recvS2S(t, stream)
		sendS2S(t, stream, controlReq("response.bogus", `{}`))
		if evt := recvS2S(t, stream); evt.GetEvent() != "error" {
			t.Fatalf("got %q, want error", evt.GetEvent())
		}
	})
}

func eventNames(events []*pb.AudioToAudioResponse) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.GetEvent())
	}
	return names
}
