package localaitools

import (
	"encoding/json"
	"reflect"
	"testing"
)

// roundTrip[T] asserts that v can be marshalled and unmarshalled back to an
// equivalent value — guards against tag drift.
func roundTrip[T any](t *testing.T, v T) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got T
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, string(data))
	}
	if !reflect.DeepEqual(v, got) {
		t.Errorf("round-trip mismatch.\n  in: %#v\n out: %#v", v, got)
	}
}

func TestDTORoundTrip(t *testing.T) {
	roundTrip(t, GallerySearchQuery{Query: "qwen", Limit: 5, Tag: "chat", Gallery: "official"})
	roundTrip(t, GalleryModelHit{Name: "n", Gallery: "g", URL: "u", Description: "d", License: "l", Tags: []string{"a", "b"}, Installed: true})
	roundTrip(t, InstalledModel{Name: "n", Backend: "b", Capabilities: []string{"chat"}, Pinned: true, Disabled: false})
	roundTrip(t, Gallery{Name: "n", URL: "u"})
	roundTrip(t, JobStatus{ID: "i", Processed: true, Progress: 0.5, Message: "m", ErrorMessage: ""})
	roundTrip(t, ModelConfigView{Name: "n", YAML: "k: v\n", JSON: map[string]any{"k": "v"}})
	roundTrip(t, InstallModelRequest{GalleryName: "g", ModelName: "m", Overrides: map[string]any{"k": "v"}})
	roundTrip(t, InstallBackendRequest{GalleryName: "g", BackendName: "b"})
	roundTrip(t, Backend{Name: "n", Gallery: "g", Installed: true, Description: "d", Tags: []string{"x"}})
	roundTrip(t, SystemInfo{Version: "v1", Distributed: false, ModelsPath: "/tmp", LoadedModels: []string{"a"}, InstalledBackends: []string{"x"}})
	roundTrip(t, Node{ID: "n", Address: "a", HTTPAddress: "h", TotalVRAM: 100, Healthy: true, LastSeen: "now"})
	roundTrip(t, VRAMEstimateRequest{ModelName: "m", ContextSize: 4096, GPULayers: -1, KVQuantBits: 8})
	roundTrip(t, VRAMEstimate{ModelName: "m", EstimatedVRAMMB: 1024, WeightsMB: 800, KVCacheMB: 100, OverheadMB: 124})
}
