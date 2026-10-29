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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestMockBackend(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mock backend")
}

// startMockS2S serves MockBackend on a loopback port and returns a client.
// The mock is driven over real gRPC so the tests cover the wire contract too.
func startMockS2S() pb.BackendClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	srv := grpc.NewServer()
	pb.RegisterBackendServer(srv, &MockBackend{})
	go func() { _ = srv.Serve(lis) }()
	DeferCleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///"+lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = conn.Close() })
	return pb.NewBackendClient(conn)
}

func openS2S(client pb.BackendClient) pb.Backend_AudioToAudioStreamClient {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	DeferCleanup(cancel)
	stream, err := client.AudioToAudioStream(ctx)
	Expect(err).ToNot(HaveOccurred())
	return stream
}

func sendS2S(stream pb.Backend_AudioToAudioStreamClient, req *pb.AudioToAudioRequest) {
	GinkgoHelper()
	Expect(stream.Send(req)).To(Succeed(), "send")
}

func recvS2S(stream pb.Backend_AudioToAudioStreamClient) *pb.AudioToAudioResponse {
	GinkgoHelper()
	evt, err := stream.Recv()
	Expect(err).ToNot(HaveOccurred(), "recv")
	return evt
}

// untilDone reads events up to and including response.done and returns them.
func untilDone(stream pb.Backend_AudioToAudioStreamClient) []*pb.AudioToAudioResponse {
	GinkgoHelper()
	var out []*pb.AudioToAudioResponse
	for {
		evt := recvS2S(stream)
		out = append(out, evt)
		if evt.GetEvent() == "response.done" || evt.GetEvent() == "error" {
			return out
		}
	}
}

func metaOf(evt *pb.AudioToAudioResponse) map[string]any {
	GinkgoHelper()
	var m map[string]any
	Expect(json.Unmarshal(evt.GetMeta(), &m)).To(Succeed(), "%s meta is not JSON", evt.GetEvent())
	return m
}

// withReplyFrames sets the length of the replies for the current spec.
func withReplyFrames(n int) {
	s2sReplyFrames.Store(int32(n))
	DeferCleanup(func() { s2sReplyFrames.Store(mockS2SDefaultFrames) })
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

func eventNames(events []*pb.AudioToAudioResponse) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.GetEvent())
	}
	return names
}

func configWithModalities(tools, modalities string) *pb.AudioToAudioRequest {
	return &pb.AudioToAudioRequest{Payload: &pb.AudioToAudioRequest_Config{
		Config: &pb.AudioToAudioConfig{Tools: tools, Params: map[string]string{"output_modalities": modalities}},
	}}
}

var _ = Describe("mock speech-to-speech stream", func() {
	It("opens with session.created, the fixed rates and the voices", func() {
		stream := openS2S(startMockS2S())
		sendS2S(stream, configReq(""))

		evt := recvS2S(stream)
		Expect(evt.GetEvent()).To(Equal("session.created"))
		m := metaOf(evt)
		Expect(m["input_sample_rate"]).To(Equal(float64(16000)))
		Expect(m["output_sample_rate"]).To(Equal(float64(24000)))
		Expect(m["voices"]).To(Equal([]any{"female", "male"}))
	})

	It("answers an audio turn with the transcript and tone frames", func() {
		withReplyFrames(4)
		stream := openS2S(startMockS2S())
		sendS2S(stream, configReq(""))
		recvS2S(stream) // session.created

		// One second of a 300 Hz tone at amplitude 10000: RMS is about 7071.
		sendS2S(stream, frameReq(tonePCM(8000, 10000), false))
		sendS2S(stream, frameReq(nil, true))
		Expect(recvS2S(stream).GetEvent()).To(Equal("input_audio_buffer.committed"))

		sendS2S(stream, controlReq("response.create", `{"max_output_tokens":200}`))
		events := untilDone(stream)

		var text string
		var audio int
		for _, evt := range events {
			switch evt.GetEvent() {
			case "response.audio_transcript.delta":
				text += metaOf(evt)["delta"].(string)
			case "response.audio.delta":
				audio++
				Expect(evt.GetSampleRate()).To(Equal(int32(24000)))
				Expect(evt.GetPcm()).To(HaveLen(960))
				Expect(rmsS16LE(evt.GetPcm())).To(BeNumerically(">=", 1000), "audio frame is silent")
			}
		}
		Expect(text).To(Equal("mock reply rms=7071"))
		Expect(audio).To(Equal(4))
		m := metaOf(events[len(events)-1])
		Expect(m["status"]).To(Equal("completed"))
		Expect(m["text"]).To(Equal(text))
	})

	It("sends no audio for a text-only response", func() {
		withReplyFrames(4)
		stream := openS2S(startMockS2S())
		sendS2S(stream, configReq(""))
		recvS2S(stream)

		sendS2S(stream, controlReq("response.create", `{"output_modalities":["text"]}`))
		for _, evt := range untilDone(stream) {
			Expect(evt.GetEvent()).ToNot(Equal("response.audio.delta"), "text-only response sent audio")
		}
	})

	It("calls a tool once, then continues after its output", func() {
		withReplyFrames(2)
		stream := openS2S(startMockS2S())
		sendS2S(stream, configReq(weatherTool))
		recvS2S(stream)

		sendS2S(stream, controlReq("conversation.item.create",
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"Call the get_time tool."}]}`))
		sendS2S(stream, controlReq("response.create", `{}`))
		events := untilDone(stream)
		Expect(eventNames(events)).To(Equal([]string{"response.function_call_arguments.done", "response.done"}))
		call := metaOf(events[0])
		Expect(call["name"]).To(Equal("get_time"))
		Expect(call["call_id"]).To(Equal("call_mock_1"))
		Expect(call["arguments"]).To(Equal(`{"city":"Paris"}`))
		Expect(metaOf(events[1])["status"]).To(Equal("completed"))
		raw, _ := metaOf(events[1])["raw_text"].(string)
		Expect(raw).To(ContainSubstring("<function=get_time>"), "raw_text keeps the call markup")

		sendS2S(stream, controlReq("conversation.item.create",
			`{"type":"function_call_output","call_id":"call_mock_1","output":"{\"time\":\"12:00\"}"}`))
		sendS2S(stream, controlReq("response.create", `{}`))
		audio := 0
		for _, evt := range untilDone(stream) {
			switch evt.GetEvent() {
			case "response.function_call_arguments.done":
				Fail("second tool call after the output was already sent")
			case "response.audio.delta":
				audio++
			case "response.done":
				meta := metaOf(evt)
				Expect(meta["raw_text"]).To(Equal(meta["text"]), "a plain reply with tools")
			}
		}
		Expect(audio).To(Equal(2))
	})

	It("stops the reply at a cancel and closes cleanly", func() {
		withReplyFrames(500)
		stream := openS2S(startMockS2S())
		sendS2S(stream, configReq(""))
		recvS2S(stream)

		sendS2S(stream, controlReq("response.create", `{}`))
		for recvS2S(stream).GetEvent() != "response.audio.delta" {
		}
		sendS2S(stream, controlReq("response.cancel", `{}`))

		var last *pb.AudioToAudioResponse
		for last == nil {
			evt := recvS2S(stream)
			if evt.GetEvent() == "response.done" {
				last = evt
				break
			}
			Expect(evt.GetEvent()).To(BeElementOf("response.audio.delta", "response.audio_transcript.delta"), "unexpected event while cancelling")
		}
		Expect(metaOf(last)["status"]).To(Equal("cancelled"))

		// Nothing follows the cancelled response.done except the answer to a
		// cancel with no reply running, which the llama.cpp backend refuses with
		// response_cancel_not_active. Then the stream closes cleanly.
		sendS2S(stream, controlReq("response.cancel", `{}`))
		evt := recvS2S(stream)
		Expect(evt.GetEvent()).To(Equal("error"))
		Expect(metaOf(evt)["type"]).To(Equal("response_cancel_not_active"))
		Expect(stream.CloseSend()).To(Succeed())
		_, err := stream.Recv()
		Expect(errors.Is(err, io.EOF)).To(BeTrue(), "after cancel the stream sent more data: err=%v", err)
	})

	Describe("protocol errors", func() {
		It("needs a config as the first message", func() {
			stream := openS2S(startMockS2S())
			sendS2S(stream, controlReq("response.create", `{}`))
			Expect(recvS2S(stream).GetEvent()).To(Equal("error"))
		})

		It("ends the stream with an error on invalid tools", func() {
			stream := openS2S(startMockS2S())
			sendS2S(stream, configReq(`{not json`))
			Expect(recvS2S(stream).GetEvent()).To(Equal("error"))
		})

		It("answers an unknown control event with an error", func() {
			stream := openS2S(startMockS2S())
			sendS2S(stream, configReq(""))
			recvS2S(stream)
			sendS2S(stream, controlReq("response.bogus", `{}`))
			Expect(recvS2S(stream).GetEvent()).To(Equal("error"))
		})
	})

	It("keeps a tool call silent unless the modes are fixed to text plus audio", func() {
		spoken := func(stream pb.Backend_AudioToAudioStreamClient) int {
			sendS2S(stream, controlReq("response.create", ""))
			n := 0
			for _, evt := range untilDone(stream) {
				if evt.GetEvent() == "response.audio.delta" {
					n++
				}
			}
			return n
		}

		// No modalities in the config: the model chooses, so a tool call is silent.
		auto := openS2S(startMockS2S())
		sendS2S(auto, configReq(weatherTool))
		recvS2S(auto)
		Expect(spoken(auto)).To(BeZero(), "a tool call in the default mode sent audio frames")

		// Text plus audio fixed at open: the model reads the call out loud.
		forced := openS2S(startMockS2S())
		sendS2S(forced, configWithModalities(weatherTool, `["text","audio"]`))
		recvS2S(forced)
		Expect(spoken(forced)).ToNot(BeZero(), "a tool call in the fixed text plus audio mode was silent")
	})

	It("refuses session changes after the first turn", func() {
		stream := openS2S(startMockS2S())
		sendS2S(stream, configReq(""))
		recvS2S(stream)

		sendS2S(stream, controlReq("session.update", `{"voice":"male"}`))
		Expect(recvS2S(stream).GetEvent()).To(Equal("session.updated"), "a voice change before the first turn")
		sendS2S(stream, controlReq("conversation.item.create", `{"type":"message"}`))
		sendS2S(stream, controlReq("session.update", `{"voice":"female"}`))
		evt := recvS2S(stream)
		Expect(evt.GetEvent()).To(Equal("error"), "a late voice change")
		Expect(metaOf(evt)["response_scoped"]).To(BeNil(), "an error outside a response")
	})

	It("keeps the reply running through errors that are not about it", func() {
		withReplyFrames(10)
		stream := openS2S(startMockS2S())
		sendS2S(stream, configReq(""))
		recvS2S(stream)

		sendS2S(stream, controlReq("response.create", ""))
		sendS2S(stream, controlReq("input_audio_buffer.commit", ""))
		sendS2S(stream, controlReq("response.create", ""))

		var errTypes []string
		for done := false; !done; {
			evt := recvS2S(stream)
			switch evt.GetEvent() {
			case "error":
				m := metaOf(evt)
				typ, _ := m["type"].(string)
				errTypes = append(errTypes, typ)
				scoped := m["response_scoped"] == true
				Expect(scoped).To(Equal(typ == "conversation_already_has_active_response"), "response_scoped of %s", typ)
			case "response.done":
				done = true
				Expect(metaOf(evt)["status"]).To(Equal("completed"), "the reply was cut short")
			}
		}
		Expect(errTypes).To(Equal([]string{"input_audio_buffer_commit_empty", "conversation_already_has_active_response"}))
	})
})
