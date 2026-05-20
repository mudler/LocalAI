package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// runtimeSettingsFile is the on-disk filename inside DynamicConfigsDir.
const runtimeSettingsFile = "runtime_settings.json"

// ReadPersistedSettings loads runtime_settings.json from DynamicConfigsDir.
// A missing file is not an error — the zero RuntimeSettings is returned.
// This lets callers update only the field they own (e.g. one branding
// asset filename) without clobbering unrelated settings already on disk.
func (o *ApplicationConfig) ReadPersistedSettings() (RuntimeSettings, error) {
	var settings RuntimeSettings
	if o.DynamicConfigsDir == "" {
		return settings, errors.New("DynamicConfigsDir is not set")
	}
	path := filepath.Join(o.DynamicConfigsDir, runtimeSettingsFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	return settings, nil
}

// WritePersistedSettings serialises the given RuntimeSettings to
// runtime_settings.json with restricted permissions (it may carry API
// keys and P2P tokens).
func (o *ApplicationConfig) WritePersistedSettings(settings RuntimeSettings) error {
	if o.DynamicConfigsDir == "" {
		return errors.New("DynamicConfigsDir is not set")
	}
	path := filepath.Join(o.DynamicConfigsDir, runtimeSettingsFile)
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
