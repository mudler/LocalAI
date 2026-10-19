// SPDX-License-Identifier: MIT
package elevenlabs_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

func TestSoundExpansion(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Sound Expansion HTTP") }

type soundBackend struct {
	grpcpkg.Backend
	output     string
	failure    error
	cancel     context.CancelFunc
	prediction *pb.PredictOptions
	audio      *pb.SoundGenerationRequest
	ctx        context.Context
}

func (b *soundBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *soundBackend) IsBusy() bool                              { return false }
func (b *soundBackend) ModelMetadata(context.Context, *pb.ModelOptions, ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return &pb.ModelMetadataResponse{}, nil
}
func (b *soundBackend) Predict(ctx context.Context, in *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.Reply, error) {
	b.prediction = in
	b.ctx = ctx
	if b.cancel != nil {
		b.cancel()
		return nil, ctx.Err()
	}
	return &pb.Reply{Message: []byte(b.output)}, b.failure
}
func (b *soundBackend) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.audio = in
	b.ctx = ctx
	if err := os.WriteFile(in.Dst, []byte("fLaC test"), 0600); err != nil {
		return nil, err
	}
	return &pb.Result{Success: true}, nil
}

var _ = Describe("Sound Expansion HTTP", func() {
	var (
		fake            *soundBackend
		cl              *config.ModelConfigLoader
		ml              *model.ModelLoader
		app             *config.ApplicationConfig
		audioCfg        config.ModelConfig
		chatCfg         config.ModelConfig
		input           schema.ElevenLabsSoundGenerationRequest
		ctx             context.Context
		cancel          context.CancelFunc
		loadError       error
		loadedContext   context.Context
		cancelAudioLoad bool
	)
	BeforeEach(func() {
		fake = &soundBackend{output: `{"caption":"warm piano","lyrics":"generated words","language":"en","bpm":120}`}
		app = config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		app.GeneratedContentDir = GinkgoT().TempDir()
		audioCfg = config.ModelConfig{Name: "audio", Backend: "stub"}
		audioCfg.SetDefaults()
		audioCfg.Model = "audio-weights"
		chatCfg = config.ModelConfig{Name: "chat", Backend: "stub"}
		chatCfg.SetDefaults()
		chatCfg.Model = "chat-weights"
		flags := config.FLAG_CHAT
		chatCfg.KnownUsecases = &flags
		cl = config.NewModelConfigLoader("")
		cl.ReplaceModelConfigs([]config.ModelConfig{audioCfg, chatCfg})
		ml = model.NewModelLoader(&system.SystemState{})
		loadError = nil
		cancelAudioLoad = false
		loadedContext = nil
		ml.SetModelRouter(func(ctx context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			loadedContext = ctx
			if cancelAudioLoad && id == "audio" {
				cancel()
			}
			if loadError != nil {
				return nil, loadError
			}
			return model.NewModelWithClient(id, "test://sound", fake), nil
		})
		name := "chat"
		input = schema.ElevenLabsSoundGenerationRequest{ModelID: "audio", Text: "a quiet piano song", ExpansionModel: &name}
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})
	request := func() error {
		data, err := json.Marshal(input)
		Expect(err).NotTo(HaveOccurred())
		req := httptest.NewRequest("POST", "/v1/sound-generation", strings.NewReader(string(data))).WithContext(ctx)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		var decoded schema.ElevenLabsSoundGenerationRequest
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &decoded)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &audioCfg)
		return elevenlabs.SoundGenerationEndpoint(cl, ml, app, nil)(c)
	}
	It("renders the selected chat model's prompt template", func() {
		chatCfg.TemplateConfig.Chat = "BEGIN {{.Input}} END"
		chatCfg.TemplateConfig.ChatMessage = "{{.RoleName}}:{{.Content}}"
		cl.ReplaceModelConfigs([]config.ModelConfig{audioCfg, chatCfg})
		Expect(request()).To(Succeed())
		Expect(fake.prediction.Prompt).To(HavePrefix("BEGIN user:"))
		Expect(fake.prediction.Prompt).To(ContainSubstring(input.Text))
	})
	It("passes generated neutral fields and marker to audio without mutating chat limits", func() {
		before, _ := cl.GetModelConfig("chat")
		originalTokens := *before.Maxtokens
		originalTemp := *before.Temperature
		instrumental := true
		input.Instrumental = &instrumental
		input.VocalLanguage = "it"
		input.Params = map[string]string{"style": "wrong", "lyrics": "wrong", "caption": "wrong", "other": "keep"}
		Expect(request()).To(Succeed())
		Expect(fake.prediction.Tokens).To(Equal(int32(2048)))
		Expect(fake.audio.GetExpanded()).To(BeTrue())
		Expect(fake.audio.Text).To(BeEmpty())
		Expect(fake.audio.GetCaption()).To(Equal("warm piano"))
		Expect(fake.audio.Lyrics).NotTo(BeNil())
		Expect(fake.audio.GetLyrics()).To(BeEmpty())
		Expect(fake.audio.GetLanguage()).To(Equal("it"))
		Expect(fake.audio.GetBpm()).To(Equal(int32(120)))
		Expect(fake.audio.Params).To(Equal(map[string]string{"other": "keep"}))
		Expect(loadedContext.Err()).NotTo(HaveOccurred())
		Expect(fake.ctx.Err()).NotTo(HaveOccurred())
		after, _ := cl.GetModelConfig("chat")
		Expect(*after.Maxtokens).To(Equal(originalTokens))
		Expect(*after.Temperature).To(Equal(originalTemp))
	})
	It("preserves backend defaults and omits the marker when expansion_model is absent", func() {
		input.ExpansionModel = nil
		Expect(request()).To(Succeed())
		Expect(fake.prediction).To(BeNil())
		Expect(fake.audio.Text).To(Equal(input.Text))
		Expect(fake.audio.Expanded).To(BeNil())
		Expect(fake.audio.GetExpanded()).To(BeFalse())
	})
	It("preserves generated lyrics and explicit primary language", func() {
		input.Language = "fr"
		input.VocalLanguage = "it"
		input.Lyrics = "old"
		input.Caption = "old"
		Expect(request()).To(Succeed())
		Expect(fake.audio.GetLyrics()).To(Equal("generated words"))
		Expect(fake.audio.GetLanguage()).To(Equal("fr"))
	})
	It("keeps generated empty lyrics authoritative", func() {
		fake.output = `{"caption":"warm piano","lyrics":""}`
		input.Params = map[string]string{"lyrics": "bad", "style": "bad"}
		Expect(request()).To(Succeed())
		Expect(fake.audio.Lyrics).NotTo(BeNil())
		Expect(fake.audio.GetLyrics()).To(BeEmpty())
		Expect(fake.audio.Params).To(BeEmpty())
	})
	It("rejects unknown models before chat or audio", func() {
		name := "missing"
		input.ExpansionModel = &name
		Expect(request()).To(MatchError(ContainSubstring("not installed")))
		Expect(fake.prediction).To(BeNil())
		Expect(fake.audio).To(BeNil())
	})
	It("rejects non-chat models before chat or audio", func() {
		name := "audio"
		input.ExpansionModel = &name
		flags := config.FLAG_SOUND_GENERATION
		audioCfg.KnownUsecases = &flags
		cl.ReplaceModelConfigs([]config.ModelConfig{audioCfg})
		Expect(request()).To(MatchError(ContainSubstring("does not support chat")))
		Expect(fake.audio).To(BeNil())
	})
	It("rejects empty explicit model or text", func() {
		empty := ""
		input.ExpansionModel = &empty
		Expect(request()).To(MatchError(ContainSubstring("must name")))
		name := "chat"
		input.ExpansionModel = &name
		input.Text = "  "
		Expect(request()).To(MatchError(ContainSubstring("text is required")))
		Expect(fake.audio).To(BeNil())
	})
	It("stops on model load failures", func() {
		loadError = errors.New("load failed")
		Expect(request()).To(MatchError(ContainSubstring("could not load expansion_model")))
		Expect(fake.audio).To(BeNil())
	})
	It("preserves scheduler admission errors for the HTTP error handler", func() {
		fake.failure = &backend.BackendAdmissionError{Limit: 1}
		var admission *backend.BackendAdmissionError
		Expect(errors.As(request(), &admission)).To(BeTrue())
		Expect(fake.audio).To(BeNil())
	})
	It("stops on inference failures", func() {
		fake.failure = errors.New("generation failed")
		Expect(request()).To(MatchError(ContainSubstring("failed to generate JSON")))
		Expect(fake.audio).To(BeNil())
	})
	It("stops on invalid JSON or schema", func() {
		for _, output := range []string{"not JSON", `{"caption":"piano","lyrics":"","unexpected":true}`} {
			fake.output = output
			Expect(request()).To(MatchError(ContainSubstring("invalid sound JSON")))
			Expect(fake.audio).To(BeNil())
		}
	})
	It("does not load or generate after cancellation", func() {
		cancel()
		Expect(request()).To(MatchError(ContainSubstring("context canceled")))
		Expect(loadedContext).To(BeNil())
		Expect(fake.audio).To(BeNil())
	})
	It("does not call audio inference when cancellation arrives during audio load", func() {
		cancelAudioLoad = true
		Expect(request()).To(MatchError(ContainSubstring("context canceled")))
		Expect(fake.audio).To(BeNil())
	})
	It("propagates inference cancellation and never starts audio", func() {
		fake.cancel = cancel
		Expect(request()).To(MatchError(ContainSubstring("context canceled")))
		Expect(fake.ctx.Err()).To(MatchError(context.Canceled))
		Expect(fake.audio).To(BeNil())
	})
})
