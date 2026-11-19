package application

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/model"
)

// fakeReplicas is the registry of a cluster: the nodes that run each model.
// A shutdown through fakeLoader stops what stop allows.
type fakeReplicas struct {
	running map[string][]nodes.BackendNode
	err     error
}

func (f *fakeReplicas) FindNodesWithModel(_ context.Context, name string) ([]nodes.BackendNode, error) {
	return f.running[name], f.err
}

type fakeLoader struct {
	replicas    *fakeReplicas
	shutdownErr error
	stopAll     bool
	shutdowns   int
}

func (f *fakeLoader) ShutdownModel(name string) error {
	f.shutdowns++
	if f.stopAll {
		delete(f.replicas.running, name)
	}
	return f.shutdownErr
}

func (f *fakeLoader) ListLoadedModels() []*model.Model { return nil }

type fakeRules struct {
	rule *nodes.ModelSchedulingConfig
	err  error
}

func (f fakeRules) GetGoverningScheduling(context.Context, string) (*nodes.ModelSchedulingConfig, error) {
	return f.rule, f.err
}

var _ = Describe("the stop-and-start runtime of a cluster", func() {
	var (
		replicas *fakeReplicas
		loader   *fakeLoader
		rt       *clusterRuntime
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		replicas = &fakeReplicas{running: map[string][]nodes.BackendNode{
			"tiny": {{ID: "a", Name: "worker-a"}, {ID: "b", Name: "worker-b"}},
		}}
		loader = &fakeLoader{replicas: replicas, stopAll: true}
		rt = newClusterRuntime(&modelRuntime{}, replicas, loader)
	})

	It("reads a model as loaded when a worker runs it, though this frontend does not", func() {
		Expect(rt.IsLoaded("tiny")).To(BeTrue())
		Expect(rt.IsLoaded("other")).To(BeFalse())
	})

	It("reads a model as loaded when the registry cannot answer", func() {
		replicas.err = errors.New("database down")
		Expect(rt.IsLoaded("other")).To(BeTrue())
	})

	It("stops every replica and checks that none is left", func() {
		Expect(rt.Unload(ctx, "tiny")).To(Succeed())
		Expect(loader.shutdowns).To(Equal(1))
	})

	It("takes a model that runs nowhere as stopped", func() {
		loader.shutdownErr = model.ErrModelNotFound
		Expect(rt.Unload(ctx, "other")).To(Succeed())
	})

	It("fails when a replica is still running, so the upgrade rolls back", func() {
		loader.stopAll = false
		Expect(rt.Unload(ctx, "tiny")).To(MatchError(ContainSubstring("worker-a")))
	})

	It("fails when the shutdown fails", func() {
		loader.shutdownErr = errors.New("node did not answer")
		Expect(rt.Unload(ctx, "tiny")).To(MatchError(ContainSubstring("node did not answer")))
	})
})

var _ = Describe("the upgrade strategy note of a cluster", func() {
	It("says nothing for stop and start, a missing rule, or a registry error", func() {
		Expect(clusterStrategyNote(fakeRules{})(context.Background(), "m")).To(BeEmpty())
		Expect(clusterStrategyNote(fakeRules{rule: &nodes.ModelSchedulingConfig{UpgradeStrategy: nodes.UpgradeStrategyStopAndStart}})(context.Background(), "m")).To(BeEmpty())
		Expect(clusterStrategyNote(fakeRules{err: errors.New("down")})(context.Background(), "m")).To(BeEmpty())
	})

	It("says that canary is not available yet", func() {
		note := clusterStrategyNote(fakeRules{rule: &nodes.ModelSchedulingConfig{UpgradeStrategy: nodes.UpgradeStrategyCanary}})(context.Background(), "m")
		Expect(note).To(Equal(modelupdates.CanaryNotAvailable))
	})
})
