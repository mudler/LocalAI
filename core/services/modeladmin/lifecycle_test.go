package modeladmin

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/nodes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type lifecycleRegistry struct {
	models map[string][]nodes.NodeModel
	calls  []string
	err    error
}

func (r *lifecycleRegistry) AdvanceModelConfigRevisions(_ context.Context, transitions []nodes.ModelConfigRevisionTransition) ([]nodes.NodeModel, error) {
	for _, transition := range transitions {
		r.calls = append(r.calls, transition.ModelName+":"+transition.ConfigRevision)
	}
	if r.err != nil {
		return nil, r.err
	}
	var quarantined []nodes.NodeModel
	for _, transition := range transitions {
		quarantined = append(quarantined, r.models[transition.ModelName]...)
	}
	return quarantined, nil
}

type lifecycleCleanup struct {
	registry *lifecycleRegistry
	seen     []nodes.NodeModel
	pending  int
}

func (c *lifecycleCleanup) Cleanup(_ context.Context, replicas []nodes.NodeModel, _ bool) int {
	Expect(c.registry.calls).ToNot(BeEmpty(), "quarantine must precede worker cleanup")
	c.seen = append(c.seen, replicas...)
	return c.pending
}

var _ = Describe("DistributedModelRevisionLifecycle", func() {
	It("advances the registry before cleanup and reports incomplete exact stops", func() {
		registry := &lifecycleRegistry{models: map[string][]nodes.NodeModel{
			"model": {{ID: "stale", ModelName: "model", State: "unloading"}},
		}}
		cleanup := &lifecycleCleanup{registry: registry, pending: 1}
		lifecycle := NewDistributedModelRevisionLifecycle(registry, cleanup)

		pending, err := lifecycle.ApplyConfigRevisions(context.Background(), []ModelRevisionTransition{{ModelName: "model", ConfigRevision: "rev-new"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(pending).To(Equal(1))
		Expect(registry.calls).To(Equal([]string{"model:rev-new"}))
		Expect(cleanup.seen).To(HaveLen(1))
	})

	It("quarantines the old identity and establishes the renamed identity", func() {
		registry := &lifecycleRegistry{models: map[string][]nodes.NodeModel{
			"old": {{ID: "old-replica", ModelName: "old"}},
		}}
		cleanup := &lifecycleCleanup{registry: registry}
		lifecycle := NewDistributedModelRevisionLifecycle(registry, cleanup)

		_, err := lifecycle.ApplyConfigRevisions(context.Background(), []ModelRevisionTransition{
			{ModelName: "old", ConfigRevision: DeletedModelConfigRevision("old"), Disabled: true},
			{ModelName: "new", ConfigRevision: "rev-renamed"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.calls).To(Equal([]string{"old:" + DeletedModelConfigRevision("old"), "new:rev-renamed"}))
		Expect(cleanup.seen).To(ConsistOf(nodes.NodeModel{ID: "old-replica", ModelName: "old"}))
	})

	It("does not clean up a partially advanced rename when the atomic transition fails", func() {
		registry := &lifecycleRegistry{err: errors.New("injected rename transition failure")}
		cleanup := &lifecycleCleanup{registry: registry}
		lifecycle := NewDistributedModelRevisionLifecycle(registry, cleanup)

		pending, err := lifecycle.ApplyConfigRevisions(context.Background(), []ModelRevisionTransition{
			{ModelName: "old", ConfigRevision: DeletedModelConfigRevision("old"), Disabled: true},
			{ModelName: "new", ConfigRevision: "rev-renamed"},
		})
		Expect(err).To(MatchError(ContainSubstring("injected rename transition failure")))
		Expect(pending).To(BeZero())
		Expect(registry.calls).To(Equal([]string{"old:" + DeletedModelConfigRevision("old"), "new:rev-renamed"}))
		Expect(cleanup.seen).To(BeEmpty())
	})
})
