package application

import (
	"context"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type recordingModelNotifier struct{ results []modelupdates.UpgradeResult }

func (n *recordingModelNotifier) ModelUpgradeResult(_ context.Context, r modelupdates.UpgradeResult) error {
	n.results = append(n.results, r)
	return nil
}

var _ = Describe("modelUpgraderOp", func() {
	It("hands the upgrade-run mark of the operation to the upgrader, so the run alone tells the end", func() {
		n := &recordingModelNotifier{}
		u := &modelupdates.Upgrader{SystemState: &system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}}, Notifier: n}
		op := modelUpgraderOp{u: u}
		// The path check refuses the name before anything is read; the
		// refusal is told with the mark of the request.
		err := op.UpgradeModel(context.Background(), "op-1", "../outside", galleryop.ModelUpgradeOptions{InRun: true}, func(*galleryop.OpStatus) {})
		Expect(err).To(MatchError(modelupdates.ErrNotFound))
		Expect(n.results).To(HaveLen(1))
		Expect(n.results[0].InRun).To(BeTrue())
	})
})
