package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// MergeNonNil overlays every set (non-nil) field of overlay onto the
// receiver, leaving the receiver's value untouched wherever overlay left a
// field unset. Every RuntimeSettings field is a pointer precisely so "set"
// can be told apart from "absent" (see the type doc), which makes this a
// faithful partial update: a caller that submits only the field it owns
// changes exactly that field and never clobbers unrelated settings.
//
// This is the read-modify-write contract the persistence helpers exist for.
// UpdateSettingsEndpoint reads the on-disk settings, merges the request body
// on top, and writes the result — so a focused admin page that POSTs only its
// own field (the Middleware page sends only mitm_listen; the detector table
// only pii_default_detectors) no longer nulls every other setting.
//
// Reflection keeps the merge total over the struct: a field added to
// RuntimeSettings later is merged automatically, so the persistence path can
// never silently drop a new setting the way a hand-maintained field list
// would. Non-pointer fields (none today) are skipped — they cannot express
// "absent", so the receiver wins.
func (s *RuntimeSettings) MergeNonNil(overlay RuntimeSettings) {
	dst := reflect.ValueOf(s).Elem()
	src := reflect.ValueOf(overlay)
	for i := 0; i < src.NumField(); i++ {
		f := src.Field(i)
		if f.Kind() == reflect.Pointer && !f.IsNil() {
			dst.Field(i).Set(f)
		}
	}
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
