package galleryop_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

var _ = Describe("NodeStatus constants", func() {
	// Pin the wire-format string values. A future refactor that renames
	// a constant must NOT silently change the JSON value the UI receives
	// (or the cross-package contract with the nodes package, which
	// reuses these constants for NodeOpStatus.Status).
	DescribeTable("status constant",
		func(actual, expected string) {
			Expect(actual).To(Equal(expected))
		},
		Entry("queued", galleryop.NodeStatusQueued, "queued"),
		Entry("downloading", galleryop.NodeStatusDownloading, "downloading"),
		Entry("running on worker", galleryop.NodeStatusRunningOnWorker, "running_on_worker"),
		Entry("success", galleryop.NodeStatusSuccess, "success"),
		Entry("error", galleryop.NodeStatusError, "error"),
	)
})

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
					Status:     galleryop.NodeStatusRunningOnWorker,
					FileName:   "vllm.tar.zst",
					Current:    "412 MB",
					Total:      "2.1 GB",
					Percentage: 19.6,
					Phase:      "downloading", // literal pins the wire-format value
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
			NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading, Percentage: 12.0,
		})
		st := svc.GetStatus("op1")
		Expect(st).ToNot(BeNil())
		Expect(st.Nodes).To(HaveLen(1))
		Expect(st.Nodes[0].NodeID).To(Equal("n1"))
		Expect(st.Nodes[0].Percentage).To(Equal(12.0))
	})

	It("merges subsequent updates into the same NodeID entry, not appending", func() {
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading, Percentage: 12.0})
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading, Percentage: 48.0, FileName: "vllm.tar"})
		st := svc.GetStatus("op1")
		Expect(st.Nodes).To(HaveLen(1))
		Expect(st.Nodes[0].Percentage).To(Equal(48.0))
		Expect(st.Nodes[0].FileName).To(Equal("vllm.tar"))
	})

	It("appends a new entry for a different NodeID", func() {
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading, Percentage: 12.0})
		svc.UpdateNodeProgress("op1", "n2", galleryop.NodeProgress{NodeID: "n2", NodeName: "worker-b", Status: galleryop.NodeStatusQueued})
		st := svc.GetStatus("op1")
		Expect(st.Nodes).To(HaveLen(2))
	})

	It("mirrors the latest tick into the aggregate OpStatus fields", func() {
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{
			NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading,
			Percentage: 33.0, FileName: "vllm.tar", Current: "330 MB", Total: "1 GB",
		})
		st := svc.GetStatus("op1")
		Expect(st.Progress).To(Equal(33.0))
		Expect(st.FileName).To(Equal("vllm.tar"))
		Expect(st.DownloadedFileSize).To(Equal("330 MB"))
		Expect(st.TotalFileSize).To(Equal("1 GB"))
	})

	It("preserves accumulated Nodes when a subsequent UpdateStatus comes through the legacy path", func() {
		// Regression: the Phase 2 progress bridge also calls the legacy
		// progressCb -> UpdateStatus(opID, &OpStatus{...}) on every tick.
		// Without preservation that overwrite would wipe the Nodes slice
		// and the UI would flicker between one node and another on a
		// multi-worker install. UpdateStatus must carry forward existing
		// Nodes when the incoming op has none.
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusSuccess})
		svc.UpdateNodeProgress("op1", "n2", galleryop.NodeProgress{NodeID: "n2", NodeName: "worker-b", Status: galleryop.NodeStatusDownloading, Percentage: 30.0})

		// Now simulate the legacy progressCb path: a fresh OpStatus
		// pointer with no Nodes set, carrying only aggregate fields.
		svc.UpdateStatus("op1", &galleryop.OpStatus{
			Progress: 30.0,
			Message:  "downloading",
		})

		st := svc.GetStatus("op1")
		Expect(st.Nodes).To(HaveLen(2), "Nodes accumulated before the legacy UpdateStatus must be preserved")
		ids := []string{st.Nodes[0].NodeID, st.Nodes[1].NodeID}
		Expect(ids).To(ConsistOf("n1", "n2"))
	})

	It("allows an explicit empty-then-populated Nodes transition to win when caller sets Nodes", func() {
		// If a caller explicitly passes a non-empty Nodes slice on the
		// incoming op, that should replace the existing slice (no merge).
		// Only an EMPTY incoming slice triggers the carry-forward.
		svc.UpdateNodeProgress("op1", "n1", galleryop.NodeProgress{NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusSuccess})
		svc.UpdateStatus("op1", &galleryop.OpStatus{
			Progress: 100.0,
			Nodes: []galleryop.NodeProgress{
				{NodeID: "n9", NodeName: "worker-final", Status: galleryop.NodeStatusSuccess},
			},
		})
		st := svc.GetStatus("op1")
		Expect(st.Nodes).To(HaveLen(1))
		Expect(st.Nodes[0].NodeID).To(Equal("n9"))
	})
})
