package openai

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
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

var _ = Describe("realtime session voice switching", func() {
	type fixture struct {
		store     *voiceprofile.Store
		voiceDir  string
		loader    *config.ModelConfigLoader
		models    *model.ModelLoader
		appConfig *config.ApplicationConfig
		profileA  voiceprofile.Profile
		profileB  voiceprofile.Profile
	}

	newFixture := func(ctx SpecContext) *fixture {
		modelDir := GinkgoT().TempDir()
		voiceDir := GinkgoT().TempDir()
		store := voiceprofile.NewStore(voiceDir)
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
		profileA, err := store.Create(ctx, voiceprofile.CreateInput{
			Name: "Alpha", Language: "en", Transcript: "Alpha transcript", ConsentConfirmed: true,
		}, bytes.NewReader(realtimeProfileWAV(time.Second)))
		Expect(err).NotTo(HaveOccurred())
		profileB, err := store.Create(ctx, voiceprofile.CreateInput{
			Name: "Beta", Language: "it", Transcript: "Beta transcript", ConsentConfirmed: true,
		}, bytes.NewReader(realtimeProfileWAV(time.Second)))
		Expect(err).NotTo(HaveOccurred())

		configs := map[string]string{
			"vad":    "name: vad\nbackend: test\nparameters:\n  model: vad.bin\n",
			"stt":    "name: stt\nbackend: test\nparameters:\n  model: stt.bin\n",
			"llm":    "name: llm\nbackend: test\nparameters:\n  model: llm.bin\n",
			"tts-a":  fmt.Sprintf("name: tts-a\nbackend: qwen3-tts-cpp\nparameters:\n  model: tts-a.bin\ntts:\n  voice: %s\n  voice_cloning: true\n", profileA.Voice),
			"tts-b":  fmt.Sprintf("name: tts-b\nbackend: qwen3-tts-cpp\nparameters:\n  model: tts-b.bin\ntts:\n  voice: %s\n  voice_cloning: true\n", profileB.Voice),
			"pipe-a": "name: pipe-a\npipeline:\n  vad: vad\n  transcription: stt\n  llm: llm\n  tts: tts-a\n  disable_warmup: true\n",
			"pipe-b": "name: pipe-b\npipeline:\n  vad: vad\n  transcription: stt\n  llm: llm\n  tts: tts-b\n  disable_warmup: true\n",
		}
		for name, body := range configs {
			Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
		}
		loader := config.NewModelConfigLoader(modelDir)
		Expect(loader.LoadModelConfigsFromPath(modelDir)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(modelDir))
		Expect(err).NotTo(HaveOccurred())
		return &fixture{
			store: store, voiceDir: voiceDir, loader: loader, models: model.NewModelLoader(state),
			appConfig: config.NewApplicationConfig(config.WithSystemState(state)),
			profileA:  profileA, profileB: profileB,
		}
	}

	newSession := func(f *fixture, voice string) *Session {
		cfg, err := f.loader.LoadModelConfigFileByNameDefaultOptions("pipe-a", f.appConfig)
		Expect(err).NotTo(HaveOccurred())
		m, err := newModel(&cfg.Pipeline, f.loader, f.models, f.appConfig, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		session := &Session{
			Model: "pipe-a", Voice: voice, ModelConfig: cfg, ModelInterface: m,
			InputAudioTranscription: &types.AudioTranscription{Model: "stt"},
		}
		resolved, params, release, err := resolveRealtimeVoice(context.Background(), voice, m.(*wrappedModel).TTSConfig, f.store)
		Expect(err).NotTo(HaveOccurred())
		session.installVoiceBinding(resolved, params, release)
		return session
	}

	update := func(f *fixture, session *Session, rt *types.RealtimeSession) error {
		return updateSession(session, &types.SessionUnion{Realtime: rt}, f.loader, f.models, f.appConfig, nil, nil, f.store)
	}

	It("switches ordinary voices to profiles and clears the lease at final cleanup", func(ctx SpecContext) {
		f := newFixture(ctx)
		session := newSession(f, "speaker-1")
		Expect(update(f, session, &types.RealtimeSession{Audio: &types.RealtimeSessionAudio{Output: &types.SessionAudioOutput{Voice: types.Voice(f.profileA.Voice)}}})).To(Succeed())

		Expect(session.Voice).To(BeAnExistingFile())
		Expect(session.ttsParams).To(Equal(map[string]string{"ref_text": "Alpha transcript"}))
		leased := session.Voice
		session.releaseVoiceBinding()
		session.releaseVoiceBinding()
		Expect(leased).NotTo(BeAnExistingFile())
	})

	It("replaces one profile lease with another and then an ordinary voice", func(ctx SpecContext) {
		f := newFixture(ctx)
		session := newSession(f, f.profileA.Voice)
		firstLease := session.Voice

		Expect(update(f, session, &types.RealtimeSession{Audio: &types.RealtimeSessionAudio{Output: &types.SessionAudioOutput{Voice: types.Voice(f.profileB.Voice)}}})).To(Succeed())
		Expect(firstLease).NotTo(BeAnExistingFile())
		secondLease := session.Voice
		Expect(secondLease).To(BeAnExistingFile())
		Expect(session.ttsParams).To(HaveKeyWithValue("ref_text", "Beta transcript"))

		Expect(update(f, session, &types.RealtimeSession{Audio: &types.RealtimeSessionAudio{Output: &types.SessionAudioOutput{Voice: "speaker-2"}}})).To(Succeed())
		Expect(secondLease).NotTo(BeAnExistingFile())
		Expect(session.Voice).To(Equal("speaker-2"))
		Expect(session.ttsParams).To(BeNil())
		Expect(session.ModelInterface.(*wrappedModel).ttsParams).To(BeNil())
	})

	It("uses a new model default profile unless an explicit voice takes precedence", func(ctx SpecContext) {
		f := newFixture(ctx)
		session := newSession(f, "speaker-1")
		Expect(update(f, session, &types.RealtimeSession{Model: "pipe-b"})).To(Succeed())
		Expect(session.ttsParams).To(HaveKeyWithValue("ref_text", "Beta transcript"))
		defaultLease := session.Voice

		Expect(update(f, session, &types.RealtimeSession{
			Model: "pipe-a",
			Audio: &types.RealtimeSessionAudio{Output: &types.SessionAudioOutput{Voice: "speaker-explicit"}},
		})).To(Succeed())
		Expect(defaultLease).NotTo(BeAnExistingFile())
		Expect(session.Voice).To(Equal("speaker-explicit"))
		Expect(session.ttsParams).To(BeNil())
	})

	It("preserves a profile binding across a language-only rebuild", func(ctx SpecContext) {
		f := newFixture(ctx)
		session := newSession(f, f.profileA.Voice)
		lease := session.Voice
		Expect(update(f, session, &types.RealtimeSession{Audio: &types.RealtimeSessionAudio{Input: &types.SessionAudioInput{
			Transcription: &types.AudioTranscription{Language: "fr"},
		}}})).To(Succeed())

		Expect(session.Voice).To(Equal(lease))
		Expect(session.InputAudioTranscription.Model).To(Equal("stt"))
		Expect(session.InputAudioTranscription.Language).To(Equal("fr"))
		wrapped := session.ModelInterface.(*wrappedModel)
		Expect(wrapped.ttsParams).To(Equal(map[string]string{"ref_text": "Alpha transcript"}))
		wrapped.ttsParams["ref_text"] = "wrapper mutation"
		Expect(session.ttsParams).To(HaveKeyWithValue("ref_text", "Alpha transcript"))
	})

	It("rolls back the model, wrapper, voice, and lease when preparation fails", func(ctx SpecContext) {
		f := newFixture(ctx)
		session := newSession(f, f.profileA.Voice)
		oldModel, oldConfig, oldVoice := session.ModelInterface, session.ModelConfig, session.Voice
		err := update(f, session, &types.RealtimeSession{
			Model: "pipe-b",
			Audio: &types.RealtimeSessionAudio{Output: &types.SessionAudioOutput{Voice: "localai://voice-profiles/00000000-0000-0000-0000-000000000001"}},
		})

		Expect(err).To(HaveOccurred())
		Expect(session.Model).To(Equal("pipe-a"))
		Expect(session.ModelConfig).To(BeIdenticalTo(oldConfig))
		Expect(session.ModelInterface).To(BeIdenticalTo(oldModel))
		Expect(session.Voice).To(Equal(oldVoice))
		Expect(oldVoice).To(BeAnExistingFile())
		Expect(session.ttsParams).To(HaveKeyWithValue("ref_text", "Alpha transcript"))
	})

	It("releases a candidate profile lease when later validation fails", func(ctx SpecContext) {
		f := newFixture(ctx)
		session := newSession(f, "speaker-1")
		leases := func() []string {
			matches, err := filepath.Glob(filepath.Join(f.voiceDir, voiceprofile.DirectoryName, ".leases", "*", "*.wav"))
			Expect(err).NotTo(HaveOccurred())
			return matches
		}
		Expect(leases()).To(BeEmpty())

		err := update(f, session, &types.RealtimeSession{
			Audio:             &types.RealtimeSessionAudio{Output: &types.SessionAudioOutput{Voice: types.Voice(f.profileA.Voice)}},
			LocalAIClassifier: classifierTestConfig(0, nil),
		})

		Expect(err).To(HaveOccurred())
		Expect(session.Voice).To(Equal("speaker-1"))
		Expect(leases()).To(BeEmpty())
	})
})

func ptrTo[T any](value T) *T { return &value }
