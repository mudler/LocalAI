package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Each chat's harness gets the context window of that chat's own model, from
// the host, so a small model compacts early and a large one does not.
var _ = Describe("context internal", func() {
	It("each chat gets the context size of its model", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		sizes := map[string]int{"small": 4096, "large": 131072}
		svc, err := New(Deps{
			Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
			ContextTokens: func(model string) int { return sizes[model] },
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })

		for model, want := range sizes {
			rec, err := svc.Create(context.Background(), "alice", CreateInput{Model: model})
			Expect(err).NotTo(HaveOccurred())
			m, err := svc.llmFor(workspace.Actor{Principal: "alice", ChatID: rec.ID})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Name).To(Equal(model), "model %q", model)
			Expect(m.ContextTokens).To(Equal(want), "model %q", model)
			Expect(m.LLM).NotTo(BeNil(), "model %q", model)
		}
	})
})
