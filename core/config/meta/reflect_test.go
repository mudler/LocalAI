package meta_test

import (
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/meta"
)

func TestWalkModelConfig(t *testing.T) {
	fields := meta.WalkModelConfig(reflect.TypeOf(config.ModelConfig{}))
	if len(fields) == 0 {
		t.Fatal("expected fields from ModelConfig, got 0")
	}

	// Build a lookup by path
	byPath := make(map[string]meta.FieldMeta, len(fields))
	for _, f := range fields {
		byPath[f.Path] = f
	}

	// Verify some top-level fields exist
	for _, path := range []string{"name", "backend", "cuda", "step"} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("expected field %q not found", path)
		}
	}

	// Verify inline LLMConfig fields appear at top level (no prefix)
	for _, path := range []string{"context_size", "gpu_layers", "threads", "mmap"} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("expected inline LLMConfig field %q not found", path)
		}
	}

	// Verify nested struct fields have correct prefix
	for _, path := range []string{
		"template.chat",
		"template.completion",
		"template.use_tokenizer_template",
		"function.grammar.parallel_calls",
		"function.grammar.mixed_mode",
		"diffusers.pipeline_type",
		"diffusers.cuda",
		"pipeline.llm",
		"pipeline.tts",
		"reasoning.disable",
		"agent.max_iterations",
		"grpc.attempts",
	} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("expected nested field %q not found", path)
		}
	}

	// Verify PredictionOptions fields have parameters. prefix
	for _, path := range []string{
		"parameters.temperature",
		"parameters.top_p",
		"parameters.top_k",
		"parameters.max_tokens",
		"parameters.seed",
	} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("expected parameters field %q not found", path)
		}
	}

	// Verify TTSConfig fields have tts. prefix
	if _, ok := byPath["tts.voice"]; !ok {
		t.Error("expected tts.voice field not found")
	}
}

func TestSkipsYAMLDashFields(t *testing.T) {
	fields := meta.WalkModelConfig(reflect.TypeOf(config.ModelConfig{}))

	byPath := make(map[string]meta.FieldMeta, len(fields))
	for _, f := range fields {
		byPath[f.Path] = f
	}

	// modelConfigFile has yaml:"-" tag, should be skipped
	for _, f := range fields {
		if f.Path == "modelConfigFile" || f.Path == "modelTemplate" {
			t.Errorf("field %q should have been skipped (yaml:\"-\")", f.Path)
		}
	}
}

func TestTypeMapping(t *testing.T) {
	fields := meta.WalkModelConfig(reflect.TypeOf(config.ModelConfig{}))

	byPath := make(map[string]meta.FieldMeta, len(fields))
	for _, f := range fields {
		byPath[f.Path] = f
	}

	tests := []struct {
		path    string
		uiType  string
		pointer bool
	}{
		{"name", "string", false},
		{"cuda", "bool", false},
		{"context_size", "int", true},
		{"gpu_layers", "int", true},
		{"threads", "int", true},
		{"f16", "bool", true},
		{"mmap", "bool", true},
		{"stopwords", "[]string", false},
		{"roles", "map", false},
		{"parameters.temperature", "float", true},
		{"parameters.top_k", "int", true},
		{"function.grammar.parallel_calls", "bool", false},
	}

	for _, tt := range tests {
		f, ok := byPath[tt.path]
		if !ok {
			t.Errorf("field %q not found", tt.path)
			continue
		}
		if f.UIType != tt.uiType {
			t.Errorf("field %q: expected UIType %q, got %q", tt.path, tt.uiType, f.UIType)
		}
		if f.Pointer != tt.pointer {
			t.Errorf("field %q: expected Pointer=%v, got %v", tt.path, tt.pointer, f.Pointer)
		}
	}
}

func TestSectionAssignment(t *testing.T) {
	fields := meta.WalkModelConfig(reflect.TypeOf(config.ModelConfig{}))

	byPath := make(map[string]meta.FieldMeta, len(fields))
	for _, f := range fields {
		byPath[f.Path] = f
	}

	tests := []struct {
		path    string
		section string
	}{
		{"name", "general"},
		{"backend", "general"},
		{"context_size", "general"},   // inline LLMConfig -> no prefix -> general
		{"parameters.temperature", "parameters"},
		{"template.chat", "templates"},
		{"function.grammar.parallel_calls", "functions"},
		{"diffusers.cuda", "diffusers"},
		{"pipeline.llm", "pipeline"},
		{"reasoning.disable", "reasoning"},
		{"agent.max_iterations", "agent"},
		{"grpc.attempts", "grpc"},
	}

	for _, tt := range tests {
		f, ok := byPath[tt.path]
		if !ok {
			t.Errorf("field %q not found", tt.path)
			continue
		}
		if f.Section != tt.section {
			t.Errorf("field %q: expected section %q, got %q", tt.path, tt.section, f.Section)
		}
	}
}

func TestLabelGeneration(t *testing.T) {
	fields := meta.WalkModelConfig(reflect.TypeOf(config.ModelConfig{}))

	byPath := make(map[string]meta.FieldMeta, len(fields))
	for _, f := range fields {
		byPath[f.Path] = f
	}

	tests := []struct {
		path  string
		label string
	}{
		{"context_size", "Context Size"},
		{"gpu_layers", "Gpu Layers"},
		{"name", "Name"},
		{"cuda", "Cuda"},
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
	}
}

func TestFieldCount(t *testing.T) {
	fields := meta.WalkModelConfig(reflect.TypeOf(config.ModelConfig{}))
	// We expect a large number of fields (100+) given the config complexity
	if len(fields) < 80 {
		t.Errorf("expected at least 80 fields, got %d", len(fields))
	}
	t.Logf("Total fields discovered: %d", len(fields))
}
