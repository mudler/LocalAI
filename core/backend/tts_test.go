package backend

// Specs for the TTSRequest assembly that carries the per-request
// instructions/params from the OpenAI `instructions` field (and the LocalAI
// `params` extension) through to the gRPC boundary. Before this plumbing the
// instruction value was dropped before reaching the backend; these specs pin
// that it now survives, and that the empty case stays backward compatible.

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	ggrpc "google.golang.org/grpc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type scriptedTTSStreamBackend struct {
	grpcPkg.Backend
	run func(context.Context, *pb.TTSRequest, func(*pb.Reply)) error
}

func (b *scriptedTTSStreamBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *scriptedTTSStreamBackend) IsBusy() bool                              { return false }
func (b *scriptedTTSStreamBackend) TTSStream(ctx context.Context, request *pb.TTSRequest, callback func(*pb.Reply), _ ...ggrpc.CallOption) error {
	return b.run(ctx, request, callback)
}

func ttsStreamTestLoader(streamBackend grpcPkg.Backend) *model.ModelLoader {
	loader := model.NewModelLoader(&system.SystemState{})
	loader.SetModelRouter(func(_ context.Context, id string, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
		return model.NewModelWithClient(id, "test://tts-stream", streamBackend), nil
	})
	return loader
}

func ttsStreamTestConfig() config.ModelConfig {
	threads := 1
	cfg := config.ModelConfig{Name: "tts-stream-test", Backend: "stub-backend", Threads: &threads}
	cfg.SetDefaults()
	cfg.Name = "tts-stream-test"
	cfg.Model = "tts-stream-test.bin"
	return cfg
}

func callTTSStreamForTest(streamBackend grpcPkg.Backend, callback func([]byte) error) error {
	appConfig := config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
	return ModelTTSStream(
		context.Background(), "hello", "", "", "", nil,
		ttsStreamTestLoader(streamBackend), appConfig, ttsStreamTestConfig(), callback,
	)
}

func directStreamingWAVHeader(sampleRate uint32) []byte {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 0xFFFFFFFF)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], 0xFFFFFFFF)
	return header
}

var _ = Describe("newTTSRequest", func() {
	It("attaches the instructions when a per-request value is set", func() {
		req := newTTSRequest("hi", "/m", "alloy", "/out.wav", "en", "cheerful narrator", nil, "m")
		Expect(req.Instructions).ToNot(BeNil())
		Expect(req.GetInstructions()).To(Equal("cheerful narrator"))
		Expect(req.GetText()).To(Equal("hi"))
		Expect(req.GetVoice()).To(Equal("alloy"))
		Expect(req.GetDst()).To(Equal("/out.wav"))
		Expect(req.GetLanguage()).To(Equal("en"))
	})

	It("leaves instructions unset when empty so backends fall back to YAML", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil, "m")
		Expect(req.Instructions).To(BeNil())
		Expect(req.GetInstructions()).To(Equal(""))
	})

	It("forwards per-request params through to the backend", func() {
		params := map[string]string{"exaggeration": "0.7", "cfg_weight": "0.3"}
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", params, "m")
		Expect(req.GetParams()).To(HaveKeyWithValue("exaggeration", "0.7"))
		Expect(req.GetParams()).To(HaveKeyWithValue("cfg_weight", "0.3"))
	})

	It("leaves params nil when none are supplied", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil, "m")
		Expect(req.GetParams()).To(BeNil())
	})
})

// TTSRequest carries TWO model strings and they are not interchangeable.
// `Model` is a path that FileStagingClient.TTS rewrites into a worker-local
// absolute path in distributed mode; `ModelIdentity` is the untranslated
// ModelConfig.Model the backend compares against what it loaded (#10952).
// Comparing `Model` instead would reject valid requests in exactly the
// configuration this guards, so these specs pin them as separate inputs.
var _ = Describe("newTTSRequest model identity", func() {
	It("carries the untranslated identity alongside the staged model path", func() {
		req := newTTSRequest("hi", "/worker/local/staged.onnx", "", "/out.wav", "", "", nil, "voices/piper-en.onnx")
		Expect(req.GetModelIdentity()).To(Equal("voices/piper-en.onnx"))
		Expect(req.GetModel()).To(Equal("/worker/local/staged.onnx"))
	})

	It("keeps the identity empty when the config names no model, so the backend skips", func() {
		req := newTTSRequest("hi", "/m", "", "/out.wav", "", "", nil, "")
		Expect(req.GetModelIdentity()).To(BeEmpty())
	})
})

var _ = Describe("ModelTTSStream framing and cancellation", func() {
	It("converts required metadata into one WAV header before forwarding PCM", func() {
		streamBackend := &scriptedTTSStreamBackend{run: func(_ context.Context, _ *pb.TTSRequest, callback func(*pb.Reply)) error {
			callback(&pb.Reply{Message: []byte(`{"sample_rate":24000}`)})
			callback(&pb.Reply{Audio: []byte{1, 2, 3, 4}})
			return nil
		}}
		var chunks [][]byte

		err := callTTSStreamForTest(streamBackend, func(chunk []byte) error {
			chunks = append(chunks, append([]byte(nil), chunk...))
			return nil
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(chunks).To(HaveLen(2))
		Expect(chunks[0]).To(HaveLen(44))
		Expect(string(chunks[0][:4])).To(Equal("RIFF"))
		Expect(binary.LittleEndian.Uint32(chunks[0][24:28])).To(Equal(uint32(24000)))
		Expect(chunks[1]).To(Equal([]byte{1, 2, 3, 4}))
	})

	It("preserves the established direct WAV-header framing used by native backends", func() {
		header := directStreamingWAVHeader(24000)
		streamBackend := &scriptedTTSStreamBackend{run: func(_ context.Context, _ *pb.TTSRequest, callback func(*pb.Reply)) error {
			callback(&pb.Reply{Audio: header})
			callback(&pb.Reply{Audio: []byte{1, 2}})
			return nil
		}}
		var chunks [][]byte

		err := callTTSStreamForTest(streamBackend, func(chunk []byte) error {
			chunks = append(chunks, append([]byte(nil), chunk...))
			return nil
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(chunks).To(Equal([][]byte{header, {1, 2}}))
	})

	DescribeTable("rejects malformed stream framing",
		func(script func(func(*pb.Reply)), message string) {
			streamBackend := &scriptedTTSStreamBackend{run: func(_ context.Context, _ *pb.TTSRequest, callback func(*pb.Reply)) error {
				script(callback)
				return nil
			}}

			err := callTTSStreamForTest(streamBackend, func([]byte) error { return nil })

			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("audio before metadata", func(callback func(*pb.Reply)) {
			callback(&pb.Reply{Audio: []byte{1, 2}})
		}, "sample-rate metadata"),
		Entry("invalid metadata", func(callback func(*pb.Reply)) {
			callback(&pb.Reply{Message: []byte(`{"sample_rate":0}`)})
		}, "invalid sample-rate"),
		Entry("odd PCM", func(callback func(*pb.Reply)) {
			callback(&pb.Reply{Message: []byte(`{"sample_rate":24000}`)})
			callback(&pb.Reply{Audio: []byte{1}})
		}, "odd-length"),
		Entry("metadata without audio", func(callback func(*pb.Reply)) {
			callback(&pb.Reply{Message: []byte(`{"sample_rate":24000}`)})
		}, "no audio"),
		Entry("metadata after framing", func(callback func(*pb.Reply)) {
			callback(&pb.Reply{Message: []byte(`{"sample_rate":24000}`)})
			callback(&pb.Reply{Message: []byte(`{"sample_rate":24000}`)})
		}, "unexpected metadata"),
	)

	It("cancels the backend stream on the first downstream callback failure", func() {
		downstreamErr := errors.New("downstream stopped")
		backendCancelled := make(chan struct{})
		streamBackend := &scriptedTTSStreamBackend{run: func(ctx context.Context, _ *pb.TTSRequest, callback func(*pb.Reply)) error {
			callback(&pb.Reply{Message: []byte(`{"sample_rate":24000}`)})
			callback(&pb.Reply{Audio: []byte{1, 2}})
			<-ctx.Done()
			close(backendCancelled)
			callback(&pb.Reply{Audio: []byte{3, 4}})
			return ctx.Err()
		}}
		callbackCount := 0

		err := callTTSStreamForTest(streamBackend, func([]byte) error {
			callbackCount++
			if callbackCount == 2 {
				return downstreamErr
			}
			return nil
		})

		Expect(err).To(MatchError(downstreamErr))
		Expect(backendCancelled).To(BeClosed())
		Expect(callbackCount).To(Equal(2), "callbacks must stop after the first downstream error")
	})

	It("preserves a backend failure returned before stream framing", func() {
		backendErr := errors.New("generation failed before first audio")
		streamBackend := &scriptedTTSStreamBackend{run: func(_ context.Context, _ *pb.TTSRequest, _ func(*pb.Reply)) error {
			return backendErr
		}}

		err := callTTSStreamForTest(streamBackend, func([]byte) error { return nil })

		Expect(err).To(MatchError(backendErr))
	})
})
