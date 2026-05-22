package galleryop_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/galleryop"
)

var _ = Describe("OpStatus.Nodes", func() {
	It("defaults to empty on a fresh OpStatus", func() {
		os := &galleryop.OpStatus{}
		Expect(os.Nodes).To(BeEmpty())
	})

	It("JSON round-trips with all NodeProgress fields", func() {
		os := &galleryop.OpStatus{
			Nodes: []galleryop.NodeProgress{
				{
					NodeID:     "node-1",
					NodeName:   "worker-a",
					Status:     "running_on_worker",
					FileName:   "vllm.tar.zst",
					Current:    "412 MB",
					Total:      "2.1 GB",
					Percentage: 19.6,
					Phase:      "downloading",
					Error:      "",
				},
			},
		}
		raw, err := json.Marshal(os)
		Expect(err).ToNot(HaveOccurred())

		got := &galleryop.OpStatus{}
		Expect(json.Unmarshal(raw, got)).To(Succeed())
		Expect(got.Nodes).To(HaveLen(1))
		Expect(got.Nodes[0]).To(Equal(os.Nodes[0]))
	})
})
