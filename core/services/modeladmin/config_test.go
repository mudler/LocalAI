package modeladmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
)

// newTestService stands up a ConfigService backed by a tmp dir so the file IO
// is real but isolated. The model loader is loaded against the same tmp path
// so GetModelConfig works.
func newTestService(t *testing.T) (*ConfigService, string) {
	t.Helper()
	dir := t.TempDir()
	loader := config.NewModelConfigLoader(dir)
	appConfig := &config.ApplicationConfig{
		SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}},
	}
	return NewConfigService(loader, appConfig), dir
}

// writeModelYAML creates a model YAML on disk and reloads the loader so the
// new entry is visible.
func writeModelYAML(t *testing.T, svc *ConfigService, dir, name string, body map[string]any) {
	t.Helper()
	body["name"] = name
	data, err := yaml.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := svc.Loader.LoadModelConfigsFromPath(dir, svc.AppConfig.ToConfigLoaderOptions()...); err != nil {
		t.Fatalf("loader: %v", err)
	}
}

func TestGetConfig_RoundTrip(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

	view, err := svc.GetConfig(context.Background(), "qwen")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Name != "qwen" {
		t.Errorf("name = %q", view.Name)
	}
	if view.JSON["backend"] != "llama-cpp" {
		t.Errorf("backend = %v", view.JSON["backend"])
	}
}

func TestGetConfig_UnknownModel(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.GetConfig(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetConfig_EmptyName(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.GetConfig(context.Background(), "")
	if !errors.Is(err, ErrNameRequired) {
		t.Errorf("err = %v, want ErrNameRequired", err)
	}
}

func TestPatchConfig_DeepMerge(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{
		"backend":      "llama-cpp",
		"context_size": 4096,
		"parameters":   map[string]any{"temperature": 0.7, "top_p": 0.9},
	})

	updated, err := svc.PatchConfig(context.Background(), "qwen", map[string]any{
		"context_size": 8192,
		"parameters":   map[string]any{"temperature": 0.5},
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if updated.Name != "qwen" {
		t.Errorf("name = %q", updated.Name)
	}

	// Verify on disk
	raw, err := os.ReadFile(filepath.Join(dir, "qwen.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["context_size"] != 8192 {
		t.Errorf("context_size = %v, want 8192", got["context_size"])
	}
	// Deep-merge preserves untouched siblings.
	params, ok := got["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters not a map: %T", got["parameters"])
	}
	if params["temperature"] != 0.5 {
		t.Errorf("temperature = %v, want 0.5", params["temperature"])
	}
	if params["top_p"] != 0.9 {
		t.Errorf("top_p was clobbered: %v", params["top_p"])
	}
}

func TestPatchConfig_UnknownModel(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.PatchConfig(context.Background(), "ghost", map[string]any{"x": 1})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPatchConfig_EmptyPatch(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})
	_, err := svc.PatchConfig(context.Background(), "qwen", map[string]any{})
	if !errors.Is(err, ErrEmptyBody) {
		t.Errorf("err = %v, want ErrEmptyBody", err)
	}
}

func TestEditYAML_Rename(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "old-name", map[string]any{"backend": "llama-cpp"})

	body := []byte("name: new-name\nbackend: llama-cpp\n")
	result, err := svc.EditYAML(context.Background(), "old-name", body, nil)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !result.Renamed || result.OldName != "old-name" || result.NewName != "new-name" {
		t.Errorf("rename mismatch: %+v", result)
	}
	// Old file must be gone.
	if _, err := os.Stat(filepath.Join(dir, "old-name.yaml")); !os.IsNotExist(err) {
		t.Errorf("old YAML still present: err=%v", err)
	}
	// New file must exist.
	if _, err := os.Stat(filepath.Join(dir, "new-name.yaml")); err != nil {
		t.Errorf("new YAML missing: %v", err)
	}
	// Loader index reflects the new name.
	if _, ok := svc.Loader.GetModelConfig("new-name"); !ok {
		t.Errorf("loader missing renamed model")
	}
	if _, ok := svc.Loader.GetModelConfig("old-name"); ok {
		t.Errorf("loader still has old name")
	}
}

func TestEditYAML_RenameConflict(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})
	writeModelYAML(t, svc, dir, "beta", map[string]any{"backend": "llama-cpp"})

	body := []byte("name: beta\nbackend: llama-cpp\n")
	_, err := svc.EditYAML(context.Background(), "alpha", body, nil)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestEditYAML_PathSeparator(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})

	body := []byte("name: ../escape\nbackend: llama-cpp\n")
	_, err := svc.EditYAML(context.Background(), "alpha", body, nil)
	if !errors.Is(err, ErrPathSeparator) {
		t.Errorf("err = %v, want ErrPathSeparator", err)
	}
}

func TestEditYAML_EmptyBody(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})
	_, err := svc.EditYAML(context.Background(), "alpha", nil, nil)
	if !errors.Is(err, ErrEmptyBody) {
		t.Errorf("err = %v, want ErrEmptyBody", err)
	}
}
