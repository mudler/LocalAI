package modeladmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestToggleState_Disable(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

	if _, err := svc.ToggleState(context.Background(), "qwen", "disable", nil); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	got := readMap(t, filepath.Join(dir, "qwen.yaml"))
	if got["disabled"] != true {
		t.Errorf("disabled = %v, want true", got["disabled"])
	}
}

func TestToggleState_Enable_RemovesField(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "disabled": true})

	if _, err := svc.ToggleState(context.Background(), "qwen", "enable", nil); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	got := readMap(t, filepath.Join(dir, "qwen.yaml"))
	if _, present := got["disabled"]; present {
		t.Errorf("disabled key should be removed when enabling, got %v", got["disabled"])
	}
}

func TestToggleState_BadAction(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})
	_, err := svc.ToggleState(context.Background(), "qwen", "noop", nil)
	if !errors.Is(err, ErrBadAction) {
		t.Errorf("err = %v, want ErrBadAction", err)
	}
}

func TestToggleState_UnknownModel(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.ToggleState(context.Background(), "ghost", "disable", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// readMap is a tiny helper: read the YAML file as a map[string]any.
func readMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}
