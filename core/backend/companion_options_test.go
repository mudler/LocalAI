package backend

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

// A companion snapshot only becomes useful once the backend can find it, and
// its location is a content-addressed cache key that is unknowable until the
// artifact resolves. A static gallery override therefore cannot carry it. The
// path is instead synthesized into ModelOptions.Options at load time, under the
// companion's own name, using the same key:value convention backends already
// parse for options like attention_backend.
var _ = Describe("companion artifact backend options", func() {
	const (
		primaryKey   = "1111111111111111111111111111111111111111111111111111111111111111"
		companionKey = "2222222222222222222222222222222222222222222222222222222222222222"
	)

	resolved := func(key string) *modelartifacts.Resolved {
		return &modelartifacts.Resolved{
			Endpoint: "https://huggingface.co",
			Revision: "0123456789abcdef0123456789abcdef01234567",
			CacheKey: key,
		}
	}

	threads := 1
	configWithCompanion := func(options ...string) config.ModelConfig {
		return config.ModelConfig{
			Backend: "longcat-video",
			Options: options,
			Threads: &threads,
			Artifacts: []modelartifacts.Spec{
				{
					Name: modelartifacts.TargetModel, Target: modelartifacts.TargetModel,
					Source:   modelartifacts.Source{Type: "huggingface", Repo: "meituan-longcat/LongCat-Video-Avatar-1.5", Revision: "main"},
					Resolved: resolved(primaryKey),
				},
				{
					Name: "base_model", Target: modelartifacts.TargetCompanion,
					Source:   modelartifacts.Source{Type: "huggingface", Repo: "meituan-longcat/LongCat-Video", Revision: "main"},
					Resolved: resolved(companionKey),
				},
			},
		}
	}

	optionValue := func(options []string, key string) (string, bool) {
		for _, option := range options {
			name, value, found := strings.Cut(option, ":")
			if found && name == key {
				return value, true
			}
		}
		return "", false
	}

	It("exposes a resolved companion snapshot as an option named after the artifact", func() {
		opts := grpcModelOpts(configWithCompanion("attention_backend:sdpa"), "/models")

		value, found := optionValue(opts.Options, "base_model")
		Expect(found).To(BeTrue())
		expected, err := modelartifacts.RelativeSnapshotPath(companionKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal(expected))
		// The companion path must never be confused with the load target.
		Expect(value).ToNot(ContainSubstring(primaryKey))
		// Author-supplied options survive untouched.
		Expect(opts.Options).To(ContainElement("attention_backend:sdpa"))
	})

	It("keeps the path relative so the worker resolves it under its own ModelPath", func() {
		opts := grpcModelOpts(configWithCompanion(), "/models")

		value, found := optionValue(opts.Options, "base_model")
		Expect(found).To(BeTrue())
		Expect(strings.HasPrefix(value, "/")).To(BeFalse())
	})

	It("does not override an explicitly configured option of the same name", func() {
		// An operator pinning base_model to a local checkout must win over the
		// synthesized value.
		opts := grpcModelOpts(configWithCompanion("base_model:/opt/checkouts/LongCat-Video"), "/models")

		Expect(opts.Options).To(ContainElement("base_model:/opt/checkouts/LongCat-Video"))
		values := 0
		for _, option := range opts.Options {
			if strings.HasPrefix(option, "base_model:") {
				values++
			}
		}
		Expect(values).To(Equal(1))
	})

	It("synthesizes nothing for a config without companions", func() {
		cfg := config.ModelConfig{
			Backend: "transformers",
			Options: []string{"attention_backend:sdpa"},
			Threads: &threads,
			Artifacts: []modelartifacts.Spec{{
				Name: modelartifacts.TargetModel, Target: modelartifacts.TargetModel,
				Source:   modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
				Resolved: resolved(primaryKey),
			}},
		}
		opts := grpcModelOpts(cfg, "/models")
		Expect(opts.Options).To(Equal([]string{"attention_backend:sdpa"}))
	})

	It("names the source repository when the companion is not resolved yet", func() {
		// A companion that reaches load time WITHOUT a resolved snapshot must not
		// vanish silently: emitting no option lets the backend fall back to its own
		// hardcoded default, which is how a distributed longcat-video worker ended
		// up trying to load the wrong base model and failing "base_model must point
		// to a LongCat-Video checkpoint". Naming the DECLARED repository instead
		// points the backend at the artifact the config actually asked for. The
		// snapshot path (the staged, no-download fast path) is still preferred
		// whenever the companion IS resolved.
		cfg := configWithCompanion()
		cfg.Artifacts[1].Resolved = nil
		opts := grpcModelOpts(cfg, "/models")

		value, found := optionValue(opts.Options, "base_model")
		Expect(found).To(BeTrue())
		Expect(value).To(Equal("meituan-longcat/LongCat-Video"))
		// The fallback is a repo reference, never a models-relative snapshot path.
		Expect(value).ToNot(ContainSubstring(".artifacts"))
	})

	It("prefers the resolved snapshot path over the source repository", func() {
		opts := grpcModelOpts(configWithCompanion(), "/models")
		value, found := optionValue(opts.Options, "base_model")
		Expect(found).To(BeTrue())
		expected, err := modelartifacts.RelativeSnapshotPath(companionKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal(expected))
		// The resolved fast path must never degrade to a bare repo id.
		Expect(value).ToNot(Equal("meituan-longcat/LongCat-Video"))
	})
})
