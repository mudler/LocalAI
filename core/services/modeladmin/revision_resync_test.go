package modeladmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
)

// stubRevisionStore stands in for the controller's stored revisions.
type stubRevisionStore struct {
	stored  map[string]string
	getErr  error
	applied []ModelRevisionTransition
	applyEr error
}

func (s *stubRevisionStore) GetModelConfigRevision(_ context.Context, name string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	rev, ok := s.stored[name]
	if !ok {
		return "", ErrNoStoredRevision
	}
	return rev, nil
}

func (s *stubRevisionStore) ApplyConfigRevisions(_ context.Context, t []ModelRevisionTransition) (int, error) {
	s.applied = append(s.applied, t...)
	return 0, s.applyEr
}

// The controller pins a model's replicas to a stored revision and rejects any
// request carrying a different one. Nothing ever re-derived that stored value
// from the configuration on disk: it only moved on an edit, a gallery install
// or a peer's change event. So whenever the stored value stopped matching what
// this build computes for an unchanged file, every request for that model was
// rejected until an operator deleted the row by hand.
var _ = Describe("ResyncModelConfigRevisions", func() {
	var (
		dir       string
		loader    *config.ModelConfigLoader
		store     *stubRevisionStore
		appConfig *config.ApplicationConfig
	)

	write := func(name, body string) {
		Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
	}

	// revisionOf resolves the revision the way an inference request does, which
	// is the value the resync must publish.
	revisionOf := func(name string) string {
		cfg, err := loader.LoadModelConfigFileByNameDefaultOptions(name, appConfig)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.PersistedConfigRevision()).ToNot(BeEmpty())
		return cfg.PersistedConfigRevision()
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		appConfig = config.NewApplicationConfig()
		appConfig.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		loader = config.NewModelConfigLoader(dir)
		store = &stubRevisionStore{stored: map[string]string{}}
	})

	load := func() {
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
	}

	It("republishes the revision when the stored one no longer matches the config on disk", func() {
		write("drifted", "name: drifted\nbackend: llama-cpp\ncontext_size: 4096\n")
		load()
		store.stored["drifted"] = "a-revision-from-an-earlier-build"

		Expect(ResyncModelConfigRevisions(context.Background(), loader, appConfig, store)).To(Succeed())

		Expect(store.applied).To(HaveLen(1))
		Expect(store.applied[0].ModelName).To(Equal("drifted"))
		Expect(store.applied[0].ConfigRevision).To(Equal(revisionOf("drifted")))
	})

	It("leaves a model alone when the stored revision already matches", func() {
		write("agreed", "name: agreed\nbackend: llama-cpp\n")
		load()
		store.stored["agreed"] = revisionOf("agreed")

		Expect(ResyncModelConfigRevisions(context.Background(), loader, appConfig, store)).To(Succeed())

		Expect(store.applied).To(BeEmpty(), "republishing an unchanged revision would quarantine live replicas for nothing")
	})

	// A model nobody has served has no stored revision. Creating one here would
	// invent controller state for a model that may never be requested; the first
	// request establishes it.
	It("does not create state for a model that has never been served", func() {
		write("never-served", "name: never-served\nbackend: llama-cpp\n")
		load()

		Expect(ResyncModelConfigRevisions(context.Background(), loader, appConfig, store)).To(Succeed())

		Expect(store.applied).To(BeEmpty())
	})

	It("republishes only the models that actually drifted", func() {
		write("drifted", "name: drifted\nbackend: llama-cpp\n")
		write("agreed", "name: agreed\nbackend: llama-cpp\ncontext_size: 2048\n")
		load()
		store.stored["drifted"] = "stale"
		store.stored["agreed"] = revisionOf("agreed")

		Expect(ResyncModelConfigRevisions(context.Background(), loader, appConfig, store)).To(Succeed())

		Expect(store.applied).To(HaveLen(1))
		Expect(store.applied[0].ModelName).To(Equal("drifted"))
	})

	It("reports a store failure instead of continuing silently", func() {
		write("drifted", "name: drifted\nbackend: llama-cpp\n")
		load()
		store.stored["drifted"] = "stale"
		store.applyEr = errors.New("database is down")

		Expect(ResyncModelConfigRevisions(context.Background(), loader, appConfig, store)).ToNot(Succeed())
	})

	It("skips a model whose stored revision cannot be read rather than guessing", func() {
		write("unreadable", "name: unreadable\nbackend: llama-cpp\n")
		load()
		store.getErr = errors.New("connection reset")

		Expect(ResyncModelConfigRevisions(context.Background(), loader, appConfig, store)).ToNot(Succeed())
		Expect(store.applied).To(BeEmpty())
	})
})

// Running the resync before the model configs are loaded reconciled nothing
// while reporting success, which is how a mis-ordered startup call went
// unnoticed. An empty loader is now called out instead of looking like a
// clean run.
var _ = Describe("ResyncModelConfigRevisions with nothing loaded", func() {
	It("does not touch stored revisions when no configs are loaded", func() {
		dir := GinkgoT().TempDir()
		loader := config.NewModelConfigLoader(dir)
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		store := &stubRevisionStore{stored: map[string]string{"served-before": "stale"}}

		Expect(ResyncModelConfigRevisions(context.Background(), loader, appConfig, store)).To(Succeed())
		Expect(store.applied).To(BeEmpty())
	})
})
