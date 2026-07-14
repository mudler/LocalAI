package gallery_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/gallery"
)

func TestDeleteStagedModelFiles(t *testing.T) {
	t.Run("rejects empty model name", func(t *testing.T) {
		dir := t.TempDir()
		err := gallery.DeleteStagedModelFiles(dir, "")
		if err == nil {
			t.Fatal("expected error for empty model name")
		}
	})

	t.Run("rejects path traversal via ..", func(t *testing.T) {
		dir := t.TempDir()
		err := gallery.DeleteStagedModelFiles(dir, "../../etc/passwd")
		if err == nil {
			t.Fatal("expected error for path traversal attempt")
		}
	})

	t.Run("rejects path traversal via ../foo", func(t *testing.T) {
		dir := t.TempDir()
		err := gallery.DeleteStagedModelFiles(dir, "../foo")
		if err == nil {
			t.Fatal("expected error for path traversal attempt")
		}
	})

	t.Run("removes model subdirectory with all files", func(t *testing.T) {
		dir := t.TempDir()
		modelDir := filepath.Join(dir, "my-model", "sd-cpp", "models")
		if err := os.MkdirAll(modelDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Create model files in subdirectory
		os.WriteFile(filepath.Join(modelDir, "flux.gguf"), []byte("model"), 0o644)
		os.WriteFile(filepath.Join(modelDir, "flux.gguf.mmproj"), []byte("mmproj"), 0o644)

		err := gallery.DeleteStagedModelFiles(dir, "my-model")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Entire my-model directory should be gone
		if _, err := os.Stat(filepath.Join(dir, "my-model")); !os.IsNotExist(err) {
			t.Fatal("expected model directory to be removed")
		}
	})

	t.Run("removes single file model", func(t *testing.T) {
		dir := t.TempDir()
		modelFile := filepath.Join(dir, "model.gguf")
		os.WriteFile(modelFile, []byte("model"), 0o644)

		err := gallery.DeleteStagedModelFiles(dir, "model.gguf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, err := os.Stat(modelFile); !os.IsNotExist(err) {
			t.Fatal("expected model file to be removed")
		}
	})

	t.Run("removes sibling files via glob", func(t *testing.T) {
		dir := t.TempDir()
		modelFile := filepath.Join(dir, "model.gguf")
		siblingFile := filepath.Join(dir, "model.gguf.mmproj")
		os.WriteFile(modelFile, []byte("model"), 0o644)
		os.WriteFile(siblingFile, []byte("mmproj"), 0o644)

		err := gallery.DeleteStagedModelFiles(dir, "model.gguf")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, err := os.Stat(modelFile); !os.IsNotExist(err) {
			t.Fatal("expected model file to be removed")
		}
		if _, err := os.Stat(siblingFile); !os.IsNotExist(err) {
			t.Fatal("expected sibling file to be removed")
		}
	})

	t.Run("no error when model does not exist", func(t *testing.T) {
		dir := t.TempDir()
		err := gallery.DeleteStagedModelFiles(dir, "nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
