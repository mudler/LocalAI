// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pion/rtp"
	"google.golang.org/grpc"
)

type fakeOpusAudioEncoder struct {
	mu    sync.Mutex
	calls []*pb.AudioEncodeRequest
	next  byte
}

func (f *fakeOpusAudioEncoder) AudioEncode(_ context.Context, req *pb.AudioEncodeRequest, _ ...grpc.CallOption) (*pb.AudioEncodeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyReq := *req
	copyReq.PcmData = append([]byte(nil), req.PcmData...)
	copyReq.Options = make(map[string]string, len(req.Options))
	for key, value := range req.Options {
		copyReq.Options[key] = value
	}
	f.calls = append(f.calls, &copyReq)
	result := &pb.AudioEncodeResult{SampleRate: opusRTPClockRate, SamplesPerFrame: opusRTPSamplesPerFrame}
	if len(req.PcmData) > 0 {
		f.next++
		result.Frames = [][]byte{{f.next}}
	}
	return result, nil
}

func (f *fakeOpusAudioEncoder) snapshotCalls() []*pb.AudioEncodeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*pb.AudioEncodeRequest(nil), f.calls...)
}

type recordedRTPPacket struct {
	packet *rtp.Packet
	at     time.Time
}

type recordingRTPWriter struct {
	mu      sync.Mutex
	packets []recordedRTPPacket
	writes  chan struct{}
}

func newRecordingRTPWriter() *recordingRTPWriter {
	return &recordingRTPWriter{writes: make(chan struct{}, 64)}
}

func (w *recordingRTPWriter) WriteRTP(packet *rtp.Packet) error {
	clone := &rtp.Packet{Header: packet.Header, Payload: append([]byte(nil), packet.Payload...)}
	w.mu.Lock()
	w.packets = append(w.packets, recordedRTPPacket{packet: clone, at: time.Now()})
	w.mu.Unlock()
	w.writes <- struct{}{}
	return nil
}

func (w *recordingRTPWriter) snapshot() []recordedRTPPacket {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]recordedRTPPacket(nil), w.packets...)
}

var _ = Describe("WebRTC audio sender", func() {
	const testFrameDuration = 4 * time.Millisecond

	It("paces continuously across independent TTS callbacks", func() {
		backend := &fakeOpusAudioEncoder{}
		writer := newRecordingRTPWriter()
		sender := newWebRTCAudioSenderWithFrameDuration(backend, writer, nil, testFrameDuration)
		DeferCleanup(func() { Expect(sender.Close(context.Background())).To(Succeed()) })

		pcm := make([]byte, 8*opusRTPSamplesPerFrame*2)
		Expect(sender.Enqueue(context.Background(), pcm, opusRTPClockRate)).To(Succeed())
		Expect(sender.Enqueue(context.Background(), pcm, opusRTPClockRate)).To(Succeed())
		Expect(sender.Drain(context.Background())).To(Succeed())

		packets := writer.snapshot()
		Expect(packets).To(HaveLen(16))
		for i := 1; i < len(packets); i++ {
			Expect(packets[i].at.Sub(packets[i-1].at)).To(BeNumerically(">=", testFrameDuration/2))
			Expect(packets[i].packet.SequenceNumber).To(Equal(packets[i-1].packet.SequenceNumber + 1))
			Expect(packets[i].packet.Timestamp).To(Equal(packets[i-1].packet.Timestamp + opusRTPSamplesPerFrame))
			Expect(packets[i].packet.Marker).To(BeFalse())
		}
		Expect(packets[0].packet.Marker).To(BeTrue())
	})

	It("uses one transport-scoped codec stream and closes it explicitly", func() {
		backend := &fakeOpusAudioEncoder{}
		writer := newRecordingRTPWriter()
		sender := newWebRTCAudioSenderWithFrameDuration(backend, writer, nil, time.Millisecond)

		Expect(sender.Enqueue(context.Background(), make([]byte, opusRTPSamplesPerFrame*2), opusRTPClockRate)).To(Succeed())
		Expect(sender.Drain(context.Background())).To(Succeed())
		Expect(sender.Close(context.Background())).To(Succeed())

		calls := backend.snapshotCalls()
		Expect(calls).To(HaveLen(3))
		streamID := calls[0].Options["stream_id"]
		Expect(streamID).ToNot(BeEmpty())
		Expect(calls[1].Options).To(HaveKeyWithValue("stream_id", streamID))
		Expect(calls[1].Options).To(HaveKeyWithValue("drain", "true"))
		Expect(calls[2].Options).To(HaveKeyWithValue("stream_id", streamID))
		Expect(calls[2].Options).To(HaveKeyWithValue("close", "true"))
	})

	It("acknowledges abort only after old audio can no longer write", func() {
		backend := &fakeOpusAudioEncoder{}
		writer := newRecordingRTPWriter()
		sender := newWebRTCAudioSenderWithFrameDuration(backend, writer, nil, 8*time.Millisecond)
		DeferCleanup(func() { Expect(sender.Close(context.Background())).To(Succeed()) })

		pcm := make([]byte, 10*opusRTPSamplesPerFrame*2)
		Expect(sender.Enqueue(context.Background(), pcm, opusRTPClockRate)).To(Succeed())
		Eventually(writer.writes).Should(Receive())
		Expect(sender.Abort(context.Background())).To(Succeed())
		writtenAtAbort := len(writer.snapshot())
		Consistently(func() int { return len(writer.snapshot()) }, 3*8*time.Millisecond, 2*time.Millisecond).Should(Equal(writtenAtAbort))

		Expect(sender.Enqueue(context.Background(), make([]byte, opusRTPSamplesPerFrame*2), opusRTPClockRate)).To(Succeed())
		Expect(sender.Drain(context.Background())).To(Succeed())
		packets := writer.snapshot()
		Expect(packets).To(HaveLen(writtenAtAbort + 1))
		Expect(packets[len(packets)-1].packet.Marker).To(BeTrue())

		calls := backend.snapshotCalls()
		Expect(calls).To(ContainElement(Satisfy(func(req *pb.AudioEncodeRequest) bool {
			return req.Options["abort"] == "true"
		})))
	})
})
