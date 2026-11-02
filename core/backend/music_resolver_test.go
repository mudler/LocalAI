// SPDX-License-Identifier: MIT
package backend_test

import (
	"context"
	"errors"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/music"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"reflect"
	"runtime"
)

type musicBinder struct {
	session backend.MusicPolicySession
	err     error
	calls   int
}

func (b *musicBinder) BindMusicPrincipal(context.Context) (backend.MusicPolicySession, error) {
	b.calls++
	return b.session, b.err
}

type musicPolicy struct {
	principal string
	calls     []string
	deny      string
}

func (p *musicPolicy) Principal() string { return p.principal }
func (p *musicPolicy) AuthorizeMusic(context.Context, music.Operation) error {
	p.calls = append(p.calls, "feature")
	if p.deny == "feature" {
		return errors.New("private feature diagnostic")
	}
	return nil
}
func (p *musicPolicy) AuthorizeMusicQuota(context.Context, music.Operation) error {
	p.calls = append(p.calls, "quota")
	if p.deny == "quota" {
		return errors.New("private quota diagnostic")
	}
	return nil
}
func (p *musicPolicy) AuthorizeMusicModel(_ context.Context, name string) error {
	p.calls = append(p.calls, name)
	if p.deny == name {
		return errors.New("private model diagnostic")
	}
	return nil
}

var _ = Describe("Music canonical-policy preflight", func() {
	var loader *config.ModelConfigLoader
	var policy *musicPolicy
	var binder *musicBinder
	var service *backend.MusicCanonicalPreflight
	BeforeEach(func() {
		loader = config.NewModelConfigLoader("")
		a := config.ModelConfig{Name: "transcriber", Backend: "audio-cpp"}
		b := config.ModelConfig{Name: "generator", Backend: "audio-cpp"}
		Expect(a.StampPersistedConfigRevision()).To(Succeed())
		Expect(b.StampPersistedConfigRevision()).To(Succeed())
		loader.ReplaceModelConfigs([]config.ModelConfig{a, b, {Name: "t-alias", Alias: "transcriber"}, {Name: "g-alias", Alias: "generator"}})
		policy = &musicPolicy{principal: "alice"}
		binder = &musicBinder{session: policy}
		var err error
		service, err = backend.NewMusicCanonicalPreflight(loader, binder)
		Expect(err).NotTo(HaveOccurred())
	})
	It("resolves strict canonical cover configs and authorizes both canonical models", func() {
		out, err := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Cover, TranscriptionModel: "t-alias", GenerationModel: "g-alias"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Transcription).NotTo(BeNil())
		Expect(out.Generation).NotTo(BeNil())
		Expect(out.Transcription.Config.Name).To(Equal("transcriber"))
		Expect(out.Generation.Config.Name).To(Equal("generator"))
		Expect(out.Generation.Revision).NotTo(BeEmpty())
		Expect(out.Policy).To(BeIdenticalTo(policy))
		Expect(policy.calls).To(Equal([]string{"feature", "quota", "transcriber", "generator"}))
	})
	It("denies failed principal binding without accepting a principal string", func() {
		binder.err = errors.New("secret authentication detail")
		out, err := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("secret"))
		Expect(out.Policy).To(BeNil())
		Expect(policy.calls).To(BeEmpty())
	})
	It("returns no partial authorization when generation access is forbidden", func() {
		policy.deny = "generator"
		out, err := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Cover, TranscriptionModel: "transcriber", GenerationModel: "generator"})
		Expect(err).To(HaveOccurred())
		Expect(out.Policy).To(BeNil())
		Expect(out.Transcription).To(BeNil())
		Expect(policy.calls).To(Equal([]string{"feature", "quota", "transcriber", "generator"}))
	})
	It("rejects disabled canonical alias targets", func() {
		disabled := true
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.Disabled = &disabled })
		_, err := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "g-alias"})
		Expect(err).To(HaveOccurred())
	})
	It("fails closed without a policy binder", func() { _, err := backend.NewMusicCanonicalPreflight(loader, nil); Expect(err).To(HaveOccurred()) })
	DescribeTable("rejects invalid aliases and routing without model authorization", func(change func(*config.ModelConfigLoader), name string) {
		change(loader)
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: name})
		Expect(e).To(HaveOccurred())
		Expect(out.Policy).To(BeNil())
		Expect(policy.calls).To(Equal([]string{"feature", "quota"}))
	},
		Entry("dangling", func(l *config.ModelConfigLoader) {
			l.UpdateModelConfig("g-alias", func(c *config.ModelConfig) { c.Alias = "missing" })
		}, "g-alias"),
		Entry("chain", func(l *config.ModelConfigLoader) {
			l.UpdateModelConfig("g-alias", func(c *config.ModelConfig) { c.Alias = "t-alias" })
		}, "g-alias"),
		Entry("disabled alias", func(l *config.ModelConfigLoader) {
			l.UpdateModelConfig("g-alias", func(c *config.ModelConfig) { v := true; c.Disabled = &v })
		}, "g-alias"),
		Entry("pipeline", func(l *config.ModelConfigLoader) {
			l.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.Pipeline.LLM = "other" })
		}, "generator"),
		Entry("failover", func(l *config.ModelConfigLoader) {
			l.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.Failover = &config.FailoverConfig{} })
		}, "generator"),
		Entry("missing backend", func(l *config.ModelConfigLoader) {
			l.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.Backend = "" })
		}, "generator"))
	DescribeTable("denies feature and quota errors without model authorization", func(deny string, calls []string) {
		policy.deny = deny
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Cover, TranscriptionModel: "transcriber", GenerationModel: "generator"})
		Expect(e).To(HaveOccurred())
		Expect(e.Error()).NotTo(ContainSubstring("private"))
		Expect(out.Policy).To(BeNil())
		Expect(policy.calls).To(Equal(calls))
	}, Entry("feature", "feature", []string{"feature"}), Entry("quota", "quota", []string{"feature", "quota"}))
	It("requires nonempty authenticated policy and refuses typed-nil dependencies", func() {
		policy.principal = ""
		_, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).To(HaveOccurred())
		var b *musicBinder
		_, e = backend.NewMusicCanonicalPreflight(loader, b)
		Expect(e).To(HaveOccurred())
		_, e = backend.NewMusicCanonicalPreflight(nil, binder)
		Expect(e).To(HaveOccurred())
	})
	It("preserves caller cancellation before binding", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, e := service.Resolve(ctx, backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).To(MatchError(context.Canceled))
		Expect(binder.calls).To(BeZero())
	})
	It("returns detached config maps slices and pointers", func() {
		size := 128
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) {
			c.ContextSize = &size
			c.Options = []string{"original"}
			c.Roles = map[string]string{"user": "original"}
		})
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "g-alias"})
		Expect(e).NotTo(HaveOccurred())
		out.Generation.Config.Options[0] = "changed"
		out.Generation.Config.Roles["user"] = "changed"
		*out.Generation.Config.ContextSize = 1
		actual, ok := loader.GetModelConfig("generator")
		Expect(ok).To(BeTrue())
		Expect(actual.Options).To(Equal([]string{"original"}))
		Expect(actual.Roles["user"]).To(Equal("original"))
		Expect(*actual.ContextSize).To(Equal(128))
	})
	It("does not mistake missing persisted revision for runtime evidence", func() {
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) { *c = config.ModelConfig{Name: "generator", Backend: "audio-cpp"} })
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Generation.Revision).To(BeEmpty())
	})
	DescribeTable("accepts exactly the selected operation models", func(op music.Operation, t, g string) {
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: op, TranscriptionModel: t, GenerationModel: g})
		Expect(e).NotTo(HaveOccurred())
		if op == music.Generation {
			Expect(out.Transcription).To(BeNil())
		}
		if op == music.Transcription {
			Expect(out.Generation).To(BeNil())
		}
	}, Entry("transcription", music.Transcription, "t-alias", ""), Entry("generation", music.Generation, "", "g-alias"))
	DescribeTable("rejects ambiguous operation requests before authentication", func(op music.Operation, t, g string) {
		_, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: op, TranscriptionModel: t, GenerationModel: g})
		Expect(e).To(HaveOccurred())
		Expect(binder.calls).To(BeZero())
	}, Entry("unknown", music.Operation(99), "", "generator"), Entry("generation with transcription", music.Generation, "transcriber", "generator"), Entry("cover missing second", music.Cover, "transcriber", ""))

	It("preserves typed policy unavailability decisions without their private messages", func() {
		service, e := backend.NewMusicCanonicalPreflight(loader, musicErrorBinder{})
		Expect(e).NotTo(HaveOccurred())
		_, e = service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).To(HaveOccurred())
		var public interface {
			HTTPStatus() int
			MusicReason() string
		}
		Expect(errors.As(e, &public)).To(BeTrue())
		Expect(public.HTTPStatus()).To(Equal(503))
		Expect(public.MusicReason()).To(Equal("service_unavailable"))
		Expect(e.Error()).NotTo(ContainSubstring("private"))
	})

	It("does not serialize unrelated model configuration", func() {
		configs := loader.GetAllModelsConfigs()
		configs = append(configs, config.ModelConfig{Name: "unrelated", Backend: "other", ChatTemplateKwargs: map[string]any{"unsupported": make(chan int)}})
		loader.ReplaceModelConfigs(configs)
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "g-alias"})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Generation.Config.Name).To(Equal("generator"))
	})

	It("preserves nested large integer concrete types and alias overlays without shared mutation", func() {
		const big int64 = 9007199254740993
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) {
			c.ChatTemplateKwargs = map[string]any{"nested": []any{map[string]any{"integer": big, "unsigned": uint64(big), "small": int32(17)}}}
			c.EngineArgs = map[string]any{"nested": map[string]any{"integer": big}}
		})
		original, _ := loader.GetModelConfig("generator")
		revision := original.PersistedConfigRevision()
		loader.UpdateModelConfig("g-alias", func(c *config.ModelConfig) {
			c.Profile.SystemPrompt = "alias prompt"
			c.Profile.MCP.Servers = "alias MCP"
			c.Profile.EvidenceCheck.Numbers = true
		})
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "g-alias"})
		Expect(e).NotTo(HaveOccurred())
		cfg := &out.Generation.Config
		nested := cfg.ChatTemplateKwargs["nested"].([]any)[0].(map[string]any)
		Expect(nested["integer"]).To(Equal(big))
		Expect(nested["unsigned"]).To(Equal(uint64(big)))
		Expect(nested["small"]).To(Equal(int32(17)))
		Expect(cfg.EngineArgs["nested"].(map[string]any)["integer"]).To(Equal(big))
		Expect(out.Generation.Revision).To(Equal(revision))
		Expect(cfg.SystemPrompt).To(Equal("alias prompt"))
		Expect(cfg.MCP.Servers).To(Equal("alias MCP"))
		Expect(cfg.EvidenceCheck().Numbers).To(BeTrue())
		nested["integer"] = int64(1)
		cfg.EngineArgs["nested"].(map[string]any)["integer"] = int64(2)
		cfg.MCP.Servers = "changed"
		cfg.Profile.SystemPrompt = "changed"
		actual, _ := loader.GetModelConfig("generator")
		alias, _ := loader.GetModelConfig("g-alias")
		Expect(actual.ChatTemplateKwargs["nested"].([]any)[0].(map[string]any)["integer"]).To(Equal(big))
		Expect(actual.EngineArgs["nested"].(map[string]any)["integer"]).To(Equal(big))
		Expect(actual.MCP.Servers).To(BeEmpty())
		Expect(alias.Profile.MCP.Servers).To(Equal("alias MCP"))
		Expect(alias.Profile.SystemPrompt).To(Equal("alias prompt"))
	})
	DescribeTable("fails closed on selected cyclic or runtime configuration", func(value any) {
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.EngineArgs = map[string]any{"bad": value} })
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).To(HaveOccurred())
		Expect(out.Policy).To(BeNil())
	}, Entry("channel", make(chan int)), Entry("function", func() {}), Entry("cycle", func() any { m := map[string]any{}; m["self"] = m; return m }()))

	It("rejects excessive nesting and preserves nil versus empty containers", func() {
		var nested any = "leaf"
		for i := 0; i < 70; i++ {
			nested = []any{nested}
		}
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.EngineArgs = map[string]any{"deep": nested} })
		_, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).To(HaveOccurred())
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.EngineArgs = map[string]any{"nil": []int64(nil), "empty": []int64{}} })
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Generation.Config.EngineArgs["nil"]).To(BeNil())
		Expect(out.Generation.Config.EngineArgs["empty"]).NotTo(BeNil())
		Expect(out.Generation.Config.EngineArgs["empty"]).To(BeEmpty())
	})

	It("preserves defined pointer types and detaches their pointees", func() {
		value := 37
		var pointer musicDefinedPointer = &value
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.EngineArgs = map[string]any{"pointer": pointer} })
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		Expect(e).NotTo(HaveOccurred())
		cloned, ok := out.Generation.Config.EngineArgs["pointer"].(musicDefinedPointer)
		Expect(ok).To(BeTrue())
		Expect(*cloned).To(Equal(37))
		*cloned = 99
		Expect(value).To(Equal(37))
	})
	It("rejects a large concrete array before allocating its clone", func() {
		large := reflect.New(reflect.ArrayOf(20<<20, reflect.TypeOf(byte(0)))).Elem().Interface()
		loader.UpdateModelConfig("generator", func(c *config.ModelConfig) { c.EngineArgs = map[string]any{"large": large} })
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		out, e := service.Resolve(context.Background(), backend.MusicModelRequest{Operation: music.Generation, GenerationModel: "generator"})
		runtime.ReadMemStats(&after)
		Expect(e).To(HaveOccurred())
		Expect(out.Policy).To(BeNil())
		Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 2<<20))
		runtime.KeepAlive(large)
	})

})

type musicErrorBinder struct{}

func (musicErrorBinder) BindMusicPrincipal(context.Context) (backend.MusicPolicySession, error) {
	return nil, musicPublicFailure{}
}

type musicPublicFailure struct{}

func (musicPublicFailure) Error() string       { return "private backend address" }
func (musicPublicFailure) HTTPStatus() int     { return 503 }
func (musicPublicFailure) MusicStage() string  { return "validation" }
func (musicPublicFailure) MusicReason() string { return "service_unavailable" }

type musicDefinedPointer *int
