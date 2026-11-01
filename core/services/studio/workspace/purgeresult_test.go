package workspace_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("PurgeResult.Add", func() {
	It("counts files and folders in the total and under their revision", func() {
		r := workspace.PurgeResult{ByRev: map[int64]workspace.PurgeCount{}}
		r.Add(3, true, 99) // a folder: its size does not count
		r.Add(3, false, 10)
		r.Add(5, false, 7)
		Expect(r.PurgeCount).To(Equal(workspace.PurgeCount{Files: 2, Dirs: 1, Bytes: 17}))
		Expect(r.ByRev).To(Equal(map[int64]workspace.PurgeCount{
			3: {Files: 1, Dirs: 1, Bytes: 10},
			5: {Files: 1, Bytes: 7},
		}))
	})
})
