package modeladmin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestTogglePinned_Pin(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

	if _, err := svc.TogglePinned(context.Background(), "qwen", "pin", nil); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	got := readMap(t, filepath.Join(dir, "qwen.yaml"))
	if got["pinned"] != true {
		t.Errorf("pinned = %v, want true", got["pinned"])
	}
}

func TestTogglePinned_Unpin_RemovesField(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "pinned": true})

	if _, err := svc.TogglePinned(context.Background(), "qwen", "unpin", nil); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	got := readMap(t, filepath.Join(dir, "qwen.yaml"))
	if _, present := got["pinned"]; present {
		t.Errorf("pinned key should be removed on unpin, got %v", got["pinned"])
	}
}

func TestTogglePinned_BadAction(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})
	_, err := svc.TogglePinned(context.Background(), "qwen", "stick", nil)
	if !errors.Is(err, ErrBadAction) {
		t.Errorf("err = %v, want ErrBadAction", err)
	}
}

func TestTogglePinned_SyncCallback(t *testing.T) {
	svc, dir := newTestService(t)
	writeModelYAML(t, svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

	called := false
	if _, err := svc.TogglePinned(context.Background(), "qwen", "pin", func() { called = true }); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if !called {
		t.Errorf("syncPinned callback not invoked")
	}
}
