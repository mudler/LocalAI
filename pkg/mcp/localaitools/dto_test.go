package localaitools

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// roundTripDTO marshals v to JSON, decodes back into the same type, and
// asserts equality. Catches struct-tag drift on every public DTO.
func roundTripDTO[T any](v T) {
	data, err := json.Marshal(v)
	Expect(err).ToNot(HaveOccurred())
	var got T
	Expect(json.Unmarshal(data, &got)).To(Succeed())
	Expect(got).To(Equal(v))
}

var _ = Describe("DTOs round-trip through JSON", func() {
	It("preserves every field across the public DTO set", func() {
		// Only the localaitools-owned DTOs need this guard. Types we
		// surface from elsewhere (config.Gallery, gallery.Metadata,
		// schema.KnownBackend, vram.EstimateResult) are tested by their
		// owning packages and don't need a copy here.
		roundTripDTO(GallerySearchQuery{Query: "qwen", Limit: 5, Tag: "chat", Gallery: "official"})
		roundTripDTO(InstalledModel{Name: "n", Backend: "b", Capabilities: []string{"chat"}, Pinned: true, Disabled: false})
		roundTripDTO(JobStatus{ID: "i", Processed: true, Progress: 0.5, Message: "m", ErrorMessage: ""})
		roundTripDTO(ModelConfigView{Name: "n", YAML: "k: v\n", JSON: map[string]any{"k": "v"}})
		roundTripDTO(InstallModelRequest{GalleryName: "g", ModelName: "m", Overrides: map[string]any{"k": "v"}})
		roundTripDTO(InstallBackendRequest{GalleryName: "g", BackendName: "b"})
		roundTripDTO(Backend{Name: "n", Installed: true})
		roundTripDTO(SystemInfo{Version: "v1", Distributed: false, ModelsPath: "/tmp", LoadedModels: []string{"a"}, InstalledBackends: []string{"x"}})
		roundTripDTO(Node{ID: "n", Address: "a", HTTPAddress: "h", TotalVRAM: 100, Healthy: true, LastSeen: "now"})
		roundTripDTO(VRAMEstimateRequest{ModelName: "m", ContextSize: 4096, GPULayers: -1, KVQuantBits: 8})
		roundTripDTO(ImportModelURIRequest{URI: "u", BackendPreference: "llama-cpp", Overrides: map[string]any{"k": "v"}})
		roundTripDTO(ImportModelURIResponse{JobID: "j", DiscoveredModelName: "m", AmbiguousBackend: true, Modality: "tts", BackendCandidates: []string{"a", "b"}, Hint: "h"})
	})
})
