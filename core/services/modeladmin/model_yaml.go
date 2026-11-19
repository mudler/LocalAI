package modeladmin

import (
	"errors"
	"path/filepath"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/xlog"
)

// OperatorStateKeys are the keys of a model config that hold operator state
// rather than configuration: the eviction pin and the disabled switch. The
// update check never counts them as a person's edit, and an upgrade keeps
// their value from the config on disk.
var OperatorStateKeys = []string{"pinned", "disabled"}

// writeModelConfig is the one writer of an existing model's config file in
// this package. It writes data to path atomically and then records the hash of
// data in the model's version 2 gallery file, so the update check can tell an
// edit made through LocalAI from a config that was changed outside it.
//
// Every edit path (EditYAML, PatchConfig, ToggleState, TogglePinned) calls it;
// model_yaml_guard_test.go fails when a new writer does not. An old gallery
// file (no version) is left untouched: old files are never rewritten, and the
// update check reports such a model as unknown anyway. A gallery file that
// cannot be read or written is logged and does not fail the edit: the hash is
// a hint for the update check, and the edit is what the person asked for.
func writeModelConfig(path, modelName string, data []byte) error {
	if err := writeFileAtomic(path, data, 0644); err != nil {
		return err
	}
	recordConfigHash(filepath.Dir(path), modelName, data)
	return nil
}

func recordConfigHash(modelsPath, modelName string, data []byte) {
	defer gallery.LockSidecar(modelName)()
	sc, err := gallery.ReadSidecar(modelsPath, modelName)
	if errors.Is(err, gallery.ErrNoSidecar) {
		return
	}
	if err != nil {
		xlog.Warn("Could not record the config hash in the gallery file", "model", modelName, "error", err)
		return
	}
	if !sc.IsV2() {
		return
	}
	sc.ConfigSHA256 = gallery.SHA256Hex(data)
	if err := gallery.WriteSidecar(modelsPath, modelName, sc); err != nil {
		xlog.Warn("Could not record the config hash in the gallery file", "model", modelName, "error", err)
	}
}

// sidecarPathFor is the gallery file of a model, for the rollback snapshot of
// an edit.
func sidecarPathFor(modelsPath, modelName string) string {
	return gallery.SidecarPath(modelsPath, modelName)
}
