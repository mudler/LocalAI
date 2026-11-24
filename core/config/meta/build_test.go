package meta_test

import (
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/meta"
)

func TestBuildConfigMetadata(t *testing.T) {
	md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())

	if len(md.Sections) == 0 {
		t.Fatal("expected sections, got 0")
	}
	if len(md.Fields) == 0 {
		t.Fatal("expected fields, got 0")
	}

	// Verify sections are ordered
	for i := 1; i < len(md.Sections); i++ {
		if md.Sections[i].Order < md.Sections[i-1].Order {
			t.Errorf("sections not ordered: %s (order=%d) before %s (order=%d)",
				md.Sections[i-1].ID, md.Sections[i-1].Order,
				md.Sections[i].ID, md.Sections[i].Order)
		}
	}
}

func TestRegistryOverrides(t *testing.T) {
	registry := map[string]meta.FieldMetaOverride{
		"name": {
			Label:       "My Custom Label",
			Description: "Custom description",
			Component:   "textarea",
			Order:       999,
		},
	}

	md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), registry)

	byPath := make(map[string]meta.FieldMeta, len(md.Fields))
	for _, f := range md.Fields {
		byPath[f.Path] = f
	}

	f, ok := byPath["name"]
	if !ok {
		t.Fatal("field 'name' not found")
	}
	if f.Label != "My Custom Label" {
		t.Errorf("expected label 'My Custom Label', got %q", f.Label)
	}
	if f.Description != "Custom description" {
		t.Errorf("expected description 'Custom description', got %q", f.Description)
	}
	if f.Component != "textarea" {
		t.Errorf("expected component 'textarea', got %q", f.Component)
	}
	if f.Order != 999 {
		t.Errorf("expected order 999, got %d", f.Order)
	}
}

func TestUnregisteredFieldsGetDefaults(t *testing.T) {
	// Use empty registry - all fields should still get auto-generated metadata
	md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), map[string]meta.FieldMetaOverride{})

	byPath := make(map[string]meta.FieldMeta, len(md.Fields))
	for _, f := range md.Fields {
		byPath[f.Path] = f
	}

	// context_size should still exist with auto-generated label
	f, ok := byPath["context_size"]
	if !ok {
		t.Fatal("field 'context_size' not found")
	}
	if f.Label == "" {
		t.Error("expected auto-generated label, got empty")
	}
	if f.UIType != "int" {
		t.Errorf("expected UIType 'int', got %q", f.UIType)
	}
	if f.Component == "" {
		t.Error("expected auto-generated component, got empty")
	}
}

func TestDefaultRegistryOverridesApply(t *testing.T) {
	md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())

	byPath := make(map[string]meta.FieldMeta, len(md.Fields))
	for _, f := range md.Fields {
		byPath[f.Path] = f
	}

	// Verify enriched fields got their overrides
	tests := []struct {
		path        string
		label       string
		description string
		vramImpact  bool
	}{
		{"context_size", "Context Size", "Maximum context window in tokens", true},
		{"gpu_layers", "GPU Layers", "Number of layers to offload to GPU (-1 = all)", true},
		{"backend", "Backend", "The inference backend to use (e.g. llama-cpp, vllm, diffusers)", false},
		{"parameters.temperature", "Temperature", "Sampling temperature (higher = more creative, lower = more deterministic)", false},
		{"template.chat", "Chat Template", "Go template for chat completion requests", false},
	}

	for _, tt := range tests {
		f, ok := byPath[tt.path]
		if !ok {
			t.Errorf("field %q not found", tt.path)
			continue
		}
		if f.Label != tt.label {
			t.Errorf("field %q: expected label %q, got %q", tt.path, tt.label, f.Label)
		}
		if f.Description != tt.description {
			t.Errorf("field %q: expected description %q, got %q", tt.path, tt.description, f.Description)
		}
		if f.VRAMImpact != tt.vramImpact {
			t.Errorf("field %q: expected vramImpact=%v, got %v", tt.path, tt.vramImpact, f.VRAMImpact)
		}
	}
}

func TestStaticOptionsFields(t *testing.T) {
	md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())

	byPath := make(map[string]meta.FieldMeta, len(md.Fields))
	for _, f := range md.Fields {
		byPath[f.Path] = f
	}

	// Fields with static options should have Options populated and no AutocompleteProvider
	staticFields := []string{"quantization", "cache_type_k", "cache_type_v", "diffusers.pipeline_type", "diffusers.scheduler_type"}
	for _, path := range staticFields {
		f, ok := byPath[path]
		if !ok {
			t.Errorf("field %q not found", path)
			continue
		}
		if len(f.Options) == 0 {
			t.Errorf("field %q: expected Options to be populated", path)
		}
		if f.AutocompleteProvider != "" {
			t.Errorf("field %q: expected no AutocompleteProvider, got %q", path, f.AutocompleteProvider)
		}
	}
}

func TestDynamicProviderFields(t *testing.T) {
	md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())

	byPath := make(map[string]meta.FieldMeta, len(md.Fields))
	for _, f := range md.Fields {
		byPath[f.Path] = f
	}

	// Fields with dynamic providers should have AutocompleteProvider and no Options
	dynamicFields := map[string]string{
		"backend":                meta.ProviderBackends,
		"pipeline.llm":          meta.ProviderModelsChat,
		"pipeline.tts":          meta.ProviderModelsTTS,
		"pipeline.transcription": meta.ProviderModelsTranscript,
		"pipeline.vad":          meta.ProviderModelsVAD,
	}
	for path, expectedProvider := range dynamicFields {
		f, ok := byPath[path]
		if !ok {
			t.Errorf("field %q not found", path)
			continue
		}
		if f.AutocompleteProvider != expectedProvider {
			t.Errorf("field %q: expected AutocompleteProvider %q, got %q", path, expectedProvider, f.AutocompleteProvider)
		}
		if len(f.Options) != 0 {
			t.Errorf("field %q: expected no Options, got %d", path, len(f.Options))
		}
	}
}

func TestVRAMImpactFields(t *testing.T) {
	md := meta.BuildForTest(reflect.TypeOf(config.ModelConfig{}), meta.DefaultRegistry())

	var vramFields []string
	for _, f := range md.Fields {
		if f.VRAMImpact {
			vramFields = append(vramFields, f.Path)
		}
	}

	if len(vramFields) == 0 {
		t.Error("expected some VRAM impact fields, got 0")
	}

	// context_size and gpu_layers should be marked
	expected := map[string]bool{"context_size": true, "gpu_layers": true}
	for _, path := range vramFields {
		if expected[path] {
			delete(expected, path)
		}
	}
	for path := range expected {
		t.Errorf("expected VRAM impact field %q not found", path)
	}
}
