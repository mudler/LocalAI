package modelupdates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// CheckFileName is the file in the models directory where the last check
// writes what it found. It is a dotfile and not YAML, so the config loader,
// the directory resync and the model deletion ignore it.
const CheckFileName = "._model_updates.json"

// checkFile is what a check found, for every installed gallery model.
type checkFile struct {
	CheckedAt     time.Time `json:"checkedAt"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitzero"`
	// Error is the error of the last check of the galleries, empty when it
	// succeeded. The models then keep the results of the last good check.
	Error  string                 `json:"error,omitempty"`
	Models map[string]checkResult `json:"models"`
}

// checkResult is what the check found for one model.
type checkResult struct {
	// InstalledDigest is the digest the gallery file had at the check, so a
	// later upgrade or reinstall is not compared with an old answer.
	InstalledDigest string `json:"installedDigest,omitempty"`
	AvailableDigest string `json:"availableDigest,omitempty"`
	Gone            bool   `json:"gone,omitempty"`
	// Error is why this model could not be checked.
	Error string `json:"error,omitempty"`
	// PlannedConfig is the model config the update would write, so the read
	// model can find the conflicts with the config on disk as it is now.
	PlannedConfig string       `json:"plannedConfig,omitempty"`
	ChangedFiles  []FileChange `json:"changedFiles,omitempty"`
}

func checkFilePath(modelsPath string) string { return filepath.Join(modelsPath, CheckFileName) }

func readCheckFile(modelsPath string) (*checkFile, error) {
	data, err := os.ReadFile(checkFilePath(modelsPath))
	if errors.Is(err, os.ErrNotExist) {
		return &checkFile{Models: map[string]checkResult{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var f checkFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", CheckFileName, err)
	}
	if f.Models == nil {
		f.Models = map[string]checkResult{}
	}
	return &f, nil
}

func writeCheckFile(modelsPath string, f *checkFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(checkFilePath(modelsPath), data, 0600)
}
