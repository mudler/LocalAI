package application

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/model"
)

// fakeReplicas is the registry of a cluster: the nodes that run each model.
// A shutdown through fakeLoader stops what stop allows.
type fakeReplicas struct {
	running map[string][]nodes.BackendNode
	err     error
	// ctxErr is the state of the context of the last call.
	ctxErr error
}

func (f *fakeReplicas) FindNodesWithModel(ctx context.Context, name string) ([]nodes.BackendNode, error) {
	f.ctxErr = ctx.Err()
	return f.running[name], f.err
}

type fakeLoader struct {
	replicas    *fakeReplicas
	shutdownErr error
	stopAll     bool
	shutdowns   int
	blocked     map[string]bool
}

func (f *fakeLoader) ShutdownModel(name string) error {
	f.shutdowns++
	if f.stopAll {
		delete(f.replicas.running, name)
	}
	return f.shutdownErr
}

func (f *fakeLoader) ListLoadedModels() []*model.Model { return nil }

func (f *fakeLoader) BlockLoads(name string) {
	if f.blocked == nil {
		f.blocked = map[string]bool{}
	}
	f.blocked[name] = true
}

func (f *fakeLoader) UnblockLoads(name string) { delete(f.blocked, name) }

// fakeRevisions is the registry's view of the config revisions: publishing a
// revision stops the replicas of any other.
type fakeRevisions struct {
	replicas  *fakeReplicas
	published map[string]string
	err       error
}

func (f *fakeRevisions) ApplyConfigRevisions(_ context.Context, transitions []modeladmin.ModelRevisionTransition) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	for _, t := range transitions {
		f.published[t.ModelName] = t.ConfigRevision
		delete(f.replicas.running, t.ModelName)
	}
	return 0, nil
}

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
		Expect(rt.IsLoaded(ctx, "tiny")).To(BeTrue())
		Expect(rt.IsLoaded(ctx, "other")).To(BeFalse())
	})

	It("asks the registry with the context of the caller", func() {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		Expect(rt.IsLoaded(cctx, "other")).To(BeFalse())
		Expect(replicas.ctxErr).To(MatchError(context.Canceled), "a cancelled upgrade must stop the registry call")
	})

	It("does not read a model as loaded when the registry cannot answer", func() {
		replicas.err = errors.New("database down")
		Expect(rt.IsLoaded(ctx, "other")).To(BeFalse())
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

	It("holds the model in the registry while the files change, then publishes the new revision", func() {
		revs := &fakeRevisions{replicas: replicas, published: map[string]string{"tiny": "rev-old"}}
		rt.revisions = revs
		rt.revisionOf = func(string) (string, error) { return "rev-old", nil }

		Expect(rt.BlockLoads(ctx, "tiny")).To(Succeed())
		Expect(revs.published["tiny"]).To(Equal(upgradeBlockRevision("tiny")))
		Expect(replicas.running["tiny"]).To(BeEmpty(), "every replica stopped")
		Expect(loader.blocked["tiny"]).To(BeTrue())

		By("publishing the revision on disk again, though it did not change")
		Expect(rt.UnblockLoads(ctx, "tiny")).To(Succeed())
		Expect(revs.published["tiny"]).To(Equal("rev-old"))
		Expect(loader.blocked).To(BeEmpty())
	})

	It("does not block this frontend when the registry refuses the block", func() {
		rt.revisions = &fakeRevisions{replicas: replicas, published: map[string]string{}, err: errors.New("database down")}
		Expect(rt.BlockLoads(ctx, "tiny")).To(MatchError(ContainSubstring("database down")))
		Expect(loader.blocked).To(BeEmpty())
	})

	It("lets this frontend load again when the config revision cannot be read", func() {
		rt.revisions = &fakeRevisions{replicas: replicas, published: map[string]string{}}
		rt.revisionOf = func(string) (string, error) { return "", errors.New("no config") }
		Expect(rt.BlockLoads(ctx, "tiny")).To(Succeed())
		Expect(rt.UnblockLoads(ctx, "tiny")).To(MatchError(ContainSubstring("no config")))
		Expect(loader.blocked).To(BeEmpty())
	})

	Context("a block left in the registry", func() {
		var revs *fakeRevisions

		BeforeEach(func() {
			revs = &fakeRevisions{replicas: replicas, published: map[string]string{
				"tiny": upgradeBlockRevision("tiny"), "other": "rev-other",
			}}
			rt.revisions = revs
			rt.revisionOf = func(name string) (string, error) { return "rev-disk-" + name, nil }
			rt.storedRevision = func(_ context.Context, name string) (string, error) {
				rev, ok := revs.published[name]
				if !ok {
					return "", modeladmin.ErrNoStoredRevision
				}
				return rev, nil
			}
		})

		It("is lifted for a model that no upgrade holds, and only for it", func() {
			done := rt.RepublishBlocked(ctx, []string{"tiny", "other", "never-served"}, func(context.Context, string) (func(), error) {
				return func() {}, nil
			})
			Expect(done).To(Equal([]string{"tiny"}))
			Expect(revs.published["tiny"]).To(Equal("rev-disk-tiny"))
			Expect(revs.published["other"]).To(Equal("rev-other"))
		})

		It("stays while another frontend holds the lock of the model", func() {
			done := rt.RepublishBlocked(ctx, []string{"tiny"}, func(context.Context, string) (func(), error) {
				return nil, gallery.ErrModelLocked
			})
			Expect(done).To(BeEmpty())
			Expect(revs.published["tiny"]).To(Equal(upgradeBlockRevision("tiny")))
		})

		It("is lifted at a later pass when the publish failed", func() {
			free := func(context.Context, string) (func(), error) { return func() {}, nil }
			revs.err = errors.New("database down")
			Expect(rt.RepublishBlocked(ctx, []string{"tiny"}, free)).To(BeEmpty())
			revs.err = nil
			Expect(rt.RepublishBlocked(ctx, []string{"tiny"}, free)).To(Equal([]string{"tiny"}))
		})
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
