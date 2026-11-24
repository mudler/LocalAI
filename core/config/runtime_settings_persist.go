package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mudler/xlog"
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
	MapRemovedRuntimeSettings(data, &settings)
	return settings, nil
}

// removedRuntimeSettings are the runtime setting keys that no longer exist.
// A stored or posted value is accepted; MapRemovedRuntimeSettings maps it.
var removedRuntimeSettings = []string{"localai_assistant_enabled"}

// RemovedRuntimeSettings returns the removed keys that data (a settings JSON
// object) contains.
func RemovedRuntimeSettings(data []byte) []string {
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil {
		return nil
	}
	var out []string
	for _, k := range removedRuntimeSettings {
		if _, ok := keys[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// MapRemovedRuntimeSettings maps the removed settings in data onto the current
// ones, logs one warning when data holds a removed setting, and returns the
// removed keys. A stored localai_assistant_enabled=false switches the admin
// tool group off (chats_localai_admin=false), the same as the old setting did
// for the assistant. An explicit chats_localai_admin in the same data wins, and
// a stored true changes nothing, since the group is on by default.
func MapRemovedRuntimeSettings(data []byte, settings *RuntimeSettings) []string {
	removed := RemovedRuntimeSettings(data)
	if len(removed) == 0 {
		return nil
	}
	var old struct {
		Enabled *bool `json:"localai_assistant_enabled"`
	}
	mapped := false
	if json.Unmarshal(data, &old) == nil && old.Enabled != nil && !*old.Enabled &&
		settings != nil && settings.ChatsLocalAIAdmin == nil {
		off := false
		settings.ChatsLocalAIAdmin = &off
		mapped = true
	}
	xlog.Warn("The localai_assistant_enabled runtime setting is deprecated; the admin tools are controlled by chats_localai_admin in Settings > Tools",
		"settings", strings.Join(removed, ", "), "mapped_to_chats_localai_admin", mapped)
	return removed
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

// MergeAPIKeys returns the env/CLI-provided keys followed by the
// runtime-managed (settings) keys, dropping runtime entries that duplicate
// an env key so repeated saves cannot stack the env keys (#9071). Env keys
// always survive: the settings file owns only the runtime tail.
func MergeAPIKeys(envKeys, runtimeKeys []string) []string {
	merged := append([]string(nil), envKeys...)
	seen := make(map[string]struct{}, len(envKeys))
	for _, k := range envKeys {
		seen[k] = struct{}{}
	}
	for _, k := range runtimeKeys {
		if _, dup := seen[k]; dup {
			continue
		}
		merged = append(merged, k)
	}
	return merged
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
