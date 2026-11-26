package upgraderun

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/galleryop"
)

var _ = Describe("viewOf", func() {
	It("takes the typed code and the rollback that the backend upgrade wrote", func() {
		v := viewOf(KindGallery, &galleryop.OpStatus{
			Processed: true, Phase: "fetching", Error: errors.New("the new build did not start"),
			ErrorKey: "start", RolledBack: true,
		}, "")
		Expect(v.outcome).To(Equal(JobError))
		Expect(v.errorKey).To(Equal(ErrKeyStart), "the typed code wins over the phase")
		Expect(v.rolledBack).To(BeTrue())
	})

	It("falls back to the phase when the status has no typed code", func() {
		v := viewOf(KindGallery, &galleryop.OpStatus{Processed: true, Phase: "fetching", Error: errors.New("reset")}, "")
		Expect(v.errorKey).To(Equal(ErrKeyDownload))
		Expect(v.rolledBack).To(BeFalse())
	})

	It("reads an upgrade whose worker reply was lost as an operation with no outcome", func() {
		v := viewOf(KindGallery, &galleryop.OpStatus{Processed: true, Unconfirmed: true, Message: "worker still installing in background"}, "")
		Expect(v.ended).To(BeTrue())
		Expect(v.outcome).To(Equal(JobLost))
	})

	It("keeps the result and the failure code of each node", func() {
		v := viewOf(KindGallery, &galleryop.OpStatus{
			Processed: true, Error: errors.New("1 of 2 nodes failed"), ErrorKey: "start", RolledBack: true,
			Nodes: []galleryop.NodeProgress{
				{NodeID: "n1", NodeName: "worker-1", Status: galleryop.NodeStatusRolledBack, Error: "did not start", ErrorKey: "start"},
				{NodeID: "n2", NodeName: "worker-2", Status: galleryop.NodeStatusUnconfirmed},
			},
		}, "")
		Expect(v.nodes).To(Equal([]NodeState{
			{ID: "n1", Name: "worker-1", Phase: "rolled_back", Message: "did not start", ErrorKey: "start"},
			{ID: "n2", Name: "worker-2", Phase: "unconfirmed"},
		}))
	})
})
