package galleryop

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

var _ = Describe("artifact operation progress", func() {
	It("maps phases and never moves percentage backward", func() {
		var statuses []*OpStatus
		bridge := newArtifactProgressBridge(func(status *OpStatus) {
			copy := *status
			statuses = append(statuses, &copy)
		})
		bridge.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseResolving})
		bridge.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseDownloading, File: "model.bin", CurrentBytes: 50, TotalBytes: 100})
		bridge.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseDownloading, File: "model.bin", CurrentBytes: 10, TotalBytes: 100})
		bridge.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseVerifying, CurrentBytes: 100, TotalBytes: 100})
		bridge.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseCommitting, CurrentBytes: 100, TotalBytes: 100})
		bridge.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhasePersisting})

		Expect(statuses).To(HaveLen(6))
		for index := 1; index < len(statuses); index++ {
			Expect(statuses[index].Progress).To(BeNumerically(">=", statuses[index-1].Progress))
		}
		Expect(statuses[1].Phase).To(Equal("downloading"))
		Expect(statuses[1].CurrentBytes).To(Equal(int64(50)))
		Expect(statuses[5].Progress).To(Equal(float64(99)))
		Expect(statuses[5].CurrentBytes).To(Equal(int64(100)))
	})
})
