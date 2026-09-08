package openai

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"time"

	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"google.golang.org/grpc"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func realtimeProfileWAV(duration time.Duration) []byte {
	const (
		sampleRate    = 16000
		channels      = 1
		bitsPerSample = 16
	)
	dataSize := int(duration.Seconds() * sampleRate * channels * bitsPerSample / 8)
	buf := bytes.NewBuffer(nil)
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*channels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

var _ = Describe("realtime pipeline voice profiles", func() {
	It("resolves a saved profile to an immutable lease and transcript", func(ctx SpecContext) {
		store := voiceprofile.NewStore(GinkgoT().TempDir())
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
		profile, err := store.Create(ctx, voiceprofile.CreateInput{
			Name:             "Narrator",
			Language:         "en-US",
			Transcript:       "The reference transcript.",
			ConsentConfirmed: true,
		}, bytes.NewReader(realtimeProfileWAV(time.Second)))
		Expect(err).NotTo(HaveOccurred())

		voice, params, release, err := resolveRealtimeVoice(ctx, profile.Voice, &config.ModelConfig{
			Name:      "clone-base",
			Backend:   "qwen3-tts-cpp",
			TTSConfig: config.TTSConfig{VoiceCloning: ptrTo(true)},
		}, store)

		Expect(err).NotTo(HaveOccurred())
		Expect(voice).To(BeAnExistingFile())
		Expect(params).To(Equal(map[string]string{"ref_text": "The reference transcript."}))
		release()
		release()
		Expect(voice).NotTo(BeAnExistingFile())
	})

	It("leaves an ordinary backend voice unchanged with no parameters", func() {
		voice, params, release, err := resolveRealtimeVoice(context.Background(), "speaker-7", &config.ModelConfig{}, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(voice).To(Equal("speaker-7"))
		Expect(params).To(BeNil())
		Expect(release).NotTo(BeNil())
		Expect(func() { release(); release() }).NotTo(Panic())
	})

	DescribeTable("returns actionable reference errors",
		func(configuredVoice string, cfg *config.ModelConfig, store *voiceprofile.Store, expected string) {
			_, _, release, err := resolveRealtimeVoice(context.Background(), configuredVoice, cfg, store)
			Expect(err).To(MatchError(ContainSubstring(expected)))
			Expect(release).To(BeNil())
		},
		Entry("malformed reference", "localai://voice-profiles/not-a-uuid", &config.ModelConfig{}, nil, "invalid voice profile reference"),
		Entry("unsupported model", "localai://voice-profiles/00000000-0000-0000-0000-000000000001", &config.ModelConfig{Backend: "piper"}, nil, "does not support reference-audio voice cloning"),
		Entry("unavailable store", "localai://voice-profiles/00000000-0000-0000-0000-000000000001", &config.ModelConfig{Name: "clone-base", Backend: "qwen3-tts-cpp", TTSConfig: config.TTSConfig{VoiceCloning: ptrTo(true)}}, nil, "voice profile store is unavailable"),
	)

	It("reports a missing profile", func() {
		store := voiceprofile.NewStore(GinkgoT().TempDir())
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
		_, _, release, err := resolveRealtimeVoice(context.Background(), "localai://voice-profiles/00000000-0000-0000-0000-000000000001", &config.ModelConfig{
			Name: "clone-base", Backend: "qwen3-tts-cpp", TTSConfig: config.TTSConfig{VoiceCloning: ptrTo(true)},
		}, store)
		Expect(errors.Is(err, voiceprofile.ErrNotFound)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("voice profile not found"))
		Expect(release).To(BeNil())
	})
})

type recordingTTSBackend struct {
	grpcPkg.Backend
	requests []*proto.TTSRequest
}

func (b *recordingTTSBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *recordingTTSBackend) IsBusy() bool                              { return false }

func (b *recordingTTSBackend) record(req *proto.TTSRequest) {
	b.requests = append(b.requests, req)
	req.Params["ref_text"] = "backend mutation"
}

func (b *recordingTTSBackend) TTS(_ context.Context, req *proto.TTSRequest, _ ...grpc.CallOption) (*proto.Result, error) {
	b.record(req)
	return &proto.Result{Success: true}, nil
}

func (b *recordingTTSBackend) TTSStream(_ context.Context, req *proto.TTSRequest, callback func(*proto.Reply), _ ...grpc.CallOption) error {
	b.record(req)
	header := make([]byte, wavStreamHeaderBytes)
	binary.LittleEndian.PutUint32(header[24:28], 24000)
	callback(&proto.Reply{Audio: header})
	return nil
}

var _ = Describe("wrappedModel voice profile parameters", func() {
	var (
		wrapped         *wrappedModel
		backendRecorder *recordingTTSBackend
	)

	BeforeEach(func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appConfig := config.NewApplicationConfig(config.WithSystemState(state))
		appConfig.GeneratedContentDir = GinkgoT().TempDir()
		loader := model.NewModelLoader(state)
		backendRecorder = &recordingTTSBackend{}
		cfg := &config.ModelConfig{Name: "tts-test", Backend: "test"}
		cfg.Model = "weights"
		loaded := model.NewModelWithClient(cfg.ModelID(), "in-process", backendRecorder)
		loaded.MarkHealthy()
		_, err = loader.LoadModel(cfg.ModelID(), cfg.Model, func(_, _, _ string) (*model.Model, error) { return loaded, nil })
		Expect(err).NotTo(HaveOccurred())
		wrapped = &wrappedModel{
			TTSConfig:   cfg,
			ttsParams:   map[string]string{"ref_text": "Original transcript"},
			modelLoader: loader,
			appConfig:   appConfig,
		}
	})

	It("forwards a fresh transcript parameter map to every unary request", func() {
		_, _, err := wrapped.TTS(context.Background(), "one", "voice.wav", "en")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = wrapped.TTS(context.Background(), "two", "voice.wav", "en")
		Expect(err).NotTo(HaveOccurred())

		Expect(backendRecorder.requests).To(HaveLen(2))
		Expect(backendRecorder.requests[0].Params).To(HaveKeyWithValue("ref_text", "backend mutation"))
		Expect(backendRecorder.requests[1].Params).To(HaveKeyWithValue("ref_text", "backend mutation"))
		Expect(wrapped.ttsParams).To(HaveKeyWithValue("ref_text", "Original transcript"))
	})

	It("forwards a copied transcript parameter map to streaming requests", func() {
		err := wrapped.TTSStream(context.Background(), "one", "voice.wav", "en", func([]byte, int) error { return nil })
		Expect(err).NotTo(HaveOccurred())

		Expect(backendRecorder.requests).To(HaveLen(1))
		Expect(backendRecorder.requests[0].Params).To(HaveKeyWithValue("ref_text", "backend mutation"))
		Expect(wrapped.ttsParams).To(HaveKeyWithValue("ref_text", "Original transcript"))
	})
})

func ptrTo[T any](value T) *T { return &value }
