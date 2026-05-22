package galleryop_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
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

var _ = Describe("GalleryService.UpdateNodeProgress", func() {
	var svc *galleryop.GalleryService

	BeforeEach(func() {
		// UpdateNodeProgress + GetStatus only touch the in-memory statuses
		// map. A zero-value ApplicationConfig is enough to get past the
		// LocalModelManager / LocalBackendManager constructors.
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
	})

	It("creates a node entry on first call", func() {
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{
			NodeID: "n1", NodeName: "worker-a", Status: "downloading", Percentage: 12.0,
		})
		st := svc.GetStatus("op1")
		Expect(st).ToNot(BeNil())
		Expect(st.Nodes).To(HaveLen(1))
		Expect(st.Nodes[0].NodeID).To(Equal("n1"))
		Expect(st.Nodes[0].Percentage).To(Equal(12.0))
	})

	It("merges subsequent updates into the same NodeID entry, not appending", func() {
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: "downloading", Percentage: 12.0})
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: "downloading", Percentage: 48.0, FileName: "vllm.tar"})
		st := svc.GetStatus("op1")
		Expect(st.Nodes).To(HaveLen(1))
		Expect(st.Nodes[0].Percentage).To(Equal(48.0))
		Expect(st.Nodes[0].FileName).To(Equal("vllm.tar"))
	})

	It("appends a new entry for a different NodeID", func() {
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: "downloading", Percentage: 12.0})
		svc.UpdateNodeProgress("op1", "n2", galleryop.NodeProgress{NodeID: "n2", NodeName: "worker-b", Status: "queued"})
		st := svc.GetStatus("op1")
		Expect(st.Nodes).To(HaveLen(2))
	})

	It("mirrors the latest tick into the aggregate OpStatus fields", func() {
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{
			NodeID: "n1", NodeName: "worker-a", Status: "downloading",
			Percentage: 33.0, FileName: "vllm.tar", Current: "330 MB", Total: "1 GB",
		})
		st := svc.GetStatus("op1")
		Expect(st.Progress).To(Equal(33.0))
		Expect(st.FileName).To(Equal("vllm.tar"))
		Expect(st.DownloadedFileSize).To(Equal("330 MB"))
		Expect(st.TotalFileSize).To(Equal("1 GB"))
	})
})
